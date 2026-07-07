package protocol

import (
	"context"
	"strings"
)

type stickerFavoriteParams struct {
	CacheKey  string `json:"cache_key"`
	MessageID string `json:"message_id"`
	Favorite  *bool  `json:"favorite"`
}

func (h commandHandlers) stickerFavorite(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p stickerFavoriteParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	cacheKey := strings.TrimSpace(p.CacheKey)
	messageID := strings.TrimSpace(p.MessageID)
	if cacheKey == "" && messageID == "" {
		return nil, errorf(CodeInvalidParams, "cache_key or message_id is required")
	}
	if p.Favorite == nil {
		return nil, errorf(CodeInvalidParams, "favorite is required")
	}
	_, err := h.actions.SetStickerFavorite(context.Background(), cacheKey, messageID, *p.Favorite)
	return nil, mapCommandError(err)
}

type stickerDownloadParams struct {
	CacheKey string `json:"cache_key"`
}

func (h commandHandlers) stickerDownload(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p stickerDownloadParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	cacheKey := strings.TrimSpace(p.CacheKey)
	if cacheKey == "" {
		return nil, errorf(CodeInvalidParams, "cache_key is required")
	}
	_, err := h.actions.DownloadSticker(context.Background(), cacheKey)
	return nil, mapCommandError(err)
}

type stickerPackInstallParams struct {
	PackID    string `json:"pack_id"`
	Installed *bool  `json:"installed"`
}

func (h commandHandlers) stickerPackInstall(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p stickerPackInstallParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	packID := strings.TrimSpace(p.PackID)
	if packID == "" {
		return nil, errorf(CodeInvalidParams, "pack_id is required")
	}
	if p.Installed == nil {
		return nil, errorf(CodeInvalidParams, "installed is required")
	}
	_, err := h.actions.SetStickerPackInstalled(context.Background(), packID, *p.Installed)
	return nil, mapCommandError(err)
}
