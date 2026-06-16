package rpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
)

// SettingsController is the backend for user-account and app preferences. It is
// implemented by the WhatsApp client (privacy/blocklist/profile go to the live
// connection; app preferences are daemon_config-persisted).
type SettingsController interface {
	GetPrivacySettings(context.Context) (app.PrivacySettings, error)
	SetPrivacySetting(ctx context.Context, category, audience string, readReceipts bool) (app.PrivacySettings, error)
	GetBlocklist(context.Context) ([]app.BlockedContact, error)
	UpdateBlocklist(ctx context.Context, jid string, block bool) ([]app.BlockedContact, error)
	SetProfileStatus(ctx context.Context, statusText string) error
	GetAppPreferences(context.Context) (app.AppPreferences, error)
	SetAppPreferences(context.Context, app.AppPreferences) (app.AppPreferences, error)
}

type SettingsService struct {
	pb.UnimplementedSettingsServiceServer
	controller SettingsController
}

func NewSettingsService(controller SettingsController) *SettingsService {
	return &SettingsService{controller: controller}
}

func (s *SettingsService) GetPrivacySettings(ctx context.Context, _ *pb.GetPrivacySettingsRequest) (*pb.GetPrivacySettingsResponse, error) {
	if s.controller == nil {
		return nil, status.Error(codes.Unimplemented, "settings controller is not available")
	}
	settings, err := s.controller.GetPrivacySettings(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.GetPrivacySettingsResponse{Settings: toProtoPrivacy(settings)}, nil
}

func (s *SettingsService) SetPrivacySetting(ctx context.Context, req *pb.SetPrivacySettingRequest) (*pb.SetPrivacySettingResponse, error) {
	if s.controller == nil {
		return nil, status.Error(codes.Unimplemented, "settings controller is not available")
	}
	category := privacyCategoryKey(req.GetCategory())
	if category == "" {
		return nil, status.Error(codes.InvalidArgument, "unknown privacy category")
	}
	audience := privacyAudienceValue(req.GetAudience())
	if category != "read_receipts" && audience == "" {
		return nil, status.Error(codes.InvalidArgument, "audience is required")
	}
	settings, err := s.controller.SetPrivacySetting(ctx, category, audience, req.GetReadReceipts())
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.SetPrivacySettingResponse{Settings: toProtoPrivacy(settings)}, nil
}

func (s *SettingsService) GetBlocklist(ctx context.Context, _ *pb.GetBlocklistRequest) (*pb.GetBlocklistResponse, error) {
	if s.controller == nil {
		return nil, status.Error(codes.Unimplemented, "settings controller is not available")
	}
	contacts, err := s.controller.GetBlocklist(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.GetBlocklistResponse{Contacts: toProtoBlockedContacts(contacts)}, nil
}

func (s *SettingsService) UpdateBlocklist(ctx context.Context, req *pb.UpdateBlocklistRequest) (*pb.UpdateBlocklistResponse, error) {
	if s.controller == nil {
		return nil, status.Error(codes.Unimplemented, "settings controller is not available")
	}
	if req.GetJid() == "" {
		return nil, status.Error(codes.InvalidArgument, "jid is required")
	}
	contacts, err := s.controller.UpdateBlocklist(ctx, req.GetJid(), req.GetBlock())
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.UpdateBlocklistResponse{Contacts: toProtoBlockedContacts(contacts)}, nil
}

func (s *SettingsService) SetProfileStatus(ctx context.Context, req *pb.SetProfileStatusRequest) (*pb.SetProfileStatusResponse, error) {
	if s.controller == nil {
		return nil, status.Error(codes.Unimplemented, "settings controller is not available")
	}
	if err := s.controller.SetProfileStatus(ctx, req.GetStatusText()); err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.SetProfileStatusResponse{}, nil
}

func (s *SettingsService) GetAppPreferences(ctx context.Context, _ *pb.GetAppPreferencesRequest) (*pb.GetAppPreferencesResponse, error) {
	if s.controller == nil {
		return nil, status.Error(codes.Unimplemented, "settings controller is not available")
	}
	prefs, err := s.controller.GetAppPreferences(ctx)
	if err != nil {
		return nil, err
	}
	return &pb.GetAppPreferencesResponse{Preferences: toProtoAppPreferences(prefs)}, nil
}

func (s *SettingsService) SetAppPreferences(ctx context.Context, req *pb.SetAppPreferencesRequest) (*pb.SetAppPreferencesResponse, error) {
	if s.controller == nil {
		return nil, status.Error(codes.Unimplemented, "settings controller is not available")
	}
	prefs, err := s.controller.SetAppPreferences(ctx, fromProtoAppPreferences(req.GetPreferences()))
	if err != nil {
		return nil, err
	}
	return &pb.SetAppPreferencesResponse{Preferences: toProtoAppPreferences(prefs)}, nil
}

// ---- proto <-> app mapping ----

func privacyCategoryKey(category pb.PrivacyCategory) string {
	switch category {
	case pb.PrivacyCategory_PRIVACY_CATEGORY_LAST_SEEN:
		return "last_seen"
	case pb.PrivacyCategory_PRIVACY_CATEGORY_ONLINE:
		return "online"
	case pb.PrivacyCategory_PRIVACY_CATEGORY_PROFILE_PHOTO:
		return "profile_photo"
	case pb.PrivacyCategory_PRIVACY_CATEGORY_ABOUT:
		return "about"
	case pb.PrivacyCategory_PRIVACY_CATEGORY_READ_RECEIPTS:
		return "read_receipts"
	case pb.PrivacyCategory_PRIVACY_CATEGORY_GROUP_ADD:
		return "group_add"
	case pb.PrivacyCategory_PRIVACY_CATEGORY_CALL_ADD:
		return "call_add"
	default:
		return ""
	}
}

func privacyAudienceValue(audience pb.PrivacyAudience) string {
	switch audience {
	case pb.PrivacyAudience_PRIVACY_AUDIENCE_EVERYONE:
		return "all"
	case pb.PrivacyAudience_PRIVACY_AUDIENCE_CONTACTS:
		return "contacts"
	case pb.PrivacyAudience_PRIVACY_AUDIENCE_CONTACTS_EXCEPT:
		return "contact_blacklist"
	case pb.PrivacyAudience_PRIVACY_AUDIENCE_NOBODY:
		return "none"
	case pb.PrivacyAudience_PRIVACY_AUDIENCE_MATCH_LAST_SEEN:
		return "match_last_seen"
	case pb.PrivacyAudience_PRIVACY_AUDIENCE_KNOWN:
		return "known"
	default:
		return ""
	}
}

func protoAudience(value string) pb.PrivacyAudience {
	switch value {
	case "all":
		return pb.PrivacyAudience_PRIVACY_AUDIENCE_EVERYONE
	case "contacts":
		return pb.PrivacyAudience_PRIVACY_AUDIENCE_CONTACTS
	case "contact_blacklist":
		return pb.PrivacyAudience_PRIVACY_AUDIENCE_CONTACTS_EXCEPT
	case "none":
		return pb.PrivacyAudience_PRIVACY_AUDIENCE_NOBODY
	case "match_last_seen":
		return pb.PrivacyAudience_PRIVACY_AUDIENCE_MATCH_LAST_SEEN
	case "known":
		return pb.PrivacyAudience_PRIVACY_AUDIENCE_KNOWN
	default:
		return pb.PrivacyAudience_PRIVACY_AUDIENCE_UNSPECIFIED
	}
}

func toProtoPrivacy(s app.PrivacySettings) *pb.PrivacySettingsValues {
	return &pb.PrivacySettingsValues{
		LastSeen:     protoAudience(s.LastSeen),
		Online:       protoAudience(s.Online),
		ProfilePhoto: protoAudience(s.ProfilePhoto),
		About:        protoAudience(s.About),
		ReadReceipts: s.ReadReceipts,
		GroupAdd:     protoAudience(s.GroupAdd),
		CallAdd:      protoAudience(s.CallAdd),
	}
}

func toProtoBlockedContacts(contacts []app.BlockedContact) []*pb.BlockedContact {
	out := make([]*pb.BlockedContact, 0, len(contacts))
	for _, contact := range contacts {
		out = append(out, &pb.BlockedContact{
			Jid:             contact.JID,
			DisplayName:     contact.DisplayName,
			PhoneNumber:     contact.PhoneNumber,
			AvatarLocalPath: contact.AvatarLocalPath,
		})
	}
	return out
}

func toProtoAppPreferences(p app.AppPreferences) *pb.AppPreferences {
	return &pb.AppPreferences{
		NotificationsEnabled:  p.NotificationsEnabled,
		NotificationSound:     p.NotificationSound,
		NotificationPreview:   p.NotificationPreview,
		AutoDownloadPhotos:    p.AutoDownloadPhotos,
		AutoDownloadVideos:    p.AutoDownloadVideos,
		AutoDownloadAudio:     p.AutoDownloadAudio,
		AutoDownloadDocuments: p.AutoDownloadDocuments,
	}
}

func fromProtoAppPreferences(p *pb.AppPreferences) app.AppPreferences {
	if p == nil {
		return app.DefaultAppPreferences()
	}
	return app.AppPreferences{
		NotificationsEnabled:  p.GetNotificationsEnabled(),
		NotificationSound:     p.GetNotificationSound(),
		NotificationPreview:   p.GetNotificationPreview(),
		AutoDownloadPhotos:    p.GetAutoDownloadPhotos(),
		AutoDownloadVideos:    p.GetAutoDownloadVideos(),
		AutoDownloadAudio:     p.GetAutoDownloadAudio(),
		AutoDownloadDocuments: p.GetAutoDownloadDocuments(),
	}
}
