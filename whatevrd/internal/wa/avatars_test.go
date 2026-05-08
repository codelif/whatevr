package wa

import (
	"testing"

	appstore "whatevrd/internal/store"
)

func TestChatNeedsAvatarRefresh(t *testing.T) {
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
			chat: appstore.Chat{AvatarPictureID: "123", AvatarLocalPath: "/tmp/avatar.jpg"},
			want: false,
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
