package wa

import "testing"

// Auto-trusting changed identities is the default (official-client behavior);
// only an explicit opt-out enables strict mode. A regression here silently
// deadlocks both directions with any contact who reinstalls WhatsApp.
func TestAutoTrustIdentityDefaultsOn(t *testing.T) {
	cases := []struct {
		value string
		want  bool
	}{
		{"", true},
		{"1", true},
		{"true", true},
		{"garbage", true},
		{"0", false},
		{"false", false},
		{"no", false},
		{"off", false},
		{" OFF ", false},
	}
	for _, tc := range cases {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv("WHATEVRD_AUTO_TRUST_IDENTITY", tc.value)
			if got := autoTrustIdentityEnabled(); got != tc.want {
				t.Fatalf("autoTrustIdentityEnabled() with %q = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
