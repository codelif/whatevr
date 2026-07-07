package wa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	waE2E "go.mau.fi/whatsmeow/proto/waE2E"
	waHistorySync "go.mau.fi/whatsmeow/proto/waHistorySync"
	waSyncAction "go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

const (
	// Public first-party sticker store endpoints (the same ones WhatsApp Web
	// uses). The index lists every official pack; tray/preview art is served
	// unencrypted by image ID. Individual pack stickers are regular encrypted
	// media fetched via whatsmeow.
	stickerStoreIndexURL = "https://static.whatsapp.net/sticker?cat=all&lg=en&lottie=1"
	stickerStoreImageURL = "https://static.whatsapp.net/sticker?img="

	stickerStoreIndexTTL   = 24 * time.Hour
	stickerStoreFetchedKey = "sticker_store_fetched_at"

	// Run-once marker for the WebP animation-flag backfill. Stickers cached
	// before byte-level animation detection existed were stored is_animated=0;
	// the backfill re-inspects their headers exactly once.
	stickerAnimBackfillKey = "sticker_webp_anim_backfill_done"

	// The frontend bounds itself to a small in-flight download pool, so the
	// daemon only needs enough parallelism to keep that pool fed; more just
	// holds gRPC streams open longer.
	stickerDownloadConcurrency = 8
	stickerDownloadTimeout     = 30 * time.Second
	stickerLibraryDebounce     = 250 * time.Millisecond

	// How long a cached upload (our own media keys) is trusted for re-sends
	// before re-uploading. WhatsApp media URL lifetime is undocumented; a
	// stale reuse falls back to a fresh upload on send failure anyway.
	stickerUploadReuseTTL = 7 * 24 * time.Hour
)

type stickerFileDownloadState struct {
	done    chan struct{}
	sticker appstore.Sticker
	err     error
}

// ListStickers serves the picker's library views straight from sqlite.
func (c *Client) ListStickers(ctx context.Context, source app.StickerSource, query string, limit int) ([]appstore.Sticker, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if query = strings.TrimSpace(query); query != "" {
		return c.store.SearchStickers(ctx, query, limit)
	}
	switch source {
	case app.StickerSourceRecent:
		return c.store.ListRecentStickers(ctx, limit)
	case app.StickerSourceFavorite:
		return c.store.ListFavoriteStickers(ctx, limit)
	default:
		return c.store.ListAllStickers(ctx, limit)
	}
}

func (c *Client) ListStickerPacks(ctx context.Context, forceRefresh bool) ([]appstore.StickerPack, error) {
	if err := c.refreshStickerStoreIndex(ctx, forceRefresh); err != nil {
		// Degrade to whatever the cache has; the store endpoint is best-effort.
		c.log.Warnf("Failed to refresh sticker store index: %v", err)
	}
	return c.store.ListStickerPacks(ctx)
}

func (c *Client) GetStickerPack(ctx context.Context, packID string) (appstore.StickerPack, []appstore.Sticker, error) {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return appstore.StickerPack{}, nil, app.NewCommandError(app.CommandErrorInvalidArgument, "pack_id is required")
	}
	pack, ok, err := c.store.GetStickerPack(ctx, packID)
	if err != nil {
		return appstore.StickerPack{}, nil, err
	}
	if !ok {
		return appstore.StickerPack{}, nil, app.NewCommandError(app.CommandErrorNotFound, "sticker pack not found")
	}
	if pack.ContentsFetchedAt == 0 {
		if err := c.fetchStickerPackContents(ctx, packID); err != nil {
			return appstore.StickerPack{}, nil, err
		}
		if pack, ok, err = c.store.GetStickerPack(ctx, packID); err != nil || !ok {
			return appstore.StickerPack{}, nil, err
		}
	}
	stickers, err := c.store.ListPackStickers(ctx, packID)
	if err != nil {
		return appstore.StickerPack{}, nil, err
	}
	return pack, stickers, nil
}

func (c *Client) DownloadSticker(ctx context.Context, cacheKey string) (appstore.Sticker, error) {
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorInvalidArgument, "cache_key is required")
	}
	return c.ensureStickerFile(ctx, cacheKey, false)
}

func (c *Client) SetStickerPackInstalled(ctx context.Context, packID string, installed bool) (appstore.StickerPack, error) {
	packID = strings.TrimSpace(packID)
	if packID == "" {
		return appstore.StickerPack{}, app.NewCommandError(app.CommandErrorInvalidArgument, "pack_id is required")
	}
	if _, ok, err := c.store.GetStickerPack(ctx, packID); err != nil {
		return appstore.StickerPack{}, err
	} else if !ok {
		return appstore.StickerPack{}, app.NewCommandError(app.CommandErrorNotFound, "sticker pack not found")
	}
	if err := c.store.SetStickerPackInstalled(ctx, packID, installed, time.Now()); err != nil {
		return appstore.StickerPack{}, err
	}
	if installed {
		// Warm the pack in the background so the category tab is instant by
		// the time the user opens it. Failures stay silent; the picker
		// downloads on demand anyway.
		go c.warmStickerPack(c.backgroundContext(), packID)
	}
	c.publishStickerLibraryChangedDebounced(app.StickerSourceUnspecified)
	pack, _, err := c.store.GetStickerPack(ctx, packID)
	return pack, err
}

func (c *Client) warmStickerPack(ctx context.Context, packID string) {
	pack, ok, err := c.store.GetStickerPack(ctx, packID)
	if err != nil || !ok {
		return
	}
	if pack.ContentsFetchedAt == 0 {
		if err := c.fetchStickerPackContents(ctx, packID); err != nil {
			c.log.Warnf("Failed to fetch sticker pack %s for warmup: %v", packID, err)
			return
		}
	}
	stickers, err := c.store.ListPackStickers(ctx, packID)
	if err != nil {
		return
	}
	for _, sticker := range stickers {
		if ctx.Err() != nil {
			return
		}
		if sticker.LocalPath != "" {
			if _, err := os.Stat(sticker.LocalPath); err == nil {
				continue
			}
		}
		if _, err := c.ensureStickerFile(ctx, sticker.CacheKey, false); err != nil {
			c.log.Warnf("Failed to warm sticker %s from pack %s: %v", sticker.CacheKey, packID, err)
		}
	}
}

// refreshStickerStoreIndex pulls the official pack index at most once per TTL.
// The response decodes directly into whatsmeow's types.StickerPack (it carries
// the right JSON tags); only pack metadata is stored — no media is fetched
// here beyond the tiny tray PNGs.
func (c *Client) refreshStickerStoreIndex(ctx context.Context, force bool) error {
	c.stickerIndexMu.Lock()
	defer c.stickerIndexMu.Unlock()

	if !force {
		if raw, err := c.store.GetAppStateValue(ctx, stickerStoreFetchedKey); err == nil && raw != "" {
			if fetchedAt, err := strconv.ParseInt(raw, 10, 64); err == nil {
				if time.Since(time.Unix(fetchedAt, 0)) < stickerStoreIndexTTL {
					return nil
				}
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stickerStoreIndexURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.stickerHTTP().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sticker store index returned status %d", resp.StatusCode)
	}

	var indexPacks []types.StickerPack
	if err := json.NewDecoder(resp.Body).Decode(&indexPacks); err != nil {
		return fmt.Errorf("decode sticker store index: %w", err)
	}
	if len(indexPacks) == 0 {
		return fmt.Errorf("sticker store index is empty")
	}

	packs := make([]appstore.StickerPack, 0, len(indexPacks))
	for i, p := range indexPacks {
		if p.StickerPackID == "" {
			continue
		}
		packs = append(packs, appstore.StickerPack{
			ID:            p.StickerPackID,
			Name:          p.Name,
			Publisher:     p.Publisher,
			Description:   p.Description,
			Animated:      p.Animated != 0,
			Lottie:        p.Lottie != 0,
			TrayImageID:   p.TrayImageID,
			ImageDataHash: p.ImageDataHash,
			StoreOrder:    int32(i),
		})
	}
	if err := c.store.UpsertStickerPacks(ctx, packs); err != nil {
		return err
	}
	if err := c.store.SetAppStateValue(ctx, stickerStoreFetchedKey, strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		return err
	}

	c.fetchMissingTrayImages(ctx)
	return nil
}

func (c *Client) fetchMissingTrayImages(ctx context.Context) {
	packs, err := c.store.ListStickerPacks(ctx)
	if err != nil {
		return
	}
	trayDir := filepath.Join(c.paths.MediaCacheDir, "stickers", "trays")
	if err := os.MkdirAll(trayDir, 0o700); err != nil {
		c.log.Warnf("Failed to create sticker tray directory: %v", err)
		return
	}

	sem := make(chan struct{}, stickerDownloadConcurrency)
	done := make(chan struct{})
	pendingJobs := 0
	for _, pack := range packs {
		if pack.TrayImageID == "" {
			continue
		}
		if pack.TrayLocalPath != "" {
			if _, err := os.Stat(pack.TrayLocalPath); err == nil {
				continue
			}
		}
		pendingJobs++
		go func(pack appstore.StickerPack) {
			defer func() { done <- struct{}{} }()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			path, err := c.downloadTrayImage(ctx, trayDir, pack.TrayImageID)
			if err != nil {
				c.log.Warnf("Failed to fetch tray image for pack %s: %v", pack.ID, err)
				return
			}
			if err := c.store.SetStickerPackTrayPath(ctx, pack.ID, path); err != nil {
				c.log.Warnf("Failed to record tray image for pack %s: %v", pack.ID, err)
			}
		}(pack)
	}
	for ; pendingJobs > 0; pendingJobs-- {
		<-done
	}
}

func (c *Client) downloadTrayImage(ctx context.Context, trayDir, imageID string) (string, error) {
	path := filepath.Join(trayDir, safeFilenamePart(imageID)+".png")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, stickerStoreImageURL+url.QueryEscape(imageID), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.stickerHTTP().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tray image returned status %d", resp.StatusCode)
	}
	data, err := readLimitedBody(resp.Body, 1<<20)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// fetchStickerPackContents imports a pack's sticker metadata (media keys,
// emoji tags) without downloading any files. The emoji tags double as the
// picker's search corpus.
func (c *Client) fetchStickerPackContents(ctx context.Context, packID string) error {
	client := c.currentClient()
	if client == nil {
		return app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	pack, err := client.FetchStickerPack(ctx, packID)
	if err != nil {
		return app.NewCommandError(app.CommandErrorNotConnected, "fetch sticker pack: %v", err)
	}
	if err := c.store.ClearPackStickerMembership(ctx, packID); err != nil {
		return err
	}

	count := int32(0)
	for i, item := range pack.Stickers {
		if item == nil {
			continue
		}
		cacheKey := ""
		encKey := hex.EncodeToString(item.EncFileHash)
		if len(item.FileHash) > 0 {
			cacheKey = hex.EncodeToString(item.FileHash)
		} else if encKey != "" {
			cacheKey = "enc:" + encKey
		} else {
			continue
		}

		mimeType := strings.TrimSpace(item.MimeType)
		if mimeType == "" {
			mimeType = "image/webp"
		}
		isLottie := isWhatsAppAnimatedSticker(mimeType)
		stickerMsg := &waE2E.StickerMessage{
			URL:           proto.String(item.URL),
			DirectPath:    proto.String(item.DirectPath),
			MediaKey:      item.MediaKey,
			FileEncSHA256: item.EncFileHash,
			FileSHA256:    item.FileHash,
			FileLength:    proto.Uint64(uint64(item.FileSize)),
			Mimetype:      proto.String(mimeType),
			Width:         proto.Uint32(uint32(item.Width)),
			Height:        proto.Uint32(uint32(item.Height)),
			IsAnimated:    proto.Bool(isLottie),
		}
		payload, err := proto.Marshal(stickerMsg)
		if err != nil {
			continue
		}
		if err := c.store.UpsertPackSticker(ctx, appstore.Sticker{
			CacheKey:          cacheKey,
			EncCacheKey:       encKey,
			MimeType:          mimeType,
			IsAnimated:        isLottie,
			Width:             int32(item.Width),
			Height:            int32(item.Height),
			StickerPayload:    payload,
			Emojis:            strings.Join(item.Emojis, " "),
			AccessibilityText: item.AccessibilityText,
			PackID:            packID,
			PackOrder:         int32(i),
		}); err != nil {
			return err
		}
		count++
	}
	if err := c.store.MarkStickerPackContentsFetched(ctx, packID, count, time.Now()); err != nil {
		return err
	}
	// Announce the fetched contents so an open `sticker_packs` list (now
	// contents_fetched/sticker_count) and any `sticker_pack` subscription that did
	// not itself trigger the fetch refresh; source Unspecified matches every view.
	c.publishStickerLibraryChangedDebounced(app.StickerSourceUnspecified)
	return nil
}

// ensureStickerFile guarantees a sticker's display file exists on disk
// (downloading and, for Lottie packs, extracting it if needed) and rekeys
// provisional rows to their plaintext content hash. With needArchive, the
// original .zip is re-fetched for Lottie stickers whose archive was never
// kept — sending uploads the archive, not the extracted JSON.
func (c *Client) ensureStickerFile(ctx context.Context, cacheKey string, needArchive bool) (appstore.Sticker, error) {
	c.stickerMu.Lock()
	if existing := c.stickerDownloads[cacheKey]; existing != nil {
		done := existing.done
		c.stickerMu.Unlock()
		select {
		case <-done:
			if existing.err != nil || !needArchive || existing.sticker.ArchivePath != "" || !isWhatsAppAnimatedSticker(existing.sticker.MimeType) {
				return existing.sticker, existing.err
			}
			// Fall through: the finished download did not produce the archive
			// this caller needs.
		case <-ctx.Done():
			return appstore.Sticker{}, ctx.Err()
		}
		c.stickerMu.Lock()
	}
	state := &stickerFileDownloadState{done: make(chan struct{})}
	c.stickerDownloads[cacheKey] = state
	c.stickerMu.Unlock()

	defer func() {
		c.stickerMu.Lock()
		if c.stickerDownloads[cacheKey] == state {
			delete(c.stickerDownloads, cacheKey)
		}
		close(state.done)
		c.stickerMu.Unlock()
	}()

	state.sticker, state.err = c.ensureStickerFileLocked(ctx, cacheKey, needArchive)
	return state.sticker, state.err
}

func (c *Client) ensureStickerFileLocked(ctx context.Context, cacheKey string, needArchive bool) (appstore.Sticker, error) {
	sticker, ok, err := c.store.GetSticker(ctx, cacheKey)
	if err != nil {
		return appstore.Sticker{}, err
	}
	if !ok {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorNotFound, "sticker not found")
	}

	isLottie := isWhatsAppAnimatedSticker(sticker.MimeType)
	if sticker.LocalPath != "" && !appstore.IsProvisionalStickerKey(sticker.CacheKey) {
		if _, statErr := os.Stat(sticker.LocalPath); statErr == nil {
			if !needArchive || !isLottie {
				return sticker, nil
			}
			if sticker.ArchivePath != "" {
				if _, statErr := os.Stat(sticker.ArchivePath); statErr == nil {
					return sticker, nil
				}
			}
		}
	}

	if len(sticker.StickerPayload) == 0 {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorRejected, "sticker has no download metadata")
	}
	var stickerMsg waE2E.StickerMessage
	if err := proto.Unmarshal(sticker.StickerPayload, &stickerMsg); err != nil {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorInternal, "decode sticker metadata: %v", err)
	}

	client := c.currentClient()
	if client == nil || !client.IsConnected() {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp is not connected")
	}

	c.stickerDownloadSem <- struct{}{}
	defer func() { <-c.stickerDownloadSem }()
	if ctx.Err() != nil {
		return appstore.Sticker{}, ctx.Err()
	}

	// Bound the network fetch so a hung media server frees this semaphore slot
	// (and the caller's gRPC stream) instead of blocking the picker's download
	// pool indefinitely.
	dlCtx, cancel := context.WithTimeout(ctx, stickerDownloadTimeout)
	defer cancel()

	var downloadable whatsmeow.DownloadableMessage = &stickerMsg
	if stickerMsg.GetDirectPath() != "" && isPlaceholderMediaURL(stickerMsg.GetURL()) {
		downloadable = stickerDownloadable{StickerMessage: &stickerMsg}
	}
	data, err := client.Download(dlCtx, downloadable)
	if err != nil {
		// Favorites carry only an encrypted file hash (FileEncSHA256), never a
		// plaintext FileSHA256, so whatsmeow's plaintext-hash check always
		// fails — but it still MAC-verifies the ciphertext and returns the
		// decrypted bytes alongside the error. With no plaintext hash to honour
		// in the first place, those bytes are the sticker: use them.
		if errors.Is(err, whatsmeow.ErrInvalidMediaSHA256) && len(stickerMsg.GetFileSHA256()) == 0 && len(data) > 0 {
			err = nil
		}
	}
	if err != nil {
		c.log.Debugf("Sticker download failed for %s: %v", cacheKey, err)
		// A stale media path (403/404/410) is terminal: favorites have no pack
		// to re-fetch and no chat message for a media-retry receipt, so the
		// path can never be refreshed. Report it as non-retryable so the client
		// marks the tile unavailable instead of hammering the dead path.
		if staleMediaDownloadError(err) {
			return appstore.Sticker{}, app.NewCommandError(app.CommandErrorNotFound, "sticker media is no longer available: %v", err)
		}
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorNotConnected, "download sticker: %v", err)
	}
	if len(data) == 0 || len(data) > maxOutboundMediaBytes {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorRejected, "sticker file size out of range")
	}

	// Canonicalize identity on the plaintext hash so favorites merge with the
	// same sticker seen in chats or packs.
	canonicalKey := sticker.CacheKey
	if appstore.IsProvisionalStickerKey(sticker.CacheKey) {
		digest := sha256.Sum256(data)
		canonicalKey = hex.EncodeToString(digest[:])
		if sticker, err = c.store.RekeySticker(ctx, sticker.CacheKey, canonicalKey); err != nil {
			return appstore.Sticker{}, err
		}
	}

	stickerDir := filepath.Join(c.paths.MediaCacheDir, "stickers")
	if err := os.MkdirAll(stickerDir, 0o700); err != nil {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorInternal, "create sticker cache directory: %v", err)
	}

	localPath := filepath.Join(stickerDir, canonicalKey+mediaExtension(sticker.MimeType))
	archivePath := sticker.ArchivePath
	if err := writeFileAtomic(localPath, data, 0o600); err != nil {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorInternal, "write sticker cache file: %v", err)
	}
	// WhatsApp's library metadata can't tell an animated WebP from a static one
	// (both are image/webp with no animation flag), so detect it from the bytes
	// and persist the corrected flag. Lottie is always animated.
	isAnimated := sticker.IsAnimated
	if isLottie {
		isAnimated = true
		archivePath = localPath
		localPath, err = extractLottieSticker(archivePath)
		if err != nil {
			return appstore.Sticker{}, err
		}
	} else if strings.EqualFold(strings.TrimSpace(sticker.MimeType), "image/webp") {
		isAnimated = isAnimatedWebP(data)
	}

	if err := c.store.SetStickerFile(ctx, canonicalKey, localPath, archivePath, isAnimated); err != nil {
		return appstore.Sticker{}, err
	}
	sticker.CacheKey = canonicalKey
	sticker.LocalPath = localPath
	sticker.ArchivePath = archivePath
	sticker.IsAnimated = isAnimated

	c.daemon.PublishStickerDownloadChanged(toDaemonSticker(sticker), "")
	return sticker, nil
}

// backfillAnimatedWebPFlags re-inspects already-cached WebP stickers that were
// stored before byte-level animation detection existed and corrects their
// is_animated flag. Without it, stickers already in the cache stay static in
// the picker (the picker never re-downloads a tile it considers downloaded).
// It runs at most once, guarded by an app_state marker, and only reads each
// file's header — never the whole file — so it stays cheap even for large
// libraries.
func (c *Client) backfillAnimatedWebPFlags(ctx context.Context) {
	if done, err := c.store.GetAppStateValue(ctx, stickerAnimBackfillKey); err == nil && done != "" {
		return
	}

	stickers, err := c.store.ListDownloadedWebPStickersUnflagged(ctx)
	if err != nil {
		c.log.Warnf("Failed to list stickers for animation backfill: %v", err)
		return
	}

	changed := false
	for _, sticker := range stickers {
		if ctx.Err() != nil {
			return
		}
		if !fileLooksAnimatedWebP(sticker.LocalPath) {
			continue
		}
		if err := c.store.SetStickerAnimated(ctx, sticker.CacheKey, true); err != nil {
			c.log.Warnf("Failed to flag sticker %s as animated: %v", sticker.CacheKey, err)
			continue
		}
		changed = true
	}

	if err := c.store.SetAppStateValue(ctx, stickerAnimBackfillKey, strconv.FormatInt(time.Now().Unix(), 10)); err != nil {
		c.log.Warnf("Failed to record sticker animation backfill marker: %v", err)
	}
	if changed {
		c.publishStickerLibraryChangedDebounced(app.StickerSourceUnspecified)
	}
}

// fileLooksAnimatedWebP reads just the WebP header to classify a cached file.
func fileLooksAnimatedWebP(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	header := make([]byte, 32)
	n, _ := io.ReadFull(f, header)
	return isAnimatedWebP(header[:n])
}

// SendSticker queues a sticker from the library to a chat. The file is
// ensured locally first so the optimistic bubble renders immediately; the
// actual upload+send happens on the send queue like every other outgoing
// message (surviving daemon restarts).
func (c *Client) SendSticker(ctx context.Context, chatID, cacheKey, replyToMessageID string) (appstore.SavedTextMessage, error) {
	client := c.currentClient()
	if client == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotConnected, "WhatsApp client is not initialized")
	}
	if client.Store.ID == nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp session is not logged in")
	}
	cacheKey = strings.TrimSpace(cacheKey)
	if cacheKey == "" {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "cache_key is required")
	}

	sticker, err := c.ensureStickerFile(ctx, cacheKey, true)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	targetJID, err := types.ParseJID(chatID)
	if err != nil {
		return appstore.SavedTextMessage{}, app.NewCommandError(app.CommandErrorInvalidArgument, "invalid chat_id: %v", err)
	}
	targetJID = c.normalizeJIDForChat(ctx, targetJID)
	chatID = targetJID.String()
	replyTo, err := c.replySnapshotForSend(ctx, chatID, replyToMessageID)
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	width, height := sticker.Width, sticker.Height
	if width <= 0 || height <= 0 {
		width, height = 512, 512
	}
	messageID := client.GenerateMessageID()
	saved, err := c.store.SaveMediaMessage(ctx, appstore.MediaMessageInput{
		TextMessageInput: appstore.TextMessageInput{
			ID:          internalMessageIDForChat(chatID, messageID),
			ChatID:      chatID,
			SenderID:    "me",
			Timestamp:   time.Now(),
			Direction:   appstore.DirectionOutgoing,
			Status:      appstore.StatusPending,
			IsGroup:     targetJID.Server == types.GroupServer || targetJID.Server == types.BroadcastServer,
			CountUnread: false,
			ReplyTo:     replyTo,
		},
		MediaKind:      appstore.MediaKindSticker,
		MediaMimeType:  sticker.MimeType,
		MediaLocalPath: sticker.LocalPath,
		MediaWidth:     width,
		MediaHeight:    height,
		MediaAnimated:  sticker.IsAnimated,
		MediaPayload:   sticker.StickerPayload,
		MediaCacheKey:  sticker.CacheKey,
	})
	if err != nil {
		return appstore.SavedTextMessage{}, err
	}

	if saved.Inserted {
		c.log.Infof("Queued sticker message %s to %s", saved.Message.ID, chatID)
		c.daemon.PublishNewMessage(toDaemonMessage(saved.Message), toDaemonChat(saved.Chat))
	}
	c.signalSendQueue()

	if err := c.store.MarkStickerUsed(ctx, sticker.CacheKey, time.Now()); err == nil {
		c.publishStickerLibraryChangedDebounced(app.StickerSourceRecent)
	}
	return saved, nil
}

// uploadStickerForSend uploads the cached sticker file (the original .zip
// archive for Lottie packs — recipients expect the archive, not the
// extracted JSON) and returns a StickerMessage carrying our own media keys.
func (c *Client) uploadStickerForSend(ctx context.Context, client *whatsmeow.Client, sticker appstore.Sticker) (*waE2E.StickerMessage, error) {
	path := sticker.LocalPath
	if isWhatsAppAnimatedSticker(sticker.MimeType) {
		if sticker.ArchivePath == "" {
			refreshed, err := c.ensureStickerFile(ctx, sticker.CacheKey, true)
			if err != nil {
				return nil, err
			}
			sticker = refreshed
		}
		path = sticker.ArchivePath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read sticker file: %w", err)
	}

	resp, err := client.Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return nil, fmt.Errorf("upload sticker: %w", err)
	}

	width, height := uint32(sticker.Width), uint32(sticker.Height)
	if width == 0 || height == 0 {
		width, height = 512, 512
	}
	isLottie := isWhatsAppAnimatedSticker(sticker.MimeType)
	stickerMsg := &waE2E.StickerMessage{
		URL:           &resp.URL,
		DirectPath:    &resp.DirectPath,
		MediaKey:      resp.MediaKey,
		FileEncSHA256: resp.FileEncSHA256,
		FileSHA256:    resp.FileSHA256,
		FileLength:    &resp.FileLength,
		Mimetype:      proto.String(sticker.MimeType),
		Width:         proto.Uint32(width),
		Height:        proto.Uint32(height),
		IsAnimated:    proto.Bool(sticker.IsAnimated),
	}
	if isLottie {
		stickerMsg.IsLottie = proto.Bool(true)
	}
	// Reuse the original sticker's inline PNG thumbnail when we have one so
	// quotes of this sticker render a preview.
	if len(sticker.StickerPayload) > 0 {
		var original waE2E.StickerMessage
		if proto.Unmarshal(sticker.StickerPayload, &original) == nil && len(original.GetPngThumbnail()) > 0 {
			stickerMsg.PngThumbnail = original.GetPngThumbnail()
		}
	}
	return stickerMsg, nil
}

// ingestRecentStickers seeds the picker's Recents from history sync. Files
// are downloaded lazily when the picker first shows them.
func (c *Client) ingestRecentStickers(ctx context.Context, recents []*waHistorySync.StickerMetadata) {
	if len(recents) == 0 {
		return
	}
	imported := 0
	for _, meta := range recents {
		if meta == nil || len(meta.GetFileSHA256()) == 0 {
			continue
		}
		mimeType := strings.TrimSpace(meta.GetMimetype())
		if mimeType == "" {
			mimeType = "image/webp"
		}
		stickerMsg := &waE2E.StickerMessage{
			URL:           proto.String(meta.GetURL()),
			DirectPath:    proto.String(meta.GetDirectPath()),
			MediaKey:      meta.GetMediaKey(),
			FileEncSHA256: meta.GetFileEncSHA256(),
			FileSHA256:    meta.GetFileSHA256(),
			FileLength:    proto.Uint64(meta.GetFileLength()),
			Mimetype:      proto.String(mimeType),
			Width:         proto.Uint32(meta.GetWidth()),
			Height:        proto.Uint32(meta.GetHeight()),
			IsAnimated:    proto.Bool(meta.GetIsLottie()),
		}
		payload, err := proto.Marshal(stickerMsg)
		if err != nil {
			continue
		}
		if err := c.store.TouchRecentSticker(ctx, appstore.Sticker{
			CacheKey:       hex.EncodeToString(meta.GetFileSHA256()),
			EncCacheKey:    hex.EncodeToString(meta.GetFileEncSHA256()),
			MimeType:       mimeType,
			IsAnimated:     isWhatsAppAnimatedSticker(mimeType) || meta.GetIsLottie(),
			Width:          int32(meta.GetWidth()),
			Height:         int32(meta.GetHeight()),
			StickerPayload: payload,
			RecentWeight:   float64(meta.GetWeight()),
			LastUsed:       normalizeUnixTimestamp(meta.GetLastStickerSentTS()),
		}); err != nil {
			c.log.Warnf("Failed to record recent sticker from history sync: %v", err)
			continue
		}
		imported++
	}
	if imported > 0 {
		c.log.Infof("Imported %d recent stickers from history sync", imported)
		c.publishStickerLibraryChangedDebounced(app.StickerSourceRecent)
	}
}

// handleStickerAppState processes live favoriteSticker / removeRecentSticker
// mutations. whatsmeow surfaces these only as generic AppState events.
func (c *Client) handleStickerAppState(ctx context.Context, evt *events.AppState) {
	if evt == nil || len(evt.Index) == 0 || evt.SyncActionValue == nil {
		return
	}
	switch evt.Index[0] {
	case appstate.IndexFavoriteSticker:
		action := evt.GetStickerAction()
		if action == nil {
			return
		}
		if err := c.applyFavoriteStickerAction(ctx, action, normalizeUnixTimestamp(evt.GetTimestamp())); err != nil {
			c.log.Warnf("Failed to apply favorite sticker mutation: %v", err)
			return
		}
		c.publishStickerLibraryChangedDebounced(app.StickerSourceFavorite)
	case appstate.IndexRemoveRecentSticker:
		if len(evt.Index) < 2 {
			return
		}
		if err := c.store.ClearStickerRecency(ctx, strings.ToLower(evt.Index[1])); err != nil {
			c.log.Warnf("Failed to clear recent sticker: %v", err)
			return
		}
		c.publishStickerLibraryChangedDebounced(app.StickerSourceRecent)
	}
}

func (c *Client) applyFavoriteStickerAction(ctx context.Context, action *waSyncAction.StickerAction, ts int64) error {
	encKey := hex.EncodeToString(action.GetFileEncSHA256())
	if encKey == "" {
		return nil
	}
	sticker := favoriteStickerFromAction(action, encKey)
	sticker.FavoriteTS = ts

	if matched, err := c.store.SetStickerFavoriteByEncKey(ctx, encKey, action.GetIsFavorite(), time.Unix(ts, 0)); err != nil {
		return err
	} else if matched || !action.GetIsFavorite() {
		return nil
	}
	return c.store.SetStickerFavorite(ctx, sticker, true, time.Unix(ts, 0))
}

// reconcileFavoriteStickersFromEvents applies the complete favorites set from
// a full regular_low app state fetch (the same fetch the pin backfill already
// performs on connect), capturing favorites added or removed while the daemon
// was offline.
func (c *Client) reconcileFavoriteStickersFromEvents(ctx context.Context, eventsToDispatch []any) {
	var favorites []appstore.Sticker
	sawFavorites := false
	for _, raw := range eventsToDispatch {
		evt, ok := raw.(*events.AppState)
		if !ok || len(evt.Index) == 0 || evt.Index[0] != appstate.IndexFavoriteSticker {
			continue
		}
		sawFavorites = true
		action := evt.GetStickerAction()
		if action == nil || !action.GetIsFavorite() {
			continue
		}
		encKey := hex.EncodeToString(action.GetFileEncSHA256())
		if encKey == "" {
			continue
		}
		sticker := favoriteStickerFromAction(action, encKey)
		sticker.FavoriteTS = normalizeUnixTimestamp(evt.GetTimestamp())
		favorites = append(favorites, sticker)
	}
	if !sawFavorites && len(favorites) == 0 {
		// No favorite mutations in this snapshot at all: nothing to reconcile.
		// (An empty favorites list with mutations present means "all removed",
		// which ReconcileStickerFavorites handles.)
		return
	}
	if err := c.store.ReconcileStickerFavorites(ctx, favorites); err != nil {
		c.log.Warnf("Failed to reconcile favorite stickers: %v", err)
		return
	}
	c.publishStickerLibraryChangedDebounced(app.StickerSourceFavorite)
}

func favoriteStickerFromAction(action *waSyncAction.StickerAction, encKey string) appstore.Sticker {
	mimeType := strings.TrimSpace(action.GetMimetype())
	if mimeType == "" {
		mimeType = "image/webp"
	}
	stickerMsg := &waE2E.StickerMessage{
		URL:           proto.String(action.GetURL()),
		DirectPath:    proto.String(action.GetDirectPath()),
		MediaKey:      action.GetMediaKey(),
		FileEncSHA256: action.GetFileEncSHA256(),
		FileLength:    proto.Uint64(action.GetFileLength()),
		Mimetype:      proto.String(mimeType),
		Width:         proto.Uint32(action.GetHeight()),
		Height:        proto.Uint32(action.GetWidth()),
		IsAnimated:    proto.Bool(action.GetIsLottie()),
	}
	payload, _ := proto.Marshal(stickerMsg)
	return appstore.Sticker{
		CacheKey:       "enc:" + encKey,
		EncCacheKey:    encKey,
		MimeType:       mimeType,
		IsAnimated:     isWhatsAppAnimatedSticker(mimeType) || action.GetIsLottie(),
		Width:          int32(action.GetWidth()),
		Height:         int32(action.GetHeight()),
		StickerPayload: payload,
	}
}

// SetStickerFavorite favorites or unfavorites a sticker, pushing the change
// to WhatsApp as a favoriteSticker app-state mutation (the exact inverse of
// applyFavoriteStickerAction) so it syncs to the user's other devices. The
// sticker is identified by its cache key, or resolved from a sticker message
// when only a message id is at hand.
func (c *Client) SetStickerFavorite(ctx context.Context, cacheKey, messageID string, favorite bool) (appstore.Sticker, error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorNotLoggedIn, "WhatsApp client is not logged in")
	}

	cacheKey = strings.TrimSpace(cacheKey)
	messageID = strings.TrimSpace(messageID)
	var message appstore.Message
	haveMessage := false
	// Lazily load the source message; idempotent so it can be called both to
	// resolve a missing cache key and, later, to build a library row for a
	// received sticker that isn't stored yet.
	loadMessage := func() error {
		if haveMessage || messageID == "" {
			return nil
		}
		msg, err := c.store.GetMessage(ctx, messageID)
		if err != nil {
			return err
		}
		if msg.MediaKind != appstore.MediaKindSticker {
			return app.NewCommandError(app.CommandErrorInvalidArgument, "message is not a sticker")
		}
		message = msg
		haveMessage = true
		return nil
	}
	if cacheKey == "" {
		if err := loadMessage(); err != nil {
			return appstore.Sticker{}, err
		}
		if haveMessage {
			cacheKey = message.MediaCacheKey
			if cacheKey == "" {
				cacheKey, _ = appstore.StickerCacheKeyFromPayload(message.MediaPayload)
			}
		}
	}
	if cacheKey == "" {
		return appstore.Sticker{}, app.NewCommandError(app.CommandErrorInvalidArgument, "sticker could not be identified")
	}

	sticker, ok, err := c.store.GetSticker(ctx, cacheKey)
	if err != nil {
		return appstore.Sticker{}, err
	}
	if !ok {
		// Not in the library yet (a sticker someone sent us): build the row
		// from the message payload — the same shape incoming app-state
		// favorites are stored in — plus the already-downloaded file. The
		// caller usually supplies the cache key directly, so the message may
		// not have been loaded yet.
		if err := loadMessage(); err != nil {
			return appstore.Sticker{}, err
		}
		if !haveMessage || len(message.MediaPayload) == 0 {
			return appstore.Sticker{}, app.NewCommandError(app.CommandErrorNotFound, "sticker is not available")
		}
		var stickerMsg waE2E.StickerMessage
		if err := proto.Unmarshal(message.MediaPayload, &stickerMsg); err != nil {
			return appstore.Sticker{}, app.NewCommandError(app.CommandErrorInvalidArgument, "sticker payload is unreadable")
		}
		mimeType := strings.TrimSpace(stickerMsg.GetMimetype())
		if mimeType == "" {
			mimeType = "image/webp"
		}
		sticker = appstore.Sticker{
			CacheKey:       cacheKey,
			EncCacheKey:    hex.EncodeToString(stickerMsg.GetFileEncSHA256()),
			MimeType:       mimeType,
			IsAnimated:     isWhatsAppAnimatedSticker(mimeType) || stickerMsg.GetIsLottie() || message.MediaAnimated,
			Width:          int32(stickerMsg.GetWidth()),
			Height:         int32(stickerMsg.GetHeight()),
			LocalPath:      message.MediaLocalPath,
			StickerPayload: message.MediaPayload,
		}
	}

	var stickerMsg waE2E.StickerMessage
	if len(sticker.StickerPayload) > 0 {
		if err := proto.Unmarshal(sticker.StickerPayload, &stickerMsg); err != nil {
			stickerMsg = waE2E.StickerMessage{}
		}
	}

	action := &waSyncAction.StickerAction{
		URL:           proto.String(stickerMsg.GetURL()),
		FileEncSHA256: stickerMsg.GetFileEncSHA256(),
		MediaKey:      stickerMsg.GetMediaKey(),
		Mimetype:      proto.String(sticker.MimeType),
		Width:         proto.Uint32(uint32(sticker.Width)),
		Height:        proto.Uint32(uint32(sticker.Height)),
		DirectPath:    proto.String(stickerMsg.GetDirectPath()),
		FileLength:    proto.Uint64(stickerMsg.GetFileLength()),
		IsFavorite:    proto.Bool(favorite),
		IsLottie:      proto.Bool(stickerMsg.GetIsLottie()),
	}

	// Key the mutation by the lowercase content hash, mirroring how
	// removeRecentSticker arrives keyed; provisional enc-only stickers fall
	// back to the encrypted hash so the entry still dedupes consistently.
	indexKey := strings.ToLower(strings.TrimPrefix(cacheKey, "enc:"))
	patch := appstate.PatchInfo{
		Type: appstate.WAPatchRegularLow,
		Mutations: []appstate.MutationInfo{{
			Index:   []string{appstate.IndexFavoriteSticker, indexKey},
			Version: 2,
			Value:   &waSyncAction.SyncActionValue{StickerAction: action},
		}},
	}
	if err := c.sendRegularLowAppState(ctx, client, patch); err != nil {
		return appstore.Sticker{}, err
	}

	now := time.Now()
	if err := c.store.SetStickerFavorite(ctx, sticker, favorite, now); err != nil {
		return appstore.Sticker{}, err
	}
	c.publishStickerLibraryChangedDebounced(app.StickerSourceFavorite)

	if updated, ok, err := c.store.GetSticker(ctx, sticker.CacheKey); err == nil && ok {
		return updated, nil
	}
	sticker.IsFavorite = favorite
	sticker.FavoriteTS = now.Unix()
	return sticker, nil
}

// publishStickerLibraryChangedDebounced coalesces library-change events; a
// full app state resync can deliver hundreds of favorite mutations in a burst.
func (c *Client) publishStickerLibraryChangedDebounced(source app.StickerSource) {
	c.stickerMu.Lock()
	defer c.stickerMu.Unlock()
	if c.stickerLibraryTimers == nil {
		c.stickerLibraryTimers = make(map[app.StickerSource]*time.Timer)
	}
	if timer := c.stickerLibraryTimers[source]; timer != nil {
		timer.Reset(stickerLibraryDebounce)
		return
	}
	c.stickerLibraryTimers[source] = time.AfterFunc(stickerLibraryDebounce, func() {
		c.stickerMu.Lock()
		delete(c.stickerLibraryTimers, source)
		c.stickerMu.Unlock()
		c.daemon.PublishStickerLibraryChanged(source)
	})
}

func (c *Client) stickerHTTP() *http.Client {
	c.stickerMu.Lock()
	defer c.stickerMu.Unlock()
	if c.stickerHTTPClient == nil {
		c.stickerHTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	return c.stickerHTTPClient
}

func toDaemonSticker(sticker appstore.Sticker) app.Sticker {
	return app.Sticker{
		CacheKey:          sticker.CacheKey,
		LocalPath:         sticker.LocalPath,
		MimeType:          sticker.MimeType,
		IsAnimated:        sticker.IsAnimated,
		Width:             sticker.Width,
		Height:            sticker.Height,
		Emojis:            strings.Fields(sticker.Emojis),
		AccessibilityText: sticker.AccessibilityText,
		PackID:            sticker.PackID,
		IsFavorite:        sticker.IsFavorite,
		LastUsedUnix:      sticker.LastUsed,
		Weight:            float32(sticker.RecentWeight),
	}
}

// normalizeUnixTimestamp accepts seconds or milliseconds and returns seconds.
// WhatsApp mixes both across sync payloads.
func normalizeUnixTimestamp(value int64) int64 {
	if value > 1_000_000_000_000 {
		return value / 1000
	}
	if value < 0 {
		return 0
	}
	return value
}

func readLimitedBody(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response body exceeds %d bytes", limit)
	}
	return data, nil
}
