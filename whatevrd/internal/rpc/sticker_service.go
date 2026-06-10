package rpc

import (
	"context"
	"strings"
	"unicode/utf8"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"whatevrd/internal/app"
	"whatevrd/internal/rpc/pb"
	appstore "whatevrd/internal/store"
)

const maxStickerSearchRunes = 256

type StickerController interface {
	ListStickers(context.Context, app.StickerSource, string, int) ([]appstore.Sticker, error)
	ListStickerPacks(context.Context, bool) ([]appstore.StickerPack, error)
	GetStickerPack(context.Context, string) (appstore.StickerPack, []appstore.Sticker, error)
	DownloadSticker(context.Context, string) (appstore.Sticker, error)
	SetStickerPackInstalled(context.Context, string, bool) (appstore.StickerPack, error)
	SendSticker(context.Context, string, string, string) (appstore.SavedTextMessage, error)
}

type StickerService struct {
	pb.UnimplementedStickerServiceServer
	stickers StickerController
}

func NewStickerService(stickers StickerController) *StickerService {
	return &StickerService{stickers: stickers}
}

func (s *StickerService) ListStickers(ctx context.Context, req *pb.ListStickersRequest) (*pb.ListStickersResponse, error) {
	if s.stickers == nil {
		return nil, status.Error(codes.Unimplemented, "sticker controller is not available")
	}
	query := strings.TrimSpace(req.GetQuery())
	if utf8.RuneCountInString(query) > maxStickerSearchRunes {
		return nil, status.Errorf(codes.InvalidArgument, "query must be <= %d characters", maxStickerSearchRunes)
	}
	stickers, err := s.stickers.ListStickers(ctx, fromProtoStickerSource(req.GetSource()), query, int(req.GetLimit()))
	if err != nil {
		return nil, err
	}
	return &pb.ListStickersResponse{Stickers: toProtoStickers(stickers)}, nil
}

func (s *StickerService) ListStickerPacks(ctx context.Context, req *pb.ListStickerPacksRequest) (*pb.ListStickerPacksResponse, error) {
	if s.stickers == nil {
		return nil, status.Error(codes.Unimplemented, "sticker controller is not available")
	}
	packs, err := s.stickers.ListStickerPacks(ctx, req.GetForceRefresh())
	if err != nil {
		return nil, err
	}
	protoPacks := make([]*pb.StickerPack, 0, len(packs))
	for _, pack := range packs {
		protoPacks = append(protoPacks, toProtoStickerPack(pack))
	}
	return &pb.ListStickerPacksResponse{Packs: protoPacks}, nil
}

func (s *StickerService) GetStickerPack(ctx context.Context, req *pb.GetStickerPackRequest) (*pb.GetStickerPackResponse, error) {
	if s.stickers == nil {
		return nil, status.Error(codes.Unimplemented, "sticker controller is not available")
	}
	pack, stickers, err := s.stickers.GetStickerPack(ctx, strings.TrimSpace(req.GetPackId()))
	if err != nil {
		return nil, err
	}
	return &pb.GetStickerPackResponse{
		Pack:     toProtoStickerPack(pack),
		Stickers: toProtoStickers(stickers),
	}, nil
}

func (s *StickerService) DownloadSticker(ctx context.Context, req *pb.DownloadStickerRequest) (*pb.DownloadStickerResponse, error) {
	if s.stickers == nil {
		return nil, status.Error(codes.Unimplemented, "sticker controller is not available")
	}
	sticker, err := s.stickers.DownloadSticker(ctx, strings.TrimSpace(req.GetCacheKey()))
	if err != nil {
		return nil, err
	}
	return &pb.DownloadStickerResponse{Sticker: toProtoStoreSticker(sticker)}, nil
}

func (s *StickerService) SetStickerPackInstalled(ctx context.Context, req *pb.SetStickerPackInstalledRequest) (*pb.SetStickerPackInstalledResponse, error) {
	if s.stickers == nil {
		return nil, status.Error(codes.Unimplemented, "sticker controller is not available")
	}
	pack, err := s.stickers.SetStickerPackInstalled(ctx, strings.TrimSpace(req.GetPackId()), req.GetInstalled())
	if err != nil {
		return nil, err
	}
	return &pb.SetStickerPackInstalledResponse{Pack: toProtoStickerPack(pack)}, nil
}

func (s *StickerService) SendSticker(ctx context.Context, req *pb.SendStickerRequest) (*pb.SendStickerResponse, error) {
	if s.stickers == nil {
		return nil, status.Error(codes.Unimplemented, "sticker controller is not available")
	}
	if strings.TrimSpace(req.GetChatId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "chat_id is required")
	}
	if strings.TrimSpace(req.GetCacheKey()) == "" {
		return nil, status.Error(codes.InvalidArgument, "cache_key is required")
	}
	saved, err := s.stickers.SendSticker(ctx, strings.TrimSpace(req.GetChatId()), strings.TrimSpace(req.GetCacheKey()), strings.TrimSpace(req.GetReplyToMessageId()))
	if err != nil {
		return nil, err
	}
	return &pb.SendStickerResponse{Message: toProtoMessage(toAppMessage(saved.Message))}, nil
}

func toProtoStickers(stickers []appstore.Sticker) []*pb.Sticker {
	protoStickers := make([]*pb.Sticker, 0, len(stickers))
	for _, sticker := range stickers {
		protoStickers = append(protoStickers, toProtoStoreSticker(sticker))
	}
	return protoStickers
}

func toProtoStoreSticker(sticker appstore.Sticker) *pb.Sticker {
	return &pb.Sticker{
		CacheKey:          sticker.CacheKey,
		LocalPath:         sticker.LocalPath,
		MimeType:          sticker.MimeType,
		IsAnimated:        sticker.IsAnimated,
		Width:             sticker.Width,
		Height:            sticker.Height,
		Emojis:            strings.Fields(sticker.Emojis),
		AccessibilityText: sticker.AccessibilityText,
		PackId:            sticker.PackID,
		IsFavorite:        sticker.IsFavorite,
		LastUsedUnix:      sticker.LastUsed,
		Weight:            float32(sticker.RecentWeight),
	}
}

func toProtoStickerPack(pack appstore.StickerPack) *pb.StickerPack {
	return &pb.StickerPack{
		Id:              pack.ID,
		Name:            pack.Name,
		Publisher:       pack.Publisher,
		Description:     pack.Description,
		Animated:        pack.Animated,
		Lottie:          pack.Lottie,
		TrayLocalPath:   pack.TrayLocalPath,
		StickerCount:    pack.StickerCount,
		Installed:       pack.Installed,
		ContentsFetched: pack.ContentsFetchedAt > 0,
	}
}

func fromProtoStickerSource(source pb.StickerSource) app.StickerSource {
	switch source {
	case pb.StickerSource_STICKER_SOURCE_RECENT:
		return app.StickerSourceRecent
	case pb.StickerSource_STICKER_SOURCE_FAVORITE:
		return app.StickerSourceFavorite
	case pb.StickerSource_STICKER_SOURCE_ALL:
		return app.StickerSourceAll
	default:
		return app.StickerSourceUnspecified
	}
}
