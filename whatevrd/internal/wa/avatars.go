package wa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

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

		jid, err := types.ParseJID(chat.ID)
		if err != nil {
			continue
		}

		picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, chat.AvatarPictureID)
		if err != nil {
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

func (c *Client) fetchAndCacheAvatar(ctx context.Context, jid types.JID, existingPicID string) (picID, localPath string, err error) {
	client := c.currentClient()
	if client == nil {
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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
