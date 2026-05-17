package wa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	appstore "whatevrd/internal/store"
)

const maxAvatarBytes = 5 * 1024 * 1024

func (c *Client) scheduleAvatarRefresh(ctx context.Context, delay time.Duration) {
	go func() {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		c.runAvatarRefresh(ctx)
	}()
}

func (c *Client) runAvatarRefresh(ctx context.Context) {
	c.avatarMu.Lock()
	if c.avatarRefreshRunning {
		c.avatarMu.Unlock()
		return
	}
	c.avatarRefreshRunning = true
	c.avatarMu.Unlock()

	defer func() {
		c.avatarMu.Lock()
		c.avatarRefreshRunning = false
		c.avatarMu.Unlock()
	}()

	c.refreshAvatarsBackground(ctx)
}

func (c *Client) refreshAvatarsBackground(ctx context.Context) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return
	}

	chats, err := c.store.ListChats(ctx, 200, 0)
	if err != nil {
		c.log.Warnf("Avatar refresh: failed to list chats: %v", err)
		return
	}

	for _, chat := range chats {
		if ctx.Err() != nil {
			return
		}
		if !chatNeedsAvatarRefresh(chat) {
			continue
		}

		jid, err := types.ParseJID(chat.ID)
		if err != nil {
			continue
		}
		if shouldSkipAvatarJID(jid) {
			continue
		}

		picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, chat.AvatarPictureID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isTransientAvatarError(err) {
				continue
			}
			c.log.Warnf("Avatar refresh: failed for %s: %v", chat.ID, err)
			continue
		}
		if picID == "" {
			continue
		}

		if err := c.store.UpdateChatAvatar(ctx, chat.ID, picID, localPath); err != nil {
			c.log.Warnf("Avatar refresh: failed to update DB for %s: %v", chat.ID, err)
			continue
		}

		updatedChat, err := c.store.GetChat(ctx, chat.ID)
		if err != nil {
			continue
		}
		c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
	}

	senders, err := c.store.ListSendersNeedingAvatar(ctx, 200)
	if err != nil {
		c.log.Warnf("Avatar refresh: failed to list senders: %v", err)
		return
	}
	for _, sender := range senders {
		if ctx.Err() != nil {
			return
		}
		jid, err := types.ParseJID(sender.ID)
		if err != nil || shouldSkipAvatarJID(jid) {
			continue
		}
		picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, sender.AvatarPictureID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isTransientAvatarError(err) {
				continue
			}
			c.log.Warnf("Avatar refresh: failed for sender %s: %v", sender.ID, err)
			continue
		}
		if picID == "" {
			continue
		}
		if err := c.store.UpdateSenderAvatar(ctx, sender.ID, picID, localPath); err != nil {
			c.log.Warnf("Avatar refresh: failed to update sender DB for %s: %v", sender.ID, err)
		}
	}
}

func (c *Client) scheduleAvatarRefreshForChat(ctx context.Context, chat appstore.Chat, delay time.Duration) {
	if !chatNeedsAvatarRefresh(chat) {
		return
	}

	go func() {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}

		c.refreshAvatarForChat(ctx, chat)
	}()
}

func (c *Client) refreshAvatarForChat(ctx context.Context, chat appstore.Chat) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || ctx.Err() != nil {
		return
	}

	jid, err := types.ParseJID(chat.ID)
	if err != nil || shouldSkipAvatarJID(jid) {
		return
	}

	picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, chat.AvatarPictureID)
	if err != nil {
		if ctx.Err() != nil || isTransientAvatarError(err) {
			return
		}
		c.log.Warnf("Avatar refresh: failed for %s: %v", chat.ID, err)
		return
	}
	if picID == "" {
		return
	}

	if err := c.store.UpdateChatAvatar(ctx, chat.ID, picID, localPath); err != nil {
		c.log.Warnf("Avatar refresh: failed to update DB for %s: %v", chat.ID, err)
		return
	}

	updatedChat, err := c.store.GetChat(ctx, chat.ID)
	if err != nil {
		return
	}
	c.daemon.PublishChatUpdated(toDaemonChat(updatedChat))
}

func chatNeedsAvatarRefresh(chat appstore.Chat) bool {
	return strings.TrimSpace(chat.AvatarPictureID) == "" || strings.TrimSpace(chat.AvatarLocalPath) == ""
}

func shouldSkipAvatarJID(jid types.JID) bool {
	return jid.IsEmpty() || jid.Server == types.BroadcastServer || jid.Server == types.NewsletterServer
}

func isTransientAvatarError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "websocket not connected") ||
		strings.Contains(message, "websocket disconnected before info query returned response")
}

func (c *Client) fetchAndCacheAvatar(ctx context.Context, jid types.JID, existingPicID string) (picID, localPath string, err error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || ctx.Err() != nil {
		return "", "", nil
	}

	info, err := client.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{
		Preview:    false,
		ExistingID: existingPicID,
	})
	if err != nil {
		if errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) ||
			errors.Is(err, whatsmeow.ErrProfilePictureNotSet) {
			return "", "", nil
		}
		return "", "", err
	}
	if info == nil {
		// nil means unchanged
		return "", "", nil
	}

	avatarDir := filepath.Join(c.paths.MediaCacheDir, "avatars")
	if err := os.MkdirAll(avatarDir, 0o700); err != nil {
		return "", "", err
	}

	safeID := strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(jid.String())
	destPath := filepath.Join(avatarDir, safeID+".jpg")

	if err := downloadFile(ctx, info.URL, destPath); err != nil {
		return "", "", err
	}

	return info.ID, destPath, nil
}

func downloadFile(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return errors.New("avatar download returned non-success status")
	}
	if resp.ContentLength > maxAvatarBytes {
		return errors.New("avatar image is too large")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxAvatarBytes {
		return errors.New("avatar image size is outside allowed bounds")
	}
	if _, ok := outboundImageExtension(http.DetectContentType(data)); !ok {
		return errors.New("avatar image has unsupported content type")
	}

	return writeFileAtomic(destPath, data, 0o600)
}
