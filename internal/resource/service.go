package resource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"tg-search/internal/link"
	"tg-search/internal/model"
	"tg-search/internal/repository"
	"tg-search/internal/searchrank"
	"tg-search/internal/task"
)

type Query struct {
	Keyword   string
	Type      string
	Category  string
	AccountID int64
	ChannelID int64
	Extension string
	Sort      string
	DateFrom  *time.Time
	DateTo    *time.Time
	Limit     int
	Offset    int
	MaxLimit  int
}

type ScoreExplain struct {
	SourceChannelCount int `json:"source_channel_count"`
	MessageCount       int `json:"message_count"`
	ProviderCount      int `json:"provider_count"`
	RecencyScore       int `json:"recency_score"`
	TypeScore          int `json:"type_score"`
	MetadataScore      int `json:"metadata_score"`
}

type Item struct {
	ID                string       `json:"id"`
	Kind              string       `json:"kind"`
	Type              string       `json:"type,omitempty"`
	Category          string       `json:"category"`
	URL               string       `json:"url,omitempty"`
	Password          string       `json:"password,omitempty"`
	TelegramFileID    int64        `json:"telegram_file_id,omitempty"`
	FileName          string       `json:"file_name,omitempty"`
	Extension         string       `json:"extension,omitempty"`
	MimeType          string       `json:"mime_type,omitempty"`
	SizeBytes         int64        `json:"size_bytes,omitempty"`
	Note              string       `json:"note,omitempty"`
	Title             string       `json:"title,omitempty"`
	SourceSnippet     string       `json:"source_snippet,omitempty"`
	MediaTitle        string       `json:"-"`
	MediaYear         string       `json:"-"`
	MediaSeason       string       `json:"-"`
	MediaEpisode      string       `json:"-"`
	MediaQuality      string       `json:"-"`
	MediaSize         string       `json:"-"`
	MediaTMDBID       string       `json:"-"`
	MediaCategory     string       `json:"-"`
	MediaTags         string       `json:"-"`
	Datetime          time.Time    `json:"datetime"`
	AccountID         int64        `json:"account_id"`
	ChannelID         int64        `json:"channel_id"`
	TelegramChannelID int64        `json:"telegram_channel_id"`
	ChannelTitle      string       `json:"channel_title"`
	ChannelUsername   string       `json:"channel_username"`
	TelegramMessageID int64        `json:"telegram_message_id"`
	MessageType       string       `json:"message_type,omitempty"`
	MediaSummary      string       `json:"-"`
	Media             *Media       `json:"media,omitempty"`
	Score             int          `json:"score"`
	ScoreExplain      ScoreExplain `json:"score_explain"`
}

type Media struct {
	ImageURL string `json:"image_url,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
	Title    string `json:"title,omitempty"`
	Year     string `json:"year,omitempty"`
	Season   string `json:"season,omitempty"`
	Episode  string `json:"episode,omitempty"`
	Quality  string `json:"quality,omitempty"`
	Size     string `json:"size,omitempty"`
	TMDBID   string `json:"tmdb_id,omitempty"`
	Category string `json:"category,omitempty"`
	Tags     string `json:"tags,omitempty"`
	Summary  string `json:"summary,omitempty"`
}

func (m Media) Empty() bool {
	return m == Media{}
}

func (item *Item) SetMediaMetadata(title, year, season, episode, quality, size, tmdbID, category, tags, summary string) {
	media := item.ensureMedia()
	media.Title = title
	media.Year = year
	media.Season = season
	media.Episode = episode
	media.Quality = quality
	media.Size = size
	media.TMDBID = tmdbID
	media.Category = category
	media.Tags = tags
	media.Summary = summary
	item.dropEmptyMedia()
}

func (item *Item) SetMediaURLs(imageURL, videoURL string) {
	media := item.ensureMedia()
	media.ImageURL = imageURL
	media.VideoURL = videoURL
	item.dropEmptyMedia()
}

func (item *Item) ensureMedia() *Media {
	if item.Media == nil {
		item.Media = &Media{}
	}
	return item.Media
}

func (item *Item) dropEmptyMedia() {
	if item.Media != nil && item.Media.Empty() {
		item.Media = nil
	}
}

type ListResult struct {
	Items   []Item         `json:"items"`
	Total   int            `json:"total"`
	Grouped map[string]int `json:"grouped"`
}

type DeleteManyResult struct {
	Deleted    int      `json:"deleted"`
	MissingIDs []string `json:"missing_ids"`
}

type Service struct {
	links     *repository.LinkRepository
	files     *repository.FileRepository
	stats     *repository.ResourceStatsRepository
	index     *repository.ResourceIndexRepository
	messages  *repository.MessageRepository
	extractor *link.Extractor
}

var ErrInvalidResourceID = errors.New("invalid resource id")

func NewService(links *repository.LinkRepository, files *repository.FileRepository, extras ...any) *Service {
	service := &Service{links: links, files: files}
	for _, extra := range extras {
		switch value := extra.(type) {
		case *repository.ResourceStatsRepository:
			service.stats = value
		case *repository.ResourceIndexRepository:
			service.index = value
		case *repository.MessageRepository:
			service.messages = value
		case *link.Extractor:
			service.extractor = value
		}
	}
	return service
}

// repairProgressSink mirrors task.ProgressSink so this package need not import
// the task package; task.ProgressSink satisfies it structurally.
type repairProgressSink interface {
	Progress(ctx context.Context, progress, total int64, message string) error
	Status(ctx context.Context) (string, error)
}

// RepairSummary reports the outcome of a media-title repair pass.
type RepairSummary struct {
	Affected          int `json:"affected"`
	Changed           int `json:"changed"`
	Unchanged         int `json:"unchanged"`
	RefreshedMessages int `json:"refreshed_messages"`
}

// RepairMediaTitles repairs telegram_links rows whose media_title was clobbered
// by a provider label. It re-parses each affected message's stored text with the
// fixed extractor and updates media_title only where the re-parsed title is
// non-empty and differs. When dryRun is true it plans but writes nothing.
// resource_index is refreshed for the changed messages so the derived title and
// the FTS index stay consistent.
func (s *Service) RepairMediaTitles(ctx context.Context, sink repairProgressSink, dryRun bool) (RepairSummary, error) {
	var summary RepairSummary
	if s.extractor == nil || s.messages == nil || s.index == nil {
		return summary, fmt.Errorf("media title repair requires extractor, message repository, and index repository")
	}
	candidates, err := s.links.ListMediaTitleLabelCandidates(ctx)
	if err != nil {
		return summary, err
	}
	summary.Affected = len(candidates)
	if sink != nil {
		_ = sink.Progress(ctx, 0, int64(summary.Affected), fmt.Sprintf("scanned %d affected links", summary.Affected))
	}

	seen := map[int64]struct{}{}
	msgIDs := make([]int64, 0, len(candidates))
	for _, c := range candidates {
		if _, ok := seen[c.MessageID]; !ok {
			seen[c.MessageID] = struct{}{}
			msgIDs = append(msgIDs, c.MessageID)
		}
	}
	texts, err := s.messages.BatchTextByMessageIDs(ctx, msgIDs)
	if err != nil {
		return summary, err
	}

	byMessage := map[int64][]repository.MediaTitleCandidate{}
	for _, c := range candidates {
		byMessage[c.MessageID] = append(byMessage[c.MessageID], c)
	}
	type change struct {
		id        int64
		messageID int64
		title     string
	}
	var changes []change
	for messageID, messageLinks := range byMessage {
		urlToTitle := map[string]string{}
		if text := strings.TrimSpace(texts[messageID]); text != "" {
			for _, parsed := range s.extractor.Extract(text) {
				if parsed.URL != "" {
					urlToTitle[parsed.URL] = parsed.MediaTitle
				}
			}
		}
		for _, candidate := range messageLinks {
			newTitle := urlToTitle[candidate.URL]
			if newTitle == "" || newTitle == candidate.MediaTitle {
				summary.Unchanged++
				continue
			}
			changes = append(changes, change{candidate.ID, candidate.MessageID, newTitle})
		}
	}

	if dryRun {
		summary.Changed = len(changes)
		if sink != nil {
			_ = sink.Progress(ctx, int64(summary.Changed), int64(summary.Affected),
				fmt.Sprintf("dry-run: would repair %d of %d titles", summary.Changed, summary.Affected))
		}
		return summary, nil
	}

	if sink != nil {
		_ = sink.Progress(ctx, 0, int64(len(changes)), fmt.Sprintf("applying %d title updates", len(changes)))
	}
	for _, c := range changes {
		if err := s.links.UpdateMediaTitle(ctx, c.id, c.title); err != nil {
			return summary, err
		}
	}
	summary.Changed = len(changes)

	changedSeen := map[int64]struct{}{}
	changedMsgIDs := make([]int64, 0, len(changes))
	for _, c := range changes {
		if _, ok := changedSeen[c.messageID]; !ok {
			changedSeen[c.messageID] = struct{}{}
			changedMsgIDs = append(changedMsgIDs, c.messageID)
		}
	}
	if err := s.index.RefreshMessages(ctx, changedMsgIDs); err != nil {
		return summary, fmt.Errorf("refresh resource_index: %w", err)
	}
	summary.RefreshedMessages = len(changedMsgIDs)
	if sink != nil {
		_ = sink.Progress(ctx, int64(summary.Changed), int64(summary.Affected),
			fmt.Sprintf("repaired %d of %d titles", summary.Changed, summary.Affected))
	}
	return summary, nil
}

// RunRepairMediaTitleTask is the task-system handler for media-title repair.
// It decodes an optional RepairMediaTitlePayload and delegates to
// RepairMediaTitles.
func (s *Service) RunRepairMediaTitleTask(ctx context.Context, item model.Task, progress task.ProgressSink) error {
	var payload task.RepairMediaTitlePayload
	if item.PayloadJSON != "" {
		_ = json.Unmarshal([]byte(item.PayloadJSON), &payload)
	}
	if _, err := s.RepairMediaTitles(ctx, progress, payload.DryRun); err != nil {
		return err
	}
	return nil
}

func (s *Service) indexedList(ctx context.Context, query Query) (ListResult, bool, error) {
	if s.index == nil {
		return ListResult{}, false, nil
	}
	result, err := s.index.List(ctx, model.ResourceIndexQuery{
		Keyword:   query.Keyword,
		Type:      query.Type,
		Category:  query.Category,
		AccountID: query.AccountID,
		ChannelID: query.ChannelID,
		Extension: query.Extension,
		Sort:      query.Sort,
		DateFrom:  query.DateFrom,
		DateTo:    query.DateTo,
		Limit:     query.Limit,
		Offset:    query.Offset,
		MaxLimit:  query.MaxLimit,
	})
	if err != nil {
		return ListResult{}, true, err
	}
	items := make([]Item, 0, len(result.Items))
	now := time.Now().UTC()
	for _, indexed := range result.Items {
		items = append(items, itemFromIndex(indexed, now))
	}
	return ListResult{Items: items, Total: result.Total, Grouped: normalizeGrouped(result.Grouped)}, true, nil
}

func itemFromIndex(indexed model.ResourceIndexItem, now time.Time) Item {
	item := Item{
		ID:                indexed.ResourceID,
		Kind:              indexed.Kind,
		Type:              indexed.Type,
		Category:          indexed.Category,
		URL:               indexed.URL,
		Password:          indexed.Password,
		TelegramFileID:    indexed.TelegramFileID,
		FileName:          indexed.FileName,
		Extension:         indexed.Extension,
		MimeType:          indexed.MimeType,
		SizeBytes:         indexed.SizeBytes,
		Note:              indexed.Note,
		Title:             indexed.Title,
		SourceSnippet:     indexed.SourceSnippet,
		MediaTitle:        indexed.MediaTitle,
		MediaYear:         indexed.MediaYear,
		MediaSeason:       indexed.MediaSeason,
		MediaEpisode:      indexed.MediaEpisode,
		MediaQuality:      indexed.MediaQuality,
		MediaSize:         indexed.MediaSize,
		MediaTMDBID:       indexed.MediaTMDBID,
		MediaCategory:     indexed.MediaCategory,
		MediaTags:         indexed.MediaTags,
		Datetime:          indexed.Datetime,
		AccountID:         indexed.AccountID,
		ChannelID:         indexed.ChannelID,
		TelegramChannelID: indexed.TelegramChannelID,
		ChannelTitle:      indexed.ChannelTitle,
		ChannelUsername:   indexed.ChannelUsername,
		TelegramMessageID: indexed.TelegramMessageID,
		MessageType:       indexed.MessageType,
		MediaSummary:      indexed.MediaSummary,
	}
	item.SetMediaMetadata(indexed.MediaTitle, indexed.MediaYear, indexed.MediaSeason, indexed.MediaEpisode, indexed.MediaQuality, indexed.MediaSize, indexed.MediaTMDBID, indexed.MediaCategory, indexed.MediaTags, indexed.MediaSummary)
	item.Score, item.ScoreExplain = itemScore(item, resourceScoreStats{
		SourceChannelCount: indexed.SourceChannelCount,
		MessageCount:       indexed.MessageCount,
		ProviderCount:      indexed.ProviderCount,
	}, now)
	return item
}

func (s *Service) ListIndexed(ctx context.Context, query model.ResourceIndexQuery) (ListResult, bool, error) {
	if s.index == nil {
		return ListResult{}, false, nil
	}
	result, err := s.index.List(ctx, query)
	if err != nil {
		return ListResult{}, true, err
	}
	items := make([]Item, 0, len(result.Items))
	now := time.Now().UTC()
	for _, indexed := range result.Items {
		items = append(items, itemFromIndex(indexed, now))
	}
	return ListResult{Items: items, Total: result.Total, Grouped: normalizeGrouped(result.Grouped)}, true, nil
}

func (s *Service) List(ctx context.Context, query Query) (ListResult, error) {
	if result, ok, err := s.indexedList(ctx, query); ok || err != nil {
		return result, err
	}

	limit := normalizedLimit(query.Limit, query.MaxLimit)
	offset := normalizedOffset(query.Offset)
	fetchLimit := offset + limit
	hotSort := isHotSort(query.Sort)

	var grouped map[string]int
	var total int
	if hotSort {
		var err error
		grouped, err = s.groupedForQuery(ctx, query)
		if err != nil {
			return ListResult{}, err
		}
		// Get total for fetching all items
		total = grouped["_total"]
		if total == 0 {
			return ListResult{Items: []Item{}, Total: 0, Grouped: grouped}, nil
		}
		fetchLimit = total
	}

	items := []Item{}
	if s.links != nil && includeLinks(query) {
		links, err := s.links.SearchResources(ctx, repository.LinkSearchParams{
			Type:      query.Type,
			Category:  query.Category,
			AccountID: query.AccountID,
			ChannelID: query.ChannelID,
			Keyword:   query.Keyword,
			Sort:      query.Sort,
			DateFrom:  query.DateFrom,
			DateTo:    query.DateTo,
			Limit:     fetchLimit,
		})
		if err != nil {
			return ListResult{}, err
		}
		for _, link := range links {
			category := link.Category
			if category == "" {
				category = link.Type
			}
			item := Item{
				ID:                "link:" + link.URL,
				Kind:              "link",
				Type:              link.Type,
				Category:          category,
				URL:               link.URL,
				Password:          link.Password,
				Note:              link.Note,
				Title:             firstNonEmpty(link.MediaTitle, link.Note, link.URL),
				SourceSnippet:     link.SourceSnippet,
				MediaTitle:        link.MediaTitle,
				MediaYear:         link.MediaYear,
				MediaSeason:       link.MediaSeason,
				MediaEpisode:      link.MediaEpisode,
				MediaQuality:      link.MediaQuality,
				MediaSize:         link.MediaSize,
				MediaTMDBID:       link.MediaTMDBID,
				MediaCategory:     link.MediaCategory,
				MediaTags:         link.MediaTags,
				Datetime:          link.MessageDate,
				AccountID:         link.AccountID,
				ChannelID:         link.ChannelID,
				TelegramChannelID: link.TelegramChannelID,
				ChannelTitle:      link.ChannelTitle,
				ChannelUsername:   link.ChannelUsername,
				TelegramMessageID: link.TelegramMessageID,
				MessageType:       link.MessageType,
				MediaSummary:      link.MediaSummary,
			}
			item.SetMediaMetadata(link.MediaTitle, link.MediaYear, link.MediaSeason, link.MediaEpisode, link.MediaQuality, link.MediaSize, link.MediaTMDBID, link.MediaCategory, link.MediaTags, link.MediaSummary)
			items = append(items, item)
		}
	}
	if s.files != nil && includeFiles(query) {
		files, err := s.files.SearchResources(ctx, repository.FileSearchParams{
			Query:              query.Keyword,
			Category:           fileCategoryFilter(query),
			ExcludedCategories: excludedFileCategories(query),
			Extension:          query.Extension,
			AccountID:          query.AccountID,
			ChannelID:          query.ChannelID,
			Sort:               query.Sort,
			DateFrom:           query.DateFrom,
			DateTo:             query.DateTo,
			Limit:              fetchLimit,
		})
		if err != nil {
			return ListResult{}, err
		}
		for _, file := range files {
			items = append(items, Item{
				ID:                "file:" + strconv.FormatInt(file.ID, 10),
				Kind:              "file",
				Type:              file.Category,
				Category:          "files",
				TelegramFileID:    file.TelegramFileID,
				FileName:          file.FileName,
				Extension:         file.Extension,
				MimeType:          file.MimeType,
				SizeBytes:         file.SizeBytes,
				Title:             file.FileName,
				Datetime:          file.MessageDate,
				AccountID:         file.AccountID,
				ChannelID:         file.ChannelID,
				TelegramChannelID: file.TelegramChannelID,
				ChannelTitle:      file.ChannelTitle,
				ChannelUsername:   file.ChannelUsername,
				TelegramMessageID: file.TelegramMessageID,
			})
		}
	}
	if err := s.attachScores(ctx, items, time.Now().UTC()); err != nil {
		return ListResult{}, err
	}
	if hotSort {
		sortItemsByHot(items)
	} else {
		SortItemsByQuality(items, query.Keyword)
	}

	if !hotSort {
		var err error
		grouped, err = s.groupedForQuery(ctx, query)
		if err != nil {
			return ListResult{}, err
		}
	}

	// Get accurate total from grouped count
	total = grouped["_total"]
	if total == 0 {
		total = len(items)
	}

	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	if end > len(items) {
		end = len(items)
	}
	return ListResult{Items: items[offset:end], Total: total, Grouped: grouped}, nil
}

func (s *Service) Delete(ctx context.Context, id string) error {
	deleted, err := s.deleteOne(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return sql.ErrNoRows
	}
	return s.RebuildIndex(ctx)
}

func (s *Service) DeleteMany(ctx context.Context, ids []string) (DeleteManyResult, error) {
	result := DeleteManyResult{MissingIDs: []string{}}
	seen := map[string]struct{}{}
	targets := []string{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		if err := validateResourceID(id); err != nil {
			return result, err
		}
		targets = append(targets, id)
	}
	for _, id := range targets {
		deleted, err := s.deleteOne(ctx, id)
		if err != nil {
			return result, err
		}
		if deleted {
			result.Deleted++
		} else {
			result.MissingIDs = append(result.MissingIDs, id)
		}
	}
	if result.Deleted > 0 {
		if err := s.RebuildIndex(ctx); err != nil {
			return result, err
		}
	}
	return result, nil
}

func validateResourceID(id string) error {
	kind, value, err := parseResourceID(id)
	if err != nil {
		return err
	}
	if kind != "file" {
		return nil
	}
	fileID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || fileID <= 0 {
		return ErrInvalidResourceID
	}
	return nil
}

func (s *Service) deleteOne(ctx context.Context, id string) (bool, error) {
	kind, value, err := parseResourceID(id)
	if err != nil {
		return false, err
	}
	switch kind {
	case "link":
		if s.links == nil {
			return false, nil
		}
		affected, err := s.links.DeleteResourceByURL(ctx, value)
		return affected > 0, err
	case "file":
		if s.files == nil {
			return false, nil
		}
		fileID, err := strconv.ParseInt(value, 10, 64)
		if err != nil || fileID <= 0 {
			return false, ErrInvalidResourceID
		}
		affected, err := s.files.DeleteResourceByID(ctx, fileID)
		return affected > 0, err
	default:
		return false, ErrInvalidResourceID
	}
}

func parseResourceID(id string) (string, string, error) {
	switch {
	case strings.HasPrefix(id, "link:"):
		url := strings.TrimPrefix(id, "link:")
		if strings.TrimSpace(url) == "" {
			return "", "", ErrInvalidResourceID
		}
		return "link", url, nil
	case strings.HasPrefix(id, "file:"):
		value := strings.TrimPrefix(id, "file:")
		if strings.TrimSpace(value) == "" {
			return "", "", ErrInvalidResourceID
		}
		return "file", value, nil
	default:
		return "", "", ErrInvalidResourceID
	}
}

func (s *Service) groupedForQuery(ctx context.Context, query Query) (map[string]int, error) {
	// Use simple COUNT queries instead of expensive grouped counts
	// This gives accurate total for pagination without the overhead of category breakdowns
	grouped := defaultGrouped()
	var linkCount, fileCount int

	if s.links != nil && includeLinks(query) {
		count, err := s.links.CountSearch(ctx, repository.LinkSearchParams{
			Type:      query.Type,
			Category:  query.Category,
			AccountID: query.AccountID,
			ChannelID: query.ChannelID,
			Keyword:   query.Keyword,
			DateFrom:  query.DateFrom,
			DateTo:    query.DateTo,
		})
		if err != nil {
			return nil, err
		}
		linkCount = count
	}

	if s.files != nil && includeFiles(query) {
		count, err := s.files.CountResources(ctx, repository.FileSearchParams{
			Query:              query.Keyword,
			Category:           fileCategoryFilter(query),
			ExcludedCategories: excludedFileCategories(query),
			Extension:          query.Extension,
			AccountID:          query.AccountID,
			ChannelID:          query.ChannelID,
			DateFrom:           query.DateFrom,
			DateTo:             query.DateTo,
		})
		if err != nil {
			return nil, err
		}
		fileCount = count
	}

	// Return total count in grouped map (not per-category breakdown)
	// Frontend shows total but not category counts
	grouped["_total"] = linkCount + fileCount
	return grouped, nil
}

func (s *Service) GlobalGrouped(ctx context.Context) (map[string]int, error) {
	if s.stats == nil {
		return s.computeGlobalGrouped(ctx)
	}
	grouped, found, err := s.stats.GetGrouped(ctx)
	if err != nil {
		return nil, err
	}
	if found {
		return normalizeGrouped(grouped), nil
	}
	if err := s.RefreshGlobalGrouped(ctx); err != nil {
		return nil, err
	}
	grouped, _, err = s.stats.GetGrouped(ctx)
	if err != nil {
		return nil, err
	}
	return normalizeGrouped(grouped), nil
}

func (s *Service) RefreshGlobalGrouped(ctx context.Context) error {
	if s.stats == nil {
		return nil
	}
	grouped, err := s.computeGlobalGrouped(ctx)
	if err != nil {
		return err
	}
	return s.stats.SaveGrouped(ctx, grouped)
}

func (s *Service) RefreshMessage(ctx context.Context, messageID int64) error {
	if s.index != nil {
		if err := s.index.RefreshMessage(ctx, messageID); err != nil {
			return err
		}
	}
	return s.RefreshGlobalGrouped(ctx)
}

func (s *Service) RefreshMessages(ctx context.Context, messageIDs []int64) error {
	if s.index != nil {
		if err := s.index.RefreshMessages(ctx, messageIDs); err != nil {
			return err
		}
	}
	return s.RefreshGlobalGrouped(ctx)
}

func (s *Service) DeleteMessageResources(ctx context.Context, messageID int64) error {
	if s.index != nil {
		if err := s.index.DeleteMessage(ctx, messageID); err != nil {
			return err
		}
	}
	return s.RefreshGlobalGrouped(ctx)
}

func (s *Service) RebuildIndex(ctx context.Context) error {
	if s.index == nil {
		return nil
	}
	if err := s.index.Rebuild(ctx); err != nil {
		return err
	}
	return s.RefreshGlobalGrouped(ctx)
}

func (s *Service) IndexStats(ctx context.Context) (model.ResourceIndexStats, error) {
	if s.index == nil {
		return model.ResourceIndexStats{}, nil
	}
	return s.index.Stats(ctx)
}

func (s *Service) computeGlobalGrouped(ctx context.Context) (map[string]int, error) {
	grouped := defaultGrouped()
	var linkCount, fileCount int

	if s.links != nil {
		count, err := s.links.CountSearch(ctx, repository.LinkSearchParams{})
		if err != nil {
			return nil, err
		}
		linkCount = count
	}
	if s.files != nil {
		count, err := s.files.CountResources(ctx, repository.FileSearchParams{
			ExcludedCategories: defaultExcludedFileCategories(),
		})
		if err != nil {
			return nil, err
		}
		fileCount = count
	}

	grouped["_total"] = linkCount + fileCount
	return grouped, nil
}

func (s *Service) ResourceTypeStats(ctx context.Context) (map[string]int, error) {
	grouped := defaultGrouped()
	var total int

	if s.links != nil {
		linkGrouped, err := s.links.CountByResourceCategory(ctx, repository.LinkSearchParams{})
		if err != nil {
			return nil, err
		}
		for category, count := range linkGrouped {
			grouped[category] = count
			total += count
		}
	}
	if s.files != nil {
		count, err := s.files.CountResources(ctx, repository.FileSearchParams{
			ExcludedCategories: defaultExcludedFileCategories(),
		})
		if err != nil {
			return nil, err
		}
		grouped["files"] = count
		total += count
	}

	grouped["_total"] = total
	return grouped, nil
}

func normalizeGrouped(grouped map[string]int) map[string]int {
	out := defaultGrouped()
	for category, count := range grouped {
		out[category] = count
	}
	return out
}

func defaultGrouped() map[string]int {
	return map[string]int{"cloud_drive": 0, "magnet": 0, "ed2k": 0, "http": 0, "files": 0}
}

func groupedTotal(grouped map[string]int) int {
	total := 0
	for _, count := range grouped {
		total += count
	}
	return total
}

func normalizedLimit(limit int, maxLimit int) int {
	if limit <= 0 {
		return 50
	}
	if maxLimit <= 0 {
		maxLimit = 200
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
}

func normalizedOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func includeLinks(query Query) bool {
	return query.Type != "files" && !isFileResourceCategory(query.Type) && query.Category != "files" && !isFileResourceCategory(query.Category)
}

func includeFiles(query Query) bool {
	return (query.Type == "" || query.Type == "files" || isFileResourceCategory(query.Type)) && (query.Category == "" || query.Category == "files" || isFileResourceCategory(query.Category))
}

func fileCategoryFilter(query Query) string {
	if isFileResourceCategory(query.Type) {
		return query.Type
	}
	if query.Category == "files" {
		return ""
	}
	return query.Category
}

func excludedFileCategories(query Query) []string {
	if fileCategoryFilter(query) != "" {
		return nil
	}
	return defaultExcludedFileCategories()
}

func defaultExcludedFileCategories() []string {
	return []string{"image"}
}

func isFileResourceCategory(category string) bool {
	switch category {
	case "image", "video", "audio", "document", "ebook", "archive", "software", "file":
		return true
	default:
		return false
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type resourceScoreStats struct {
	SourceChannelCount int
	MessageCount       int
	ProviderCount      int
}

func isHotSort(sort string) bool {
	return sort == "hot"
}

func (s *Service) attachScores(ctx context.Context, items []Item, now time.Time) error {
	statsByURL := map[string]repository.LinkResourceStats{}
	if s.links != nil {
		urls := make([]string, 0, len(items))
		for _, item := range items {
			if item.Kind == "link" && item.URL != "" {
				urls = append(urls, item.URL)
			}
		}
		if len(urls) > 0 {
			var err error
			statsByURL, err = s.links.ResourceStatsByURL(ctx, urls)
			if err != nil {
				return err
			}
		}
	}

	for i := range items {
		stats := resourceScoreStats{SourceChannelCount: 1, MessageCount: 1, ProviderCount: 1}
		if items[i].Kind == "link" {
			if linkStats, ok := statsByURL[items[i].URL]; ok {
				stats = resourceScoreStats{
					SourceChannelCount: positiveOrOne(linkStats.SourceChannelCount),
					MessageCount:       positiveOrOne(linkStats.MessageCount),
					ProviderCount:      positiveOrOne(linkStats.ProviderCount),
				}
			}
		}
		items[i].Score, items[i].ScoreExplain = itemScore(items[i], stats, now)
	}
	return nil
}

func itemScore(item Item, stats resourceScoreStats, now time.Time) (int, ScoreExplain) {
	explain := ScoreExplain{
		SourceChannelCount: positiveOrOne(stats.SourceChannelCount),
		MessageCount:       positiveOrOne(stats.MessageCount),
		ProviderCount:      positiveOrOne(stats.ProviderCount),
		RecencyScore:       recencyScore(item.Datetime, now),
		TypeScore:          resourceCategoryScore(item),
		MetadataScore: searchrank.MetadataScore(
			item.MediaTitle,
			item.MediaYear,
			item.MediaSeason,
			item.MediaEpisode,
			item.MediaQuality,
			item.MediaSize,
			item.MediaTMDBID,
			item.MediaCategory,
			item.MediaTags,
		),
	}
	score := explain.SourceChannelCount*10 +
		explain.MessageCount*3 +
		explain.ProviderCount*6 +
		explain.RecencyScore +
		explain.TypeScore +
		explain.MetadataScore
	return score, explain
}

func positiveOrOne(value int) int {
	if value <= 0 {
		return 1
	}
	return value
}

func recencyScore(publishedAt time.Time, now time.Time) int {
	if publishedAt.IsZero() {
		return 0
	}
	publishedAt = publishedAt.UTC()
	now = now.UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	switch {
	case !publishedAt.Before(today):
		return 30
	case !publishedAt.Before(now.AddDate(0, 0, -7)):
		return 20
	case !publishedAt.Before(now.AddDate(0, 0, -30)):
		return 10
	default:
		return 0
	}
}

func sortItemsByHot(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if !items[i].Datetime.Equal(items[j].Datetime) {
			return items[i].Datetime.After(items[j].Datetime)
		}
		if leftRank, rightRank := resourceKindRank(items[i].Kind), resourceKindRank(items[j].Kind); leftRank != rightRank {
			return leftRank < rightRank
		}
		return items[i].ID < items[j].ID
	})
}

func SortItemsByQuality(items []Item, keyword string) {
	if strings.TrimSpace(keyword) == "" {
		sortItemsByDate(items)
		return
	}
	sort.SliceStable(items, func(i, j int) bool {
		leftScore := ItemQualityScore(items[i], keyword)
		rightScore := ItemQualityScore(items[j], keyword)
		if leftScore != rightScore {
			return leftScore > rightScore
		}
		if !items[i].Datetime.Equal(items[j].Datetime) {
			return items[i].Datetime.After(items[j].Datetime)
		}
		if leftRank, rightRank := resourceKindRank(items[i].Kind), resourceKindRank(items[j].Kind); leftRank != rightRank {
			return leftRank < rightRank
		}
		return items[i].ID < items[j].ID
	})
}

func sortItemsByDate(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].Datetime.Equal(items[j].Datetime) {
			return items[i].Datetime.After(items[j].Datetime)
		}
		if leftRank, rightRank := resourceKindRank(items[i].Kind), resourceKindRank(items[j].Kind); leftRank != rightRank {
			return leftRank < rightRank
		}
		return items[i].ID < items[j].ID
	})
}

func ItemQualityScore(item Item, keyword string) int {
	return searchrank.TextMatchScore(keyword, item.MediaTitle, item.Title, item.Note, item.FileName, item.MediaTags, item.SourceSnippet, item.URL) +
		searchrank.TitleMarkerScore(item.MediaTitle, item.Title, item.Note, item.FileName) +
		searchrank.MetadataScore(item.MediaTitle, item.MediaYear, item.MediaSeason, item.MediaEpisode, item.MediaQuality, item.MediaSize, item.MediaTMDBID, item.MediaCategory, item.MediaTags) +
		resourceCategoryScore(item) +
		resourcePasswordScore(item.Password)
}

func resourceCategoryScore(item Item) int {
	switch item.Category {
	case "cloud_drive":
		return 90 + providerScore(item.Type)
	case "video":
		return 75
	case "files":
		return 55
	case "magnet":
		return 45
	case "ed2k":
		return 40
	case "http":
		return 15
	default:
		if item.Kind == "file" {
			return 50
		}
		return providerScore(item.Type)
	}
}

func providerScore(typ string) int {
	switch typ {
	case "quark", "aliyun", "baidu", "115", "uc", "xunlei", "tianyi", "mobile", "123", "pikpak", "guangya":
		return 35
	default:
		return 0
	}
}

func resourcePasswordScore(password string) int {
	if strings.TrimSpace(password) == "" {
		return 0
	}
	return 8
}

func resourceKindRank(kind string) int {
	if kind == "link" {
		return 0
	}
	return 1
}
