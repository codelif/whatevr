package protocol

import (
	"context"
	"encoding/json"
	"strings"

	"whatevrd/internal/app"
)

type privacySetParams struct {
	Category string          `json:"category"`
	Value    json.RawMessage `json:"value"`
}

func (h commandHandlers) privacySet(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p privacySetParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	category := strings.TrimSpace(p.Category)
	if !knownPrivacyCategory(category) {
		return nil, errorf(CodeInvalidParams, "unknown privacy category")
	}
	if len(p.Value) == 0 || strings.TrimSpace(string(p.Value)) == "null" {
		return nil, errorf(CodeInvalidParams, "value is required")
	}

	audience := ""
	readReceipts := false
	if category == "read_receipts" {
		var b bool
		if err := json.Unmarshal(p.Value, &b); err != nil {
			return nil, errorf(CodeInvalidParams, "read_receipts value must be a boolean")
		}
		readReceipts = b
	} else {
		if err := json.Unmarshal(p.Value, &audience); err != nil {
			return nil, errorf(CodeInvalidParams, "privacy value must be a string")
		}
		audience = strings.TrimSpace(audience)
		if audience == "" {
			return nil, errorf(CodeInvalidParams, "privacy value is required")
		}
		if !knownPrivacyAudience(audience) {
			return nil, errorf(CodeInvalidParams, "unknown privacy value")
		}
	}

	_, err := h.actions.SetPrivacySetting(context.Background(), category, audience, readReceipts)
	return nil, mapCommandError(err)
}

func knownPrivacyCategory(category string) bool {
	switch category {
	case "last_seen", "online", "profile_photo", "about", "read_receipts", "group_add", "call_add":
		return true
	default:
		return false
	}
}

func knownPrivacyAudience(value string) bool {
	switch value {
	case "all", "contacts", "contact_blacklist", "none", "match_last_seen", "known":
		return true
	default:
		return false
	}
}

type preferencesSetParams struct {
	NotificationsEnabled  *bool `json:"notifications_enabled"`
	NotificationSound     *bool `json:"notification_sound"`
	NotificationPreview   *bool `json:"notification_preview"`
	AutoDownloadPhotos    *bool `json:"auto_download_photos"`
	AutoDownloadVideos    *bool `json:"auto_download_videos"`
	AutoDownloadAudio     *bool `json:"auto_download_audio"`
	AutoDownloadDocuments *bool `json:"auto_download_documents"`
	AutoDownloadStickers  *bool `json:"auto_download_stickers"`
}

func (h commandHandlers) preferencesSet(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p preferencesSetParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	// Apply the partial patch atomically on the daemon side: the read-modify-write
	// happens under the daemon's lock so two concurrent preferences.set calls
	// cannot lose each other's fields.
	_, err := h.actions.UpdateAppPreferences(context.Background(), func(prefs *app.AppPreferences) {
		applyPreferencesPatch(prefs, p)
	})
	return nil, mapCommandError(err)
}

func applyPreferencesPatch(prefs *app.AppPreferences, p preferencesSetParams) {
	if p.NotificationsEnabled != nil {
		prefs.NotificationsEnabled = *p.NotificationsEnabled
	}
	if p.NotificationSound != nil {
		prefs.NotificationSound = *p.NotificationSound
	}
	if p.NotificationPreview != nil {
		prefs.NotificationPreview = *p.NotificationPreview
	}
	if p.AutoDownloadPhotos != nil {
		prefs.AutoDownloadPhotos = *p.AutoDownloadPhotos
	}
	if p.AutoDownloadVideos != nil {
		prefs.AutoDownloadVideos = *p.AutoDownloadVideos
	}
	if p.AutoDownloadAudio != nil {
		prefs.AutoDownloadAudio = *p.AutoDownloadAudio
	}
	if p.AutoDownloadDocuments != nil {
		prefs.AutoDownloadDocuments = *p.AutoDownloadDocuments
	}
	if p.AutoDownloadStickers != nil {
		prefs.AutoDownloadStickers = *p.AutoDownloadStickers
	}
}

type selfSetAboutParams struct {
	// Text is a pointer so a missing field (invalid) is distinguishable from an
	// intentionally empty string (a legitimate request to clear the about).
	Text *string `json:"text"`
}

func (h commandHandlers) selfSetAbout(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p selfSetAboutParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	if p.Text == nil {
		return nil, errorf(CodeInvalidParams, "text is required")
	}
	return nil, mapCommandError(h.actions.SetProfileStatus(context.Background(), *p.Text))
}

type contactBlockParams struct {
	JID     string `json:"jid"`
	Blocked *bool  `json:"blocked"`
}

func (h commandHandlers) contactBlock(_ *conn, req request) (any, *Error) {
	if err := h.requireActions(); err != nil {
		return nil, err
	}
	var p contactBlockParams
	if err := decodeParams(req.Params, &p); err != nil {
		return nil, err
	}
	jid := strings.TrimSpace(p.JID)
	if jid == "" {
		return nil, errorf(CodeInvalidParams, "jid is required")
	}
	// Blocking is a contact-only action: WhatsApp has no notion of blocking a
	// group. Reject a group jid here (F10) rather than let it reach WhatsApp and
	// surface as an opaque failure.
	if strings.HasSuffix(jid, groupJIDSuffix) {
		return nil, errorf(CodeInvalidParams, "jid must be a contact, not a group")
	}
	if p.Blocked == nil {
		return nil, errorf(CodeInvalidParams, "blocked is required")
	}
	_, err := h.actions.UpdateBlocklist(context.Background(), jid, *p.Blocked)
	return nil, mapCommandError(err)
}
