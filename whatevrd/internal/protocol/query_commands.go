package protocol

import (
	"context"
	"strings"
	"unicode/utf8"
)

const (
	defaultQueryLimit = 30
	maxQueryLimit     = 100
	maxQueryRunes     = 256
)

type searchChatsParams struct {
	Query string `json:"query"`
	Limit *int   `json:"limit"`
}

func (h commandHandlers) searchChats(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p searchChatsParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(p.Query)
	if utf8.RuneCountInString(query) > maxQueryRunes {
		return nil, errorf(CodeInvalidParams, "query must be <= %d characters", maxQueryRunes)
	}
	limit, err := normalizeQueryLimit(p.Limit)
	if err != nil {
		return nil, err
	}
	chats, qerr := h.actions.SearchChats(context.Background(), query, limit)
	if perr := mapCommandError(qerr); perr != nil {
		return nil, perr
	}
	rows := make([]chatItem, 0, len(chats))
	for _, chat := range chats {
		rows = append(rows, chatItemFromStore(chat))
	}
	return map[string]any{"chats": rows}, nil
}

type searchMessagesParams struct {
	Query           string `json:"query"`
	ChatID          string `json:"chat_id"`
	Limit           *int   `json:"limit"`
	BeforeMessageID string `json:"before_message_id"`
}

type searchMessageItem struct {
	messageItem
	ChatName string `json:"chat_name,omitempty"`
}

func (h commandHandlers) searchMessages(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p searchMessagesParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(p.Query)
	if utf8.RuneCountInString(query) > maxQueryRunes {
		return nil, errorf(CodeInvalidParams, "query must be <= %d characters", maxQueryRunes)
	}
	limit, err := normalizeQueryLimit(p.Limit)
	if err != nil {
		return nil, err
	}
	results, qerr := h.actions.SearchMessages(context.Background(), query, strings.TrimSpace(p.ChatID), limit+1, strings.TrimSpace(p.BeforeMessageID))
	if perr := mapCommandError(qerr); perr != nil {
		return nil, perr
	}
	hasMore := len(results) > limit
	if hasMore {
		results = results[:limit]
	}
	rows := make([]searchMessageItem, 0, len(results))
	for _, result := range results {
		rows = append(rows, searchMessageItem{messageItem: messageItemFromStore(result.Message), ChatName: result.ChatName})
	}
	return map[string]any{"messages": rows, "has_more": hasMore}, nil
}

func normalizeQueryLimit(limit *int) (int, *Error) {
	if limit == nil || *limit == 0 {
		return defaultQueryLimit, nil
	}
	if *limit < 0 {
		return 0, errorf(CodeInvalidParams, "limit must be non-negative")
	}
	if *limit > maxQueryLimit {
		return 0, errorf(CodeInvalidParams, "limit must be <= %d", maxQueryLimit)
	}
	return *limit, nil
}

type contactsCheckPhoneParams struct {
	Phone string `json:"phone"`
}

func (h commandHandlers) contactsCheckPhone(ctx context.Context, _ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p contactsCheckPhoneParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	phone := strings.TrimSpace(p.Phone)
	if phone == "" {
		return nil, errorf(CodeInvalidParams, "phone is required")
	}
	check, err := h.actions.CheckPhoneOnWhatsApp(ctx, phone)
	if perr := mapCommandError(err); perr != nil {
		return nil, perr
	}
	return map[string]any{
		"registered":   check.Registered,
		"jid":          check.JID,
		"display_name": check.DisplayName,
		"is_business":  check.IsBusiness,
		"phone":        check.Phone,
	}, nil
}
