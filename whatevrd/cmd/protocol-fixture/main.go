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

type conformanceView struct{ faults *faultSet }

func (v conformanceView) Open(_ json.RawMessage, _ func()) (protocol.ViewSession, map[string]any, *protocol.Error) {
	return conformanceSession{faults: v.faults}, map[string]any{"fixture": "conformance"}, nil
}

type conformanceSession struct{ faults *faultSet }

type fixtureCommands struct{}

func (fixtureCommands) FrontendSessionStarted(string)                    {}
func (fixtureCommands) FrontendSessionEnded(string)                      {}
func (fixtureCommands) FrontendSessionStateChanged(string, bool, string) {}
func (fixtureCommands) Reconnect(context.Context) error                  { return nil }
func (fixtureCommands) Logout(context.Context) error                     { return nil }
func (fixtureCommands) MarkChatReadUpTo(context.Context, string, string) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (fixtureCommands) MarkChatRead(context.Context, string) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (fixtureCommands) MarkAllChatsRead(context.Context) (int, error) { return 0, nil }
func (fixtureCommands) ExportChat(_ context.Context, chatID, path string) (string, error) {
	return path, nil
}
func (fixtureCommands) SetChatPinned(context.Context, string, bool) (appstore.Chat, error) {
	return appstore.Chat{}, nil
}
func (fixtureCommands) SetChatFavorite(context.Context, string, bool) (appstore.Chat, error) {
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
func (fixtureCommands) ScheduleText(context.Context, string, string, time.Time) (int64, error) {
	return 1, nil
}
func (fixtureCommands) ListScheduledMessages(context.Context, string) ([]appstore.ScheduledMessage, error) {
	return nil, nil
}
func (fixtureCommands) CancelScheduledMessage(context.Context, int64) error { return nil }
func (fixtureCommands) SendMediaWithMentions(_ context.Context, chatID, path, caption, _ string, _ []string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":fixture-media", ChatID: chatID, Text: caption, MediaLocalPath: path}}, nil
}
func (fixtureCommands) SendMediaWithOptions(_ context.Context, chatID, path, caption, _ string, _ []string, _ app.MediaSendOptions) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":fixture-media", ChatID: chatID, Text: caption, MediaLocalPath: path}}, nil
}
func (fixtureCommands) SendMediaBatch(_ context.Context, chatID string, files []app.MediaBatchFile, _ string, _ app.MediaSendOptions) ([]appstore.SavedTextMessage, []app.MediaBatchError) {
	out := make([]appstore.SavedTextMessage, 0, len(files))
	for _, file := range files {
		out = append(out, appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":fixture-batch", ChatID: chatID, Text: file.Caption}})
	}
	return out, nil
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
func (fixtureCommands) CancelMessageMediaDownload(context.Context, string) error {
	return nil
}
func (fixtureCommands) StreamMessageMedia(_ context.Context, messageID string, _ func(app.MediaStreamUpdate)) (app.MediaStream, error) {
	return app.MediaStream{
		StreamID:  "fixture-" + messageID,
		URL:       "http://127.0.0.1:0/media/" + messageID + "?t=fixture",
		Mime:      "video/mp4",
		SizeBytes: 1 << 20,
	}, nil
}
func (fixtureCommands) MarkMessagePlayed(context.Context, string) error       { return nil }
func (fixtureCommands) RequestMessageFromPhone(context.Context, string) error { return nil }
func (fixtureCommands) VotePoll(context.Context, string, []int) error         { return nil }
func (fixtureCommands) JoinGroupInvite(context.Context, string) (string, error) {
	return "120363000000000000@g.us", nil
}
func (fixtureCommands) RespondToEvent(context.Context, string, string, int) error { return nil }
func (fixtureCommands) FetchProfilePicture(_ context.Context, jid string) (string, error) {
	return "/cache/avatars/" + jid + ".jpg", nil
}
func (fixtureCommands) SaveMediaToPath(_ context.Context, messageID, statusID, jid, dest string) (string, error) {
	if dest == "" {
		return "", nil
	}
	return dest, nil
}
func (fixtureCommands) MarkStatusViewed(_ context.Context, statusID string) (appstore.StatusUpdate, error) {
	return appstore.StatusUpdate{ID: statusID}, nil
}
func (fixtureCommands) PostStatus(_ context.Context, text, path, caption string, background uint32, font int32) (appstore.StatusUpdate, error) {
	return appstore.StatusUpdate{ID: "status:fixture", Text: text}, nil
}
func (fixtureCommands) DownloadStatusMedia(_ context.Context, statusID string) (appstore.StatusUpdate, error) {
	return appstore.StatusUpdate{ID: statusID}, nil
}
func (fixtureCommands) ReplyToStatus(_ context.Context, statusID, text string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: "reply:fixture", Text: text}}, nil
}
func (fixtureCommands) DeleteStatus(context.Context, string) error { return nil }
func (fixtureCommands) ListStatusViewers(context.Context, string) ([]appstore.StatusViewer, error) {
	return nil, nil
}
func (fixtureCommands) ListMessageEdits(_ context.Context, messageID string) ([]appstore.MessageEdit, error) {
	return []appstore.MessageEdit{{MessageID: messageID}}, nil
}
func (fixtureCommands) SetStatusKeepSender(context.Context, string, bool) error  { return nil }
func (fixtureCommands) ListKeptStatusSenders(context.Context) ([]string, error)  { return nil, nil }
func (fixtureCommands) SetStatusMutedSender(context.Context, string, bool) error { return nil }
func (fixtureCommands) ListMutedStatusSenders(context.Context) ([]string, error) { return nil, nil }
func (fixtureCommands) SendPoll(_ context.Context, chatID, question string, options []string, multi bool) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":poll", ChatID: chatID, Text: question}}, nil
}
func (fixtureCommands) SendContact(_ context.Context, chatID, name, phone string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":contact", ChatID: chatID, Text: name}}, nil
}
func (fixtureCommands) SendLocation(_ context.Context, chatID string, lat, long float64, name, address string) (appstore.SavedTextMessage, error) {
	return appstore.SavedTextMessage{Message: appstore.Message{ID: chatID + ":location", ChatID: chatID, Text: name}}, nil
}
func (fixtureCommands) CreateGroup(_ context.Context, name string, members []string, photo string) (appstore.Chat, error) {
	return appstore.Chat{ID: "group-fixture@g.us", Name: name}, nil
}
func (fixtureCommands) LeaveGroup(context.Context, string) error                  { return nil }
func (fixtureCommands) SetGroupName(context.Context, string, string) error        { return nil }
func (fixtureCommands) SetGroupDescription(context.Context, string, string) error { return nil }
func (fixtureCommands) SetGroupPhoto(context.Context, string, string) error       { return nil }
func (fixtureCommands) GetGroupInviteLink(_ context.Context, chatID string, reset bool) (string, error) {
	return "https://chat.whatsapp.com/fixture", nil
}
func (fixtureCommands) JoinGroupWithLink(_ context.Context, link string) (appstore.Chat, error) {
	return appstore.Chat{ID: "joined@g.us"}, nil
}
func (fixtureCommands) UpdateGroupMembers(context.Context, string, string, []string) error {
	return nil
}
func (fixtureCommands) SetGroupAnnounce(context.Context, string, bool) error { return nil }
func (fixtureCommands) SetGroupLocked(context.Context, string, bool) error   { return nil }
func (fixtureCommands) RejectCall(context.Context, string) error             { return nil }
func (fixtureCommands) ExportBackup(_ context.Context, dest, passphrase string, useKeyring bool) (string, int64, error) {
	if dest == "" {
		dest = "/tmp/whatevr-backup.tar.gz"
	}
	return dest, 42, nil
}
func (fixtureCommands) SetBackupPassphrase(context.Context, string) error { return nil }
func (fixtureCommands) RecentLogs(_ context.Context, limit int) ([]string, error) {
	return []string{"log line 1", "log line 2"}, nil
}
func (fixtureCommands) ListCommunitySubgroups(_ context.Context, chatID string) ([]app.CommunityGroup, error) {
	return []app.CommunityGroup{{ID: "sub@g.us", Name: "Sub"}}, nil
}
func (fixtureCommands) LinkCommunityGroup(context.Context, string, string) error   { return nil }
func (fixtureCommands) UnlinkCommunityGroup(context.Context, string, string) error { return nil }
func (fixtureCommands) RefreshChannels(context.Context) ([]appstore.Channel, error) {
	return []appstore.Channel{{ID: "chan@newsletter", Name: "Chan"}}, nil
}
func (fixtureCommands) FollowChannel(context.Context, string) error { return nil }
func (fixtureCommands) FollowChannelByInvite(_ context.Context, invite string) (appstore.Channel, error) {
	return appstore.Channel{ID: "chan@newsletter", Name: "Chan"}, nil
}
func (fixtureCommands) UnfollowChannel(context.Context, string) error            { return nil }
func (fixtureCommands) SetChannelMuted(context.Context, string, bool) error      { return nil }
func (fixtureCommands) MarkChannelViewed(context.Context, string, []int64) error { return nil }
func (fixtureCommands) ReactToChannelMessage(context.Context, string, int64, string) error {
	return nil
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

func (s conformanceSession) Items(max int) []protocol.Item {
	s.faults.sleepIfArmed()
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
	var scriptPath string
	var faultSpec string
	flag.StringVar(&socketPath, "socket", "", "unix socket path to serve")
	flag.StringVar(&readyFile, "ready-file", "", "write this file after the fixture is listening")
	flag.StringVar(&scriptPath, "script", "", "replay a stream recorded by scripts/record-stream")
	flag.StringVar(&faultSpec, "fault", "", "faults to arm: name[:one-in-N], comma separated, or all")
	flag.Parse()

	if socketPath == "" {
		log.Fatal("--socket is required")
	}

	faults, err := parseFaults(faultSpec)
	if err != nil {
		log.Fatalf("--fault: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	base := filepath.Dir(socketPath)
	daemon := app.NewDaemon(app.Paths{
		DataDir:  filepath.Join(base, "data"),
		CacheDir: filepath.Join(base, "cache"),
	})
	daemon.SetState(app.StateOnline)

	server, err := protocol.New(socketPath, nil, daemon)
	if err != nil {
		log.Fatalf("start protocol fixture: %v", err)
	}
	protocol.RegisterDaemonViews(server, daemon, nil, nil)
	protocol.RegisterDaemonCommands(server, fixtureCommands{})
	server.RegisterView("conformance", conformanceView{faults: faults})

	// A recorded stream replaces the daemon views it covers, so a replay goes
	// through the real engine (windows, sort ordering, ready) over real frames
	// rather than over three synthetic rows.
	if scriptPath != "" {
		recorded, loadErr := loadScript(scriptPath)
		if loadErr != nil {
			log.Fatalf("--script: %v", loadErr)
		}
		view := scriptedView{script: recorded, faults: faults}
		for _, name := range recorded.views {
			server.RegisterView(name, view)
		}
		log.Printf("replaying %d view(s) from %s", len(recorded.views), scriptPath)
	}
	if faultSpec != "" {
		log.Printf("faults armed: %s", faultSpec)
	}
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
