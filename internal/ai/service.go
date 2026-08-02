package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"tg-search/internal/config"
	"tg-search/internal/model"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	taskpkg "tg-search/internal/task"
)

type Enhancer interface {
	Enhance(context.Context, EnhancementRequest) (EnhancementResponse, error)
}

type ServiceOptions struct {
	Settings    *repository.SettingsRepository
	Defaults    config.Config
	Messages    *repository.MessageRepository
	Links       *repository.LinkRepository
	Resources   *resource.Service
	NewEnhancer func(config.AIMediaMetadataSettings) Enhancer
	Logger      *zap.Logger
}

type Service struct {
	settings    *repository.SettingsRepository
	defaults    config.Config
	messages    *repository.MessageRepository
	links       *repository.LinkRepository
	resources   *resource.Service
	newEnhancer func(config.AIMediaMetadataSettings) Enhancer
	logger      *zap.Logger
}

func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	service := &Service{
		settings:    opts.Settings,
		defaults:    opts.Defaults,
		messages:    opts.Messages,
		links:       opts.Links,
		resources:   opts.Resources,
		newEnhancer: opts.NewEnhancer,
		logger:      logger,
	}
	if service.newEnhancer == nil {
		service.newEnhancer = func(settings config.AIMediaMetadataSettings) Enhancer {
			return NewEngine(settings)
		}
	}
	return service
}

func (s *Service) RunMediaMetadataTask(ctx context.Context, item model.Task, progress taskpkg.ProgressSink) error {
	if s == nil || s.settings == nil || s.messages == nil || s.links == nil {
		return errors.New("ai media metadata service is not configured")
	}
	var payload taskpkg.AIMediaMetadataPayload
	if err := json.Unmarshal([]byte(item.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode ai media metadata payload: %w", err)
	}
	if payload.MessageID <= 0 {
		return errors.New("message_id is required")
	}
	settings, err := s.settings.LoadRuntimeSettings(ctx, s.defaults)
	if err != nil {
		return fmt.Errorf("load runtime settings: %w", err)
	}
	aiSettings := settings.AI.MediaMetadata
	if !aiSettings.Enabled {
		return progressIfPresent(ctx, progress, 1, 1, "ai media metadata disabled")
	}
	message, err := s.messages.FindByID(ctx, payload.MessageID)
	if err != nil {
		return err
	}
	links, err := s.links.ListByMessage(ctx, payload.MessageID)
	if err != nil {
		return err
	}
	cloudLinks := filterCloudDriveLinks(links)
	if len(cloudLinks) == 0 {
		return progressIfPresent(ctx, progress, 1, 1, "no cloud-drive links")
	}
	req := buildEnhancementRequest(message, cloudLinks)
	if err := progressIfPresent(ctx, progress, 0, int64(len(cloudLinks)), "requesting ai media metadata"); err != nil {
		return err
	}
	resp, err := s.newEnhancer(aiSettings).Enhance(ctx, req)
	if err != nil {
		return err
	}
	updated, err := s.applyResponse(ctx, cloudLinks, resp)
	if err != nil {
		return err
	}
	if s.resources != nil && updated > 0 {
		s.resources.MarkStatsDirty()
	}
	return progressIfPresent(ctx, progress, int64(updated), int64(len(cloudLinks)), "ai media metadata completed")
}

func progressIfPresent(ctx context.Context, progress taskpkg.ProgressSink, current int64, total int64, message string) error {
	if progress == nil {
		return nil
	}
	return progress.Progress(ctx, current, total, message)
}

func filterCloudDriveLinks(links []model.Link) []model.Link {
	out := make([]model.Link, 0, len(links))
	for _, link := range links {
		if link.Category == "cloud_drive" || (link.Category == "" && isCloudDriveType(link.Type)) {
			out = append(out, link)
		}
	}
	return out
}

func isCloudDriveType(typ string) bool {
	switch typ {
	case "quark", "aliyun", "baidu", "115", "uc", "xunlei", "tianyi", "mobile", "123", "pikpak", "guangya":
		return true
	default:
		return false
	}
}

func buildEnhancementRequest(message model.Message, links []model.Link) EnhancementRequest {
	preprocessor := NewPreProcessor()
	req := EnhancementRequest{
		Message: EnhancementMessage{
			ID:           message.ID,
			Text:         message.Text,
			RawJSON:      message.RawJSON,
			MessageType:  message.MessageType,
			MediaSummary: message.MediaSummary,
		},
		Links: make([]EnhancementLink, 0, len(links)),
		Context: preprocessor.Extract(strings.Join([]string{
			message.Text,
			message.MediaSummary,
		}, "\n")),
	}
	for _, link := range links {
		rawHint := strings.TrimSpace(strings.Join([]string{
			link.Note,
			link.SourceSnippet,
			link.MediaTitle,
			link.MediaYear,
			link.MediaSeason,
			link.MediaEpisode,
			link.MediaQuality,
			link.MediaSize,
		}, " "))
		req.Links = append(req.Links, EnhancementLink{
			LinkID:        link.ID,
			Type:          link.Type,
			URL:           link.URL,
			Password:      link.Password,
			Note:          link.Note,
			SourceSnippet: link.SourceSnippet,
			RawHint:       rawHint,
			Media: MediaMetadata{
				Title:    link.MediaTitle,
				Year:     link.MediaYear,
				Season:   link.MediaSeason,
				Episode:  link.MediaEpisode,
				Quality:  link.MediaQuality,
				Size:     link.MediaSize,
				TMDBID:   link.MediaTMDBID,
				Category: link.MediaCategory,
				Tags:     link.MediaTags,
			},
		})
	}
	return req
}

func (s *Service) applyResponse(ctx context.Context, links []model.Link, resp EnhancementResponse) (int, error) {
	byID := map[int64]int{}
	byURL := map[string]int{}
	for i, link := range links {
		byID[link.ID] = i
		byURL[link.URL] = i
	}
	updated := 0
	for _, item := range resp.Items {
		idx, ok := byID[item.LinkID]
		if !ok && item.URL != "" {
			idx, ok = byURL[item.URL]
		}
		if !ok {
			s.logger.Debug("ai media metadata result ignored for unknown link", zap.Int64("link_id", item.LinkID), zap.String("url", item.URL))
			continue
		}
		link := links[idx]
		if !overlayMedia(&link, item.Media) {
			continue
		}
		if err := s.links.UpdateMediaMetadata(ctx, link); err != nil {
			return updated, err
		}
		links[idx] = link
		updated++
	}
	return updated, nil
}

func overlayMedia(link *model.Link, media MediaMetadata) bool {
	changed := false
	if media.Title != "" && media.Title != link.MediaTitle {
		link.MediaTitle = media.Title
		changed = true
	}
	if media.Year != "" && media.Year != link.MediaYear {
		link.MediaYear = media.Year
		changed = true
	}
	if media.Season != "" && media.Season != link.MediaSeason {
		link.MediaSeason = media.Season
		changed = true
	}
	if media.Episode != "" && media.Episode != link.MediaEpisode {
		link.MediaEpisode = media.Episode
		changed = true
	}
	if media.Quality != "" && media.Quality != link.MediaQuality {
		link.MediaQuality = media.Quality
		changed = true
	}
	if media.Size != "" && media.Size != link.MediaSize {
		link.MediaSize = media.Size
		changed = true
	}
	if media.TMDBID != "" && media.TMDBID != link.MediaTMDBID {
		link.MediaTMDBID = media.TMDBID
		changed = true
	}
	if media.Category != "" && media.Category != link.MediaCategory {
		link.MediaCategory = media.Category
		changed = true
	}
	if media.Tags != "" && media.Tags != link.MediaTags {
		link.MediaTags = media.Tags
		changed = true
	}
	return changed
}
