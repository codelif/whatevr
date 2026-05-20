package wa

import (
	"os"
	"path/filepath"
	"testing"

	"go.mau.fi/whatsmeow/types"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

func TestChatNeedsAvatarRefresh(t *testing.T) {
	cachedPath := filepath.Join(t.TempDir(), "avatar.jpg")
	if err := os.WriteFile(cachedPath, []byte("avatar"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		chat appstore.Chat
		want bool
	}{
		{
			name: "missing picture id",
			chat: appstore.Chat{AvatarLocalPath: "/tmp/avatar.jpg"},
			want: true,
		},
		{
			name: "missing local path",
			chat: appstore.Chat{AvatarPictureID: "123"},
			want: true,
		},
		{
			name: "both cached",
			chat: appstore.Chat{AvatarPictureID: "123", AvatarLocalPath: cachedPath},
			want: false,
		},
		{
			name: "cached file missing",
			chat: appstore.Chat{AvatarPictureID: "123", AvatarLocalPath: filepath.Join(t.TempDir(), "missing.jpg")},
			want: true,
		},
		{
			name: "whitespace treated as missing",
			chat: appstore.Chat{AvatarPictureID: " ", AvatarLocalPath: "\t"},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := chatNeedsAvatarRefresh(tc.chat); got != tc.want {
				t.Fatalf("chatNeedsAvatarRefresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSenderNeedsAvatarRefresh(t *testing.T) {
	cachedPath := filepath.Join(t.TempDir(), "avatar.jpg")
	if err := os.WriteFile(cachedPath, []byte("avatar"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		sender appstore.SenderProfile
		want   bool
	}{
		{
			name:   "missing picture id",
			sender: appstore.SenderProfile{AvatarLocalPath: cachedPath},
			want:   true,
		},
		{
			name:   "missing local path",
			sender: appstore.SenderProfile{AvatarPictureID: "123"},
			want:   true,
		},
		{
			name:   "both cached",
			sender: appstore.SenderProfile{AvatarPictureID: "123", AvatarLocalPath: cachedPath},
			want:   false,
		},
		{
			name:   "cached file missing",
			sender: appstore.SenderProfile{AvatarPictureID: "123", AvatarLocalPath: filepath.Join(t.TempDir(), "missing.jpg")},
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := senderNeedsAvatarRefresh(tc.sender); got != tc.want {
				t.Fatalf("senderNeedsAvatarRefresh() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShouldSkipAvatarJIDSkipsInvalidZeroUser(t *testing.T) {
	jid := types.NewJID("0", types.DefaultUserServer)
	if !shouldSkipAvatarJID(jid) {
		t.Fatal("0@s.whatsapp.net should be skipped")
	}

	valid := types.NewJID("919999999999", types.DefaultUserServer)
	if shouldSkipAvatarJID(valid) {
		t.Fatal("valid user JID should not be skipped")
	}
}

func TestDaemonActiveHistorySyncBlocksAvatarRefreshBeforeStoreCheck(t *testing.T) {
	daemon := app.NewDaemon(app.Paths{})
	daemon.PublishHistorySyncProgress(app.HistorySyncEvent{SyncType: app.HistorySyncTypeRecent, ProgressPercent: 43})
	client := &Client{daemon: daemon}

	if !client.avatarRefreshBlockedByHistorySync(t.Context()) {
		t.Fatal("active history sync should block avatar refresh")
	}
}
