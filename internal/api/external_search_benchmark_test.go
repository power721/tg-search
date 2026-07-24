package api

import (
	"context"
	"strconv"
	"testing"
	"time"

	"tg-search/internal/apikey"
	"tg-search/internal/model"
	"tg-search/internal/resource"
)

func BenchmarkAttachMediaToExternalResourceItems30(b *testing.B) {
	ctx := context.Background()
	deps := testDeps(b)
	deps.APIKeyService = apikey.NewService(deps.APIKeys, deps.Settings)
	h := handlers{deps: deps}

	accountID, err := deps.Accounts.Save(ctx, model.Account{
		Phone:    "+10000000000",
		Username: "benchmark",
		Status:   model.AccountStatusOnline,
	})
	if err != nil {
		b.Fatal(err)
	}
	channelID, err := deps.Channels.Save(ctx, model.Channel{
		AccountID:         accountID,
		TelegramChannelID: 1,
		Title:             "Benchmark",
		Type:              model.ChannelTypeChannel,
	})
	if err != nil {
		b.Fatal(err)
	}

	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	messages := make([]model.Message, 30)
	for i := range messages {
		messages[i] = model.Message{
			AccountID:         accountID,
			ChannelID:         channelID,
			TelegramMessageID: int64(i + 1),
			Text:              "benchmark item " + strconv.Itoa(i),
			RawJSON:           "{}",
			Date:              now.Add(-time.Duration(i) * time.Minute),
		}
	}
	stored, err := deps.Messages.SaveBatch(ctx, messages)
	if err != nil {
		b.Fatal(err)
	}

	items := make([]resource.Item, 0, len(stored))
	for i, message := range stored {
		if _, err := deps.Files.SaveBatch(ctx, message.ID, []model.File{{
			TelegramFileID: int64(10000 + i),
			FileName:       "poster-" + strconv.Itoa(i) + ".jpg",
			Extension:      ".jpg",
			MimeType:       "image/jpeg",
			Category:       "image",
		}}); err != nil {
			b.Fatal(err)
		}
		items = append(items, resource.Item{
			ID:                "link:" + strconv.Itoa(i),
			Kind:              "link",
			ChannelID:         channelID,
			TelegramMessageID: message.TelegramMessageID,
			MessageType:       "photo",
		})
	}
	if _, err := deps.APIKeyService.EnsureActive(ctx); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		itemsCopy := append([]resource.Item(nil), items...)
		if _, err := h.attachMediaToExternalResourceItems(ctx, itemsCopy, true, true); err != nil {
			b.Fatal(err)
		}
	}
}
