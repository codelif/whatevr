package wa

import (
	"context"
	"strings"

	appstore "whatevrd/internal/store"
)

// SearchChats serves the protocol's transient search.chats query from the
// daemon store. Results are ordered by the store (same ordering as the chat
// list), so frontends render the returned array as-is.
func (c *Client) SearchChats(ctx context.Context, query string, limit int) ([]appstore.Chat, error) {
	return c.store.SearchChats(ctx, strings.TrimSpace(query), limit)
}

// SearchMessages serves the protocol's transient search.messages query from the
// daemon store. The store owns ranking/order and cursor semantics.
func (c *Client) SearchMessages(ctx context.Context, query, chatID string, limit int, beforeMessageID string) ([]appstore.MessageSearchResult, error) {
	return c.store.SearchMessages(ctx, strings.TrimSpace(query), strings.TrimSpace(chatID), limit, strings.TrimSpace(beforeMessageID))
}

// SearchStickers serves the protocol's transient search.stickers query from the
// daemon store. The store owns result ordering.
func (c *Client) SearchStickers(ctx context.Context, query string, limit int) ([]appstore.Sticker, error) {
	return c.store.SearchStickers(ctx, strings.TrimSpace(query), limit)
}
