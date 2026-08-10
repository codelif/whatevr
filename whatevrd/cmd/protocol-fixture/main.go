package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"whatevrd/internal/app"
	"whatevrd/internal/protocol"
	appstore "whatevrd/internal/store"
)

type conformanceView struct{}

func (conformanceView) Open(_ json.RawMessage, _ func()) (protocol.ViewSession, map[string]any, *protocol.Error) {
	return conformanceSession{}, map[string]any{"fixture": "conformance"}, nil
}

type conformanceSession struct{}

type fixtureCommands struct{}

func (fixtureCommands) FrontendSessionStarted(string)                    {}
func (fixtureCommands) FrontendSessionEnded(string)                      {}
func (fixtureCommands) FrontendSessionStateChanged(string, bool, string) {}
func (fixtureCommands) Reconnect(context.Context) error                  { return nil }
func (fixtureCommands) Logout(context.Context) error                     { return nil }
func (fixtureCommands) MarkChatReadUpTo(context.Context, string, string) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (fixtureCommands) SetChatPinned(context.Context, string, bool) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (fixtureCommands) SetChatArchived(context.Context, string, bool) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (fixtureCommands) SetChatMuted(context.Context, string, bool, time.Duration) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (fixtureCommands) SetChatPresence(context.Context, string, bool) error { return nil }
func (fixtureCommands) RequestOlderMessages(context.Context, string) (bool, error) {
	return true, nil
}
func (fixtureCommands) EnsureDirectChat(_ context.Context, jid string) (appstore.Chat, error) {
	return appstore.Chat{ID: jid}, nil
}
func (fixtureCommands) SendText(_ context.Context, chatID, text, _ string, _ []string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":fixture-text", ChatID: chatID, Text: text}}, nil
}
func (fixtureCommands) SendMediaWithMentions(_ context.Context, chatID, path, caption, _ string, _ []string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":fixture-media", ChatID: chatID, Text: caption, MediaLocalPath: path}}, nil
}
func (fixtureCommands) SendSticker(_ context.Context, chatID, cacheKey, _ string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":fixture-sticker", ChatID: chatID, MediaCacheKey: cacheKey}}, nil
}
func (fixtureCommands) SendReaction(context.Context, string, string) (appstore.Message, error) {
	return appstore.Message{}, nil
}
func (fixtureCommands) EditMessage(context.Context, string, string) (appstore.Message, error) {
	return appstore.Message{}, nil
}
func (fixtureCommands) RevokeMessage(context.Context, string) (appstore.Message, error) {
	return appstore.Message{}, nil
}
func (fixtureCommands) DeleteMessageForMe(context.Context, string) error { return nil }
func (fixtureCommands) SetMessageStarred(context.Context, string, bool) (appstore.Message, error) {
	return appstore.Message{}, nil
}
func (fixtureCommands) PinMessage(context.Context, string, bool, uint32) (appstore.Message, error) {
	return appstore.Message{}, nil
}
func (fixtureCommands) ForwardMessage(_ context.Context, _ string, chatIDs []string) ([]appstore.SavedTextMessage, error) {
	out := make([]appstore.SavedTextMessage, 0, len(chatIDs))
	for _, chatID := range chatIDs {
		out = append(out, appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":fixture-forward", ChatID: chatID}})
	}
	return out, nil
}
func (fixtureCommands) DownloadMessageMedia(context.Context, string) (appstore.Message, error) {
	return appstore.Message{}, nil
}
func (fixtureCommands) FetchProfilePicture(_ context.Context, jid string) (string, error) {
	return "/cache/avatars/" + jid + ".jpg", nil
}
func (fixtureCommands) SetPrivacySetting(context.Context, string, string, bool) (app.PrivacySettings, error) {
	return app.PrivacySettings{}, nil
}
func (fixtureCommands) UpdateAppPreferences(_ context.Context, apply func(*app.AppPreferences)) (app.AppPreferences, error) {
	prefs := app.DefaultAppPreferences()
	apply(&prefs)
	return prefs, nil
}
func (fixtureCommands) SetProfileStatus(context.Context, string) error { return nil }
func (fixtureCommands) UpdateBlocklist(context.Context, string, bool) ([]app.BlockedContact, error) {
	return nil, nil
}
func (fixtureCommands) SetStickerFavorite(context.Context, string, string, bool) (appstore.Sticker, error) {
	return appstore.Sticker{}, nil
}
func (fixtureCommands) DownloadSticker(_ context.Context, cacheKey string) (appstore.Sticker, error) {
	return appstore.Sticker{CacheKey: cacheKey}, nil
}
func (fixtureCommands) SetStickerPackInstalled(_ context.Context, packID string, installed bool) (appstore.StickerPack, error) {
	return appstore.StickerPack{ID: packID, Installed: installed}, nil
}
func (fixtureCommands) RefreshStickerPacks(context.Context) error { return nil }
func (fixtureCommands) SearchChats(context.Context, string, int) ([]appstore.Chat, error) {
	return nil, nil
}
func (fixtureCommands) SearchMessages(context.Context, string, string, int, string) ([]appstore.MessageSearchResult, error) {
	return nil, nil
}
func (fixtureCommands) SearchStickers(context.Context, string, int) ([]appstore.Sticker, error) {
	return nil, nil
}
func (fixtureCommands) CheckPhoneOnWhatsApp(_ context.Context, phone string) (app.PhoneCheck, error) {
	return app.PhoneCheck{Phone: phone}, nil
}

func (conformanceSession) Items(max int) []protocol.Item {
	items := []protocol.Item{
		{ID: "alpha", Sort: "0001", Data: map[string]any{"id": "alpha", "title": "Alpha"}},
		{ID: "bravo", Sort: "0002", Data: map[string]any{"id": "bravo", "title": "Bravo"}},
		{ID: "charlie", Sort: "0003", Data: map[string]any{"id": "charlie", "title": "Charlie"}},
	}
	if max > 0 && len(items) > max {
		items = items[:max]
	}
	return append([]protocol.Item(nil), items...)
}

func (conformanceSession) Close() {}

func main() {
	var socketPath string
	var readyFile string
	flag.StringVar(&socketPath, "socket", "", "unix socket path to serve")
	flag.StringVar(&readyFile, "ready-file", "", "write this file after the fixture is listening")
	flag.Parse()

	if socketPath == "" {
		log.Fatal("--socket is required")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	base := filepath.Dir(socketPath)
	daemon := app.NewDaemon(app.Paths{
		DataDir:  filepath.Join(base, "data"),
		CacheDir: filepath.Join(base, "cache"),
	})
	daemon.SetState(app.StateOnline)

	server, err := protocol.New(socketPath, daemon)
	if err != nil {
		log.Fatalf("start protocol fixture: %v", err)
	}
	protocol.RegisterDaemonViews(server, daemon, nil, nil)
	protocol.RegisterDaemonCommands(server, fixtureCommands{})
	server.RegisterView("conformance", conformanceView{})
	// Accept only after registration; the ready file (which the conformance
	// harness waits on before dialing) is written below, after Serve.
	server.Serve(ctx)

	if readyFile != "" {
		if err := os.WriteFile(readyFile, []byte(socketPath+"\n"), 0o644); err != nil {
			log.Fatalf("write ready file: %v", err)
		}
	}
	log.Printf("protocol conformance fixture listening on %s", socketPath)

	select {
	case <-ctx.Done():
		for err := range server.Err() {
			if err != nil {
				log.Fatalf("protocol fixture shutdown: %v", err)
			}
		}
	case err, ok := <-server.Err():
		if ok && err != nil {
			log.Fatalf("protocol fixture failed: %v", err)
		}
	}
}
