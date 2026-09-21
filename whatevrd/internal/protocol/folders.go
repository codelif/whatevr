package protocol

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"whatevrd/internal/store"
)

type FolderLister interface {
	ListChatFolders(context.Context) ([]store.ChatFolder, error)
}
type foldersView struct{ lister FolderLister }
type folderSession struct {
	folders []store.ChatFolder
	lister  FolderLister
}

func (v foldersView) Open(params json.RawMessage, _ func()) (ViewSession, map[string]any, *Error) {
	if len(params) > 0 && strings.TrimSpace(string(params)) != "{}" {
		return nil, nil, errorf(CodeInvalidParams, "folders params must be empty")
	}
	return &folderSession{lister: v.lister}, nil, nil
}
func (s *folderSession) Items(_ int) []Item {
	if s.lister != nil {
		s.folders, _ = s.lister.ListChatFolders(context.Background())
	}
	items := make([]Item, 0, len(s.folders))
	for _, f := range s.folders {
		id := strconv.FormatInt(f.ID, 10)
		items = append(items, Item{ID: id, Sort: f.Name, Data: map[string]any{"id": id, "folder_id": f.ID, "name": f.Name}})
	}
	return items
}
func (*folderSession) Close() {}

type folderActions interface {
	CreateChatFolder(context.Context, string) (store.ChatFolder, error)
	RenameChatFolder(context.Context, int64, string) error
	DeleteChatFolder(context.Context, int64) error
	SetChatFolder(context.Context, string, *int64) error
}
type folderCreateParams struct {
	Name string `json:"name"`
}

func (h commandHandlers) folderCreate(ctx context.Context, _ *conn, req request) (any, *Error) {
	a, ok := h.actions.(folderActions)
	if !ok {
		return nil, errorf(CodeInternal, "folder actions are not available")
	}
	var p folderCreateParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	f, err := a.CreateChatFolder(ctx, p.Name)
	if err != nil {
		return nil, mapCommandError(err)
	}
	return map[string]any{"id": f.ID, "name": f.Name}, nil
}

type folderRenameParams struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (h commandHandlers) folderRename(ctx context.Context, _ *conn, req request) (any, *Error) {
	a, ok := h.actions.(folderActions)
	if !ok {
		return nil, errorf(CodeInternal, "folder actions are not available")
	}
	var p folderRenameParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	return nil, mapCommandError(a.RenameChatFolder(ctx, p.ID, p.Name))
}

type folderDeleteParams struct {
	ID int64 `json:"id"`
}

func (h commandHandlers) folderDelete(ctx context.Context, _ *conn, req request) (any, *Error) {
	a, ok := h.actions.(folderActions)
	if !ok {
		return nil, errorf(CodeInternal, "folder actions are not available")
	}
	var p folderDeleteParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	return nil, mapCommandError(a.DeleteChatFolder(ctx, p.ID))
}

type folderSetChatParams struct {
	ChatID   string `json:"chat_id"`
	FolderID *int64 `json:"folder_id"`
}

func (h commandHandlers) folderSetChat(ctx context.Context, _ *conn, req request) (any, *Error) {
	a, ok := h.actions.(folderActions)
	if !ok {
		return nil, errorf(CodeInternal, "folder actions are not available")
	}
	var p folderSetChatParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ChatID) == "" {
		return nil, errorf(CodeInvalidParams, "chat_id is required")
	}
	return nil, mapCommandError(a.SetChatFolder(ctx, p.ChatID, p.FolderID))
}
