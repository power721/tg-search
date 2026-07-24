package update

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tg-search/internal/config"
	dbpkg "tg-search/internal/db"
	"tg-search/internal/link"
	"tg-search/internal/messagefilter"
	"tg-search/internal/model"
	"tg-search/internal/notification"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	taskpkg "tg-search/internal/task"
)

type ProcessorOptions struct {
	DB                   *sql.DB
	Channels             *repository.ChannelRepository
	Messages             *repository.MessageRepository
	Links                *repository.LinkRepository
	Files                *repository.FileRepository
	Resources            *resource.Service
	Notifications        *notification.Service
	Cursors              *repository.SyncCursorRepository
	Tasks                *taskpkg.Service
	Extractor            *link.Extractor
	Filter               *messagefilter.Filter
	Settings             *repository.SettingsRepository
	RuntimeConfig        config.Config
	AIMediaMetadataTasks taskEnqueuer
}

type taskEnqueuer interface {
	Enqueue(context.Context, string, any) (model.Task, error)
}

type Processor struct {
	db                   *sql.DB
	channels             *repository.ChannelRepository
	messages             *repository.MessageRepository
	links                *repository.LinkRepository
	files                *repository.FileRepository
	resources            *resource.Service
	notifications        *notification.Service
	cursors              *repository.SyncCursorRepository
	tasks                *taskpkg.Service
	extractor            *link.Extractor
	filter               *messagefilter.Filter
	settings             *repository.SettingsRepository
	runtimeConfig        config.Config
	aiMediaMetadataTasks taskEnqueuer
}

func NewProcessor(opts ProcessorOptions) *Processor {
	if opts.Extractor == nil {
		opts.Extractor = link.NewExtractor()
	}
	return &Processor{
		db:                   opts.DB,
		channels:             opts.Channels,
		messages:             opts.Messages,
		links:                opts.Links,
		files:                opts.Files,
		resources:            opts.Resources,
		notifications:        opts.Notifications,
		cursors:              opts.Cursors,
		tasks:                opts.Tasks,
		extractor:            opts.Extractor,
		filter:               opts.Filter,
		settings:             opts.Settings,
		runtimeConfig:        opts.RuntimeConfig,
		aiMediaMetadataTasks: opts.AIMediaMetadataTasks,
	}
}

func (p *Processor) Process(ctx context.Context, event Event) error {
	channel, err := p.channels.FindByTelegramID(ctx, event.AccountID, event.TelegramChannelID)
	if err != nil {
		return fmt.Errorf("find update channel: %w", err)
	}
	if !channel.ListenEnabled {
		return nil
	}

	switch event.Type {
	case EventNewMessage, EventEditMessage:
		if err := p.enqueueGapRecovery(ctx, channel, event); err != nil {
			return err
		}
		if err := p.storeMessage(ctx, channel, event); err != nil {
			return err
		}
		if event.Type == EventNewMessage {
			return p.advanceHistoryCursor(ctx, channel, event)
		}
		return nil
	case EventDeleteMessage:
		return p.deleteMessage(ctx, channel, event)
	default:
		return fmt.Errorf("unsupported update event type %q", event.Type)
	}
}

func (p *Processor) enqueueGapRecovery(ctx context.Context, channel model.Channel, event Event) error {
	if p.cursors == nil || p.tasks == nil || event.Type != EventNewMessage || event.MessageID <= 0 {
		return nil
	}
	cursor, err := p.cursors.Find(ctx, event.AccountID, channel.ID, "history")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load history cursor for gap detection: %w", err)
	}
	if cursor.LastMessageID <= 0 || event.MessageID <= cursor.LastMessageID+1 {
		return nil
	}
	_, err = p.tasks.EnqueueGapRecovery(ctx, taskpkg.GapRecoveryPayload{
		AccountID:         event.AccountID,
		ChannelID:         channel.ID,
		FromMessageID:     cursor.LastMessageID + 1,
		ToMessageID:       event.MessageID - 1,
		TriggerMessageID:  event.MessageID,
		TelegramChannelID: event.TelegramChannelID,
	})
	if err != nil {
		return fmt.Errorf("enqueue gap recovery task: %w", err)
	}
	return nil
}

func (p *Processor) storeMessage(ctx context.Context, channel model.Channel, event Event) error {
	extracted := p.extractor.Extract(event.Text)
	if p.filter != nil {
		result, err := p.filter.Apply(ctx, messagefilter.Request{
			ChannelID:      channel.ID,
			Text:           event.Text,
			MessageType:    event.MessageType,
			Files:          event.Files,
			RequireRule:    true,
			RequireEnabled: true,
		})
		if err != nil {
			return err
		}
		if !result.Keep {
			if event.Type == EventEditMessage && result.RuleApplied {
				if err := p.messages.MarkDeleted(ctx, channel.ID, event.MessageID); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
				return p.refreshResourceStats(ctx)
			}
			return nil
		}
		extracted = result.Links
	}
	createdResources := []resource.Item{}
	var aiMessageID int64
	var storedMessageID int64
	if err := dbpkg.WithTx(ctx, p.db, func(tx *sql.Tx) error {
		date := event.Date
		if date.IsZero() {
			date = event.EditDateOrNow()
		}
		stored, err := p.messages.SaveBatchTx(ctx, tx, []model.Message{{
			AccountID:         event.AccountID,
			ChannelID:         channel.ID,
			TelegramMessageID: event.MessageID,
			SenderID:          event.SenderID,
			MessageType:       event.MessageType,
			MediaSummary:      event.MediaSummary,
			Text:              event.Text,
			RawJSON:           event.RawJSON,
			Date:              date,
			EditDate:          event.EditDate,
			Files:             event.Files,
		}})
		if err != nil {
			return err
		}
		storedMessageID = stored[0].ID
		savedLinks, err := p.links.ReplaceForMessageTx(ctx, tx, stored[0].ID, extracted)
		if err != nil {
			return err
		}
		if hasCloudDriveLinks(savedLinks) {
			aiMessageID = stored[0].ID
		}
		if event.Type == EventNewMessage {
			createdResources = append(createdResources, updateResourceItemsFromMessage(channel, stored[0], extracted, stored[0].Files)...)
		}
		if p.files == nil {
			return nil
		}
		_, err = p.files.ReplaceForMessageTx(ctx, tx, stored[0].ID, stored[0].Files)
		return err
	}); err != nil {
		return err
	}
	p.enqueueCreatedResources(ctx, createdResources)
	p.enqueueAIMediaMetadataTask(ctx, aiMessageID)
	if p.resources != nil && storedMessageID > 0 {
		return p.resources.RefreshMessage(ctx, storedMessageID)
	}
	return p.refreshResourceStats(ctx)
}

func (p *Processor) enqueueAIMediaMetadataTask(ctx context.Context, messageID int64) {
	if p.aiMediaMetadataTasks == nil || messageID <= 0 || !p.aiMediaMetadataEnabled(ctx) {
		return
	}
	if _, err := p.aiMediaMetadataTasks.Enqueue(ctx, model.TaskTypeAIMediaMetadata, taskpkg.AIMediaMetadataPayload{MessageID: messageID}); err != nil {
		return
	}
}

func (p *Processor) aiMediaMetadataEnabled(ctx context.Context) bool {
	if p.settings == nil {
		return false
	}
	settings, err := p.settings.LoadRuntimeSettings(ctx, p.runtimeConfig)
	if err != nil {
		return false
	}
	return settings.AI.MediaMetadata.Enabled
}

func (p *Processor) enqueueCreatedResources(ctx context.Context, items []resource.Item) {
	if p.notifications == nil {
		return
	}
	for _, item := range items {
		_, _ = p.notifications.EnqueueResourceCreated(ctx, item)
	}
}

func updateResourceItemsFromMessage(channel model.Channel, msg model.Message, links []model.Link, files []model.File) []resource.Item {
	items := make([]resource.Item, 0, len(links)+len(files))
	for _, link := range links {
		category := link.Category
		if category == "" {
			category = updateResourceCategoryFromLink(link)
		}
		title := link.Note
		if title == "" {
			title = link.URL
		}
		items = append(items, resource.Item{
			ID:                "link:" + link.URL,
			Kind:              "link",
			Type:              link.Type,
			Category:          category,
			URL:               link.URL,
			Password:          link.Password,
			Note:              link.Note,
			Title:             title,
			SourceSnippet:     link.SourceSnippet,
			Datetime:          msg.Date,
			AccountID:         msg.AccountID,
			ChannelID:         channel.ID,
			TelegramChannelID: channel.TelegramChannelID,
			ChannelTitle:      channel.Title,
			ChannelUsername:   channel.Username,
			TelegramMessageID: msg.TelegramMessageID,
			MessageType:       msg.MessageType,
			MediaSummary:      msg.MediaSummary,
		})
	}
	for _, file := range files {
		items = append(items, resource.Item{
			ID:                "file:" + file.FileName,
			Kind:              "file",
			Type:              file.Category,
			Category:          "files",
			FileName:          file.FileName,
			Extension:         file.Extension,
			MimeType:          file.MimeType,
			SizeBytes:         file.SizeBytes,
			Title:             file.FileName,
			Datetime:          msg.Date,
			AccountID:         msg.AccountID,
			ChannelID:         channel.ID,
			TelegramChannelID: channel.TelegramChannelID,
			ChannelTitle:      channel.Title,
			ChannelUsername:   channel.Username,
			TelegramMessageID: msg.TelegramMessageID,
			MessageType:       msg.MessageType,
			MediaSummary:      msg.MediaSummary,
		})
	}
	return items
}

func updateResourceCategoryFromLink(link model.Link) string {
	switch link.Type {
	case "magnet":
		return "magnet"
	case "ed2k":
		return "ed2k"
	case "url":
		return "http"
	default:
		return "cloud_drive"
	}
}

func hasCloudDriveLinks(links []model.Link) bool {
	for _, link := range links {
		if link.Category == "cloud_drive" || (link.Category == "" && isCloudDriveLinkType(link.Type)) {
			return true
		}
	}
	return false
}

func isCloudDriveLinkType(typ string) bool {
	switch typ {
	case "quark", "aliyun", "baidu", "115", "uc", "xunlei", "tianyi", "mobile", "123", "pikpak", "guangya":
		return true
	default:
		return false
	}
}

func (p *Processor) deleteMessage(ctx context.Context, channel model.Channel, event Event) error {
	messageID, err := p.messages.FindIDByTelegramMessageID(ctx, channel.ID, event.MessageID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := dbpkg.WithTx(ctx, p.db, func(tx *sql.Tx) error {
		return p.messages.MarkDeletedTx(ctx, tx, channel.ID, event.MessageID)
	}); err != nil {
		return err
	}
	if p.resources != nil && messageID > 0 {
		return p.resources.DeleteMessageResources(ctx, messageID)
	}
	return p.refreshResourceStats(ctx)
}

func (p *Processor) refreshResourceStats(ctx context.Context) error {
	if p.resources == nil {
		return nil
	}
	return p.resources.RefreshGlobalGrouped(ctx)
}

func (p *Processor) advanceHistoryCursor(ctx context.Context, channel model.Channel, event Event) error {
	if p.cursors == nil || event.MessageID <= 0 {
		return nil
	}
	cursor, err := p.cursors.Find(ctx, event.AccountID, channel.ID, "history")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load history cursor for update advance: %w", err)
	}
	if err == nil {
		if cursor.LastMessageID >= event.MessageID {
			return nil
		}
		if event.MessageID > cursor.LastMessageID+1 {
			return nil
		}
	}
	date := event.Date
	if date.IsZero() {
		date = event.EditDateOrNow()
	}
	return p.cursors.Save(ctx, model.SyncCursor{
		AccountID:     event.AccountID,
		ChannelID:     channel.ID,
		CursorType:    "history",
		LastMessageID: event.MessageID,
		Date:          date,
	})
}

func (e Event) EditDateOrNow() time.Time {
	if e.EditDate != nil {
		return *e.EditDate
	}
	return time.Now().UTC()
}
