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
	"go.mau.fi/whatsmeow/types/events"

	appstore "whatevrd/internal/store"
)

const maxAvatarBytes = 5 * 1024 * 1024

const avatarNegativeCacheTTL = 7 * 24 * time.Hour

const avatarRefreshBatchSize = 100

const avatarRefreshMaxItems = 500

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
	if c.avatarRefreshBlockedByHistorySync(ctx) {
		return
	}

	for offset := 0; offset < avatarRefreshMaxItems; offset += avatarRefreshBatchSize {
		if c.avatarRefreshBlockedByHistorySync(ctx) {
			return
		}
		chats, err := c.store.ListChats(ctx, avatarRefreshBatchSize, offset)
		if err != nil {
			c.log.Warnf("Avatar refresh: failed to list chats: %v", err)
			return
		}
		if len(chats) == 0 {
			break
		}

		for _, chat := range chats {
			if ctx.Err() != nil || c.daemonHasActiveHistorySync() {
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

			existingPicID := chat.AvatarPictureID
			if !avatarLocalFileExists(chat.AvatarLocalPath) {
				existingPicID = ""
			}
			picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, existingPicID)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if status := avatarStatusForError(err); status != "" {
					_ = c.store.UpdateChatAvatarStatus(ctx, chat.ID, status)
					continue
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
	}

	if c.avatarRefreshBlockedByHistorySync(ctx) {
		return
	}
	senders, err := c.store.ListSendersForAvatarRefresh(ctx, avatarRefreshMaxItems)
	if err != nil {
		c.log.Warnf("Avatar refresh: failed to list senders: %v", err)
		return
	}
	for _, sender := range senders {
		c.refreshAvatarForSenderProfile(ctx, sender)
	}
}

func (c *Client) scheduleAvatarRefreshForSender(ctx context.Context, senderID string, delay time.Duration) {
	if strings.TrimSpace(senderID) == "" || senderID == "me" {
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
		c.refreshAvatarForSenderID(ctx, senderID)
	}()
}

func (c *Client) refreshAvatarForSenderID(ctx context.Context, senderID string) {
	sender, err := c.store.GetSenderProfile(ctx, senderID)
	if err != nil || sender.ID == "" {
		return
	}
	c.refreshAvatarForSenderProfile(ctx, sender)
}

func (c *Client) refreshSenderAvatarsForChat(ctx context.Context, chatID string, limit int) {
	if strings.TrimSpace(chatID) == "" || ctx.Err() != nil {
		return
	}
	jid, err := types.ParseJID(chatID)
	if err != nil || jid.Server != types.GroupServer {
		return
	}
	senders, err := c.store.ListSenderProfilesByChatID(ctx, chatID, limit)
	if err != nil {
		c.log.Warnf("Avatar refresh: failed to list senders for chat %s: %v", chatID, err)
		return
	}
	for _, sender := range senders {
		c.refreshAvatarForSenderProfile(ctx, sender)
	}
}

func (c *Client) refreshAvatarForSenderProfile(ctx context.Context, sender appstore.SenderProfile) {
	if ctx.Err() != nil || c.daemonHasActiveHistorySync() || !senderNeedsAvatarRefresh(sender) {
		return
	}
	jid, err := types.ParseJID(sender.ID)
	if err != nil || shouldSkipAvatarJID(jid) {
		return
	}
	// LID JIDs can't be used to fetch profile pictures; resolve to PN first.
	if jid.Server == types.HiddenUserServer {
		pn := c.normalizeJIDForChat(ctx, jid)
		if pn.IsEmpty() || pn.String() == jid.String() {
			return
		}
		jid = pn
	}
	existingPicID := sender.AvatarPictureID
	if !avatarLocalFileExists(sender.AvatarLocalPath) {
		existingPicID = ""
	}
	picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, existingPicID)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		if status := avatarStatusForError(err); status != "" {
			_ = c.store.UpdateSenderAvatarStatus(ctx, sender.ID, status)
			return
		}
		if !isTransientAvatarError(err) {
			c.log.Warnf("Avatar refresh: failed for sender %s: %v", sender.ID, err)
		}
		return
	}
	if picID == "" {
		return
	}
	if err := c.store.UpdateSenderAvatar(ctx, sender.ID, picID, localPath); err != nil {
		c.log.Warnf("Avatar refresh: failed to update sender DB for %s: %v", sender.ID, err)
		return
	}
	// Notify the frontend that messages in chats containing this sender now
	// have an updated avatar path, so it can reload visible message lists.
	chatIDs, err := c.store.ListChatIDsBySenderID(ctx, sender.ID)
	if err != nil {
		c.log.Warnf("Avatar refresh: failed to list chats for sender %s: %v", sender.ID, err)
		return
	}
	for _, chatID := range chatIDs {
		chat, err := c.store.GetChat(ctx, chatID)
		if err != nil || chat.ID == "" {
			continue
		}
		c.daemon.PublishChatUpdated(toDaemonChat(chat))
	}
}

func (c *Client) avatarRefreshBlockedByHistorySync(ctx context.Context) bool {
	if c.daemonHasActiveHistorySync() {
		return true
	}
	activeHistorySync, err := c.store.HasActiveHistorySyncChunks(ctx)
	if err != nil {
		c.log.Warnf("Avatar refresh: failed to check history sync state: %v", err)
		return true
	}
	return activeHistorySync
}

func (c *Client) daemonHasActiveHistorySync() bool {
	return c.daemon != nil && c.daemon.HasActiveHistorySync()
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

		c.refreshAvatarForChat(ctx, chat, false)
	}()
}

func (c *Client) refreshAvatarForChat(ctx context.Context, chat appstore.Chat, force bool) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || ctx.Err() != nil {
		return
	}
	if c.daemonHasActiveHistorySync() {
		return
	}
	if !force && !chatNeedsAvatarRefresh(chat) {
		return
	}

	jid, err := types.ParseJID(chat.ID)
	if err != nil || shouldSkipAvatarJID(jid) {
		return
	}

	existingPicID := chat.AvatarPictureID
	if !avatarLocalFileExists(chat.AvatarLocalPath) || force {
		existingPicID = ""
	}
	picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, existingPicID)
	if err != nil {
		if ctx.Err() != nil || isTransientAvatarError(err) {
			return
		}
		if status := avatarStatusForError(err); status != "" {
			_ = c.store.UpdateChatAvatarStatus(ctx, chat.ID, status)
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

func (c *Client) handlePictureEvent(ctx context.Context, evt *events.Picture) {
	if evt == nil || ctx.Err() != nil {
		return
	}
	jid := c.normalizeJIDForChat(ctx, evt.JID)
	if jid.IsEmpty() || shouldSkipAvatarJID(jid) {
		return
	}
	id := jid.String()
	if evt.Remove {
		_ = c.store.ClearChatAvatar(ctx, id, appstore.AvatarStatusNotSet)
		_ = c.store.ClearSenderAvatar(ctx, id, appstore.AvatarStatusNotSet)
		if chat, err := c.store.GetChat(ctx, id); err == nil && chat.ID != "" {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
		return
	}

	if chat, err := c.store.GetChat(ctx, id); err == nil && chat.ID != "" {
		c.refreshAvatarForChat(ctx, chat, true)
	}
	c.refreshAvatarForSender(ctx, jid, id)
}

func (c *Client) refreshAvatarForSender(ctx context.Context, jid types.JID, senderID string) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || ctx.Err() != nil {
		return
	}
	if c.daemonHasActiveHistorySync() {
		return
	}
	if jid.Server == types.HiddenUserServer {
		pn := c.normalizeJIDForChat(ctx, jid)
		if pn.IsEmpty() || pn.String() == jid.String() {
			return
		}
		jid = pn
	}
	picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, "")
	if err != nil {
		if status := avatarStatusForError(err); status != "" {
			_ = c.store.UpdateSenderAvatarStatus(ctx, senderID, status)
			return
		}
		if ctx.Err() == nil && !isTransientAvatarError(err) {
			c.log.Warnf("Avatar refresh: failed for sender %s: %v", senderID, err)
		}
		return
	}
	if picID == "" {
		return
	}
	if err := c.store.UpdateSenderAvatar(ctx, senderID, picID, localPath); err != nil {
		c.log.Warnf("Avatar refresh: failed to update sender DB for %s: %v", senderID, err)
		return
	}
	chatIDs, err := c.store.ListChatIDsBySenderID(ctx, senderID)
	if err != nil {
		return
	}
	for _, chatID := range chatIDs {
		chat, err := c.store.GetChat(ctx, chatID)
		if err == nil && chat.ID != "" {
			c.daemon.PublishChatUpdated(toDaemonChat(chat))
		}
	}
}

func chatNeedsAvatarRefresh(chat appstore.Chat) bool {
	if strings.TrimSpace(chat.AvatarPictureID) != "" && avatarLocalFileExists(chat.AvatarLocalPath) {
		return false
	}
	if chat.AvatarStatus != "" && time.Since(time.Unix(chat.AvatarCheckedAt, 0)) < avatarNegativeCacheTTL {
		return false
	}
	return true
}

func senderNeedsAvatarRefresh(sender appstore.SenderProfile) bool {
	if strings.TrimSpace(sender.AvatarPictureID) != "" && avatarLocalFileExists(sender.AvatarLocalPath) {
		return false
	}
	if sender.AvatarStatus != "" && time.Since(time.Unix(sender.AvatarCheckedAt, 0)) < avatarNegativeCacheTTL {
		return false
	}
	return true
}

func avatarLocalFileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func shouldSkipAvatarJID(jid types.JID) bool {
	return jid.IsEmpty() || jid.User == "0" || jid.Server == types.BroadcastServer || jid.Server == types.NewsletterServer
}

func isTransientAvatarError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "websocket not connected") ||
		strings.Contains(message, "websocket disconnected before info query returned response")
}

func avatarStatusForError(err error) string {
	if errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) {
		return appstore.AvatarStatusUnauthorized
	}
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) {
		return appstore.AvatarStatusNotSet
	}
	return ""
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
