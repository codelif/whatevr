package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"whatevrd/internal/app"
	"whatevrd/internal/store"
)

// StickerStore supplies the sticker-library and pack views their rows. *store.DB
// implements it; actions only trigger upstream refresh/fetch work.
type StickerStore interface {
	ListRecentStickers(ctx context.Context, limit int) ([]store.Sticker, error)
	ListFavoriteStickers(ctx context.Context, limit int) ([]store.Sticker, error)
	ListAllStickers(ctx context.Context, limit int) ([]store.Sticker, error)
	ListStickerPacks(ctx context.Context) ([]store.StickerPack, error)
	GetStickerPack(ctx context.Context, id string) (store.StickerPack, bool, error)
	ListPackStickers(ctx context.Context, packID string) ([]store.Sticker, error)
}

// StickerActions is the upstream seam behind sticker views. ListStickerPacks
// refreshes the first-party pack index; GetStickerPack fetches a pack's contents
// when the local store has only the shell row. *wa.Client implements both.
type StickerActions interface {
	ListStickerPacks(ctx context.Context, forceRefresh bool) ([]store.StickerPack, error)
	GetStickerPack(ctx context.Context, packID string) (store.StickerPack, []store.Sticker, error)
}

// --- stickers ------------------------------------------------------------

type stickersView struct {
	daemon *app.Daemon
	store  StickerStore
}

type stickersParams struct {
	Source string `json:"source"`
}

const defaultStickerViewLimit = 200

type stickerItem struct {
	ID                string   `json:"id"`
	CacheKey          string   `json:"cache_key"`
	LocalPath         string   `json:"local_path,omitempty"`
	MimeType          string   `json:"mime_type,omitempty"`
	IsAnimated        bool     `json:"is_animated"`
	Width             int32    `json:"width"`
	Height            int32    `json:"height"`
	Emojis            []string `json:"emojis,omitempty"`
	AccessibilityText string   `json:"accessibility_text,omitempty"`
	PackID            string   `json:"pack_id,omitempty"`
	IsFavorite        bool     `json:"is_favorite"`
	LastUsedUnix      int64    `json:"last_used_unix"`
	Weight            float32  `json:"weight"`
}

func (v stickersView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.store == nil {
		return nil, nil, errorf(CodeInternal, "stickers view unavailable")
	}
	var p stickersParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed stickers params")
		}
	}
	source, err := stickerSourceFromWire(p.Source)
	if err != nil {
		return nil, nil, err
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &stickersSession{store: v.store, source: source, eventsCancel: cancel, done: make(chan struct{})}
	go s.run(events, invalidate)
	return s, nil, nil
}

type stickersSession struct {
	store        StickerStore
	source       app.StickerSource
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *stickersSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.eventAffects(evt) {
				invalidate()
			}
		}
	}
}

func (s *stickersSession) eventAffects(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync:
		return true
	case app.DaemonEventStickerLibraryChanged:
		return stickerLibraryEventMatches(s.source, evt.StickerSource)
	case app.DaemonEventStickerDownloadChanged:
		return true
	default:
		return false
	}
}

func (s *stickersSession) Items(max int) []Item {
	limit := max
	if limit <= 0 {
		limit = defaultStickerViewLimit
	}
	var (
		stickers []store.Sticker
		err      error
	)
	switch s.source {
	case app.StickerSourceRecent:
		stickers, err = s.store.ListRecentStickers(context.Background(), limit)
	case app.StickerSourceFavorite:
		stickers, err = s.store.ListFavoriteStickers(context.Background(), limit)
	case app.StickerSourceAll:
		stickers, err = s.store.ListAllStickers(context.Background(), limit)
	}
	if err != nil {
		log.Printf("protocol: list stickers for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(stickers))
	for i, sticker := range stickers {
		items = append(items, Item{
			ID:   sticker.CacheKey,
			Sort: indexedSort(i, sticker.CacheKey),
			Data: stickerItemFromStore(sticker),
		})
	}
	return items
}

func (s *stickersSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

func stickerSourceFromWire(raw string) (app.StickerSource, *Error) {
	switch strings.TrimSpace(raw) {
	case "recent":
		return app.StickerSourceRecent, nil
	case "favorite":
		return app.StickerSourceFavorite, nil
	case "all":
		return app.StickerSourceAll, nil
	default:
		return app.StickerSourceUnspecified, errorf(CodeInvalidParams, "stickers params must carry source recent, favorite, or all")
	}
}

func stickerLibraryEventMatches(viewSource, eventSource app.StickerSource) bool {
	if eventSource == app.StickerSourceUnspecified || viewSource == app.StickerSourceAll {
		return true
	}
	// A favorite toggle flips the is_favorite flag on the sticker's row in *every*
	// source view (recent and all render it too, not just the favorite list), so a
	// favorite-source event is cross-cutting. Items re-reads and the engine diffs
	// away any view where nothing visible actually changed.
	if eventSource == app.StickerSourceFavorite {
		return true
	}
	return viewSource == eventSource
}

// --- sticker_packs -------------------------------------------------------

type stickerPacksView struct {
	daemon  *app.Daemon
	store   StickerStore
	actions StickerActions
}

type stickerPackItem struct {
	ID              string `json:"id"`
	Name            string `json:"name,omitempty"`
	Publisher       string `json:"publisher,omitempty"`
	Description     string `json:"description,omitempty"`
	Animated        bool   `json:"animated"`
	Lottie          bool   `json:"lottie"`
	TrayLocalPath   string `json:"tray_local_path,omitempty"`
	StickerCount    int32  `json:"sticker_count"`
	Installed       bool   `json:"installed"`
	ContentsFetched bool   `json:"contents_fetched"`
}

func (v stickerPacksView) Open(_ json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.store == nil {
		return nil, nil, errorf(CodeInternal, "sticker_packs view unavailable")
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &stickerPacksSession{store: v.store, actions: v.actions, eventsCancel: cancel, done: make(chan struct{})}
	go s.refreshIndex(invalidate)
	go s.run(events, invalidate)
	return s, nil, nil
}

type stickerPacksSession struct {
	store        StickerStore
	actions      StickerActions
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *stickerPacksSession) refreshIndex(invalidate func()) {
	if s.actions == nil {
		return
	}
	if _, err := s.actions.ListStickerPacks(context.Background(), false); err != nil {
		log.Printf("protocol: refresh sticker packs for view: %v", err)
	}
	invalidate()
}

func (s *stickerPacksSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if evt.Kind == app.DaemonEventStickerLibraryChanged || evt.Kind == app.DaemonEventResync {
				invalidate()
			}
		}
	}
}

func (s *stickerPacksSession) Items(int) []Item {
	packs, err := s.store.ListStickerPacks(context.Background())
	if err != nil {
		log.Printf("protocol: list sticker packs for view: %v", err)
		return nil
	}
	items := make([]Item, 0, len(packs))
	for i, pack := range packs {
		items = append(items, Item{
			ID:   pack.ID,
			Sort: indexedSort(i, pack.ID),
			Data: stickerPackItemFromStore(pack),
		})
	}
	return items
}

func (s *stickerPacksSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

// --- sticker_pack --------------------------------------------------------

type stickerPackView struct {
	daemon  *app.Daemon
	store   StickerStore
	actions StickerActions
}

type stickerPackParams struct {
	PackID string `json:"pack_id"`
}

func (v stickerPackView) Open(params json.RawMessage, invalidate func()) (ViewSession, map[string]any, *Error) {
	if v.store == nil {
		return nil, nil, errorf(CodeInternal, "sticker_pack view unavailable")
	}
	var p stickerPackParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, nil, errorf(CodeInvalidParams, "malformed sticker_pack params")
		}
	}
	p.PackID = strings.TrimSpace(p.PackID)
	if p.PackID == "" {
		return nil, nil, errorf(CodeInvalidParams, "sticker_pack params must carry a pack_id")
	}
	pack, ok, err := v.store.GetStickerPack(context.Background(), p.PackID)
	if err != nil {
		log.Printf("protocol: get sticker pack for view: %v", err)
		return nil, nil, errorf(CodeInternal, "sticker_pack view unavailable")
	}
	if !ok {
		return nil, nil, errorf(CodeNotFound, "no sticker pack %s", p.PackID)
	}
	events, cancel := v.daemon.SubscribeDaemonEvents()
	s := &stickerPackSession{store: v.store, actions: v.actions, packID: p.PackID, eventsCancel: cancel, done: make(chan struct{})}
	if pack.ContentsFetchedAt == 0 && v.actions != nil {
		go s.fetchContents(invalidate)
	}
	go s.run(events, invalidate)
	return s, nil, nil
}

type stickerPackSession struct {
	store        StickerStore
	actions      StickerActions
	packID       string
	eventsCancel func()
	done         chan struct{}
	closeOnce    sync.Once
}

func (s *stickerPackSession) fetchContents(invalidate func()) {
	if _, _, err := s.actions.GetStickerPack(context.Background(), s.packID); err != nil {
		log.Printf("protocol: fetch sticker pack %s for view: %v", s.packID, err)
	}
	invalidate()
}

func (s *stickerPackSession) run(events <-chan app.DaemonEvent, invalidate func()) {
	for {
		select {
		case <-s.done:
			return
		case evt := <-events:
			if s.eventAffects(evt) {
				invalidate()
			}
		}
	}
}

func (s *stickerPackSession) eventAffects(evt app.DaemonEvent) bool {
	switch evt.Kind {
	case app.DaemonEventResync:
		return true
	case app.DaemonEventStickerLibraryChanged:
		return evt.StickerSource == app.StickerSourceUnspecified || evt.StickerSource == app.StickerSourceAll
	case app.DaemonEventStickerDownloadChanged:
		return evt.StickerDownload.Sticker.PackID == s.packID
	default:
		return false
	}
}

func (s *stickerPackSession) Items(int) []Item {
	stickers, err := s.store.ListPackStickers(context.Background(), s.packID)
	if err != nil {
		log.Printf("protocol: list sticker pack %s for view: %v", s.packID, err)
		return nil
	}
	items := make([]Item, 0, len(stickers))
	for _, sticker := range stickers {
		items = append(items, Item{
			ID:   sticker.CacheKey,
			Sort: fmt.Sprintf("%020d-%s", nonNegativeInt32(sticker.PackOrder), sticker.CacheKey),
			Data: stickerItemFromStore(sticker),
		})
	}
	return items
}

func (s *stickerPackSession) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		s.eventsCancel()
	})
}

func stickerItemFromStore(sticker store.Sticker) stickerItem {
	return stickerItem{
		ID:                sticker.CacheKey,
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

func stickerPackItemFromStore(pack store.StickerPack) stickerPackItem {
	return stickerPackItem{
		ID:              pack.ID,
		Name:            pack.Name,
		Publisher:       pack.Publisher,
		Description:     pack.Description,
		Animated:        pack.Animated,
		Lottie:          pack.Lottie,
		TrayLocalPath:   pack.TrayLocalPath,
		StickerCount:    pack.StickerCount,
		Installed:       pack.Installed,
		ContentsFetched: pack.ContentsFetchedAt > 0,
	}
}

func indexedSort(index int, id string) string {
	return fmt.Sprintf("%020d-%s", index, id)
}

func nonNegativeInt32(v int32) int32 {
	if v < 0 {
		return 0
	}
	return v
}
