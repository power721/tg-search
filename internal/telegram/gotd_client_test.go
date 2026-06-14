package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
)

func TestListDialogChannelsCollectsAllDialogPages(t *testing.T) {
	ctx := context.Background()
	pages := [][]tg.DialogClass{
		{testDialog(1001), testDialog(1002)},
		{testDialog(2001)},
		nil,
	}
	calls := 0

	query := dialogs.QueryFunc(func(context.Context, dialogs.Request) (tg.MessagesDialogsClass, error) {
		if calls >= len(pages) {
			t.Fatalf("unexpected dialogs request %d", calls+1)
		}
		page := pages[calls]
		calls++
		return testDialogsResult(page, 3), nil
	})
	iter := dialogs.NewIterator(query, 2)
	channels, err := listDialogChannels(ctx, iter)
	if err != nil {
		t.Fatalf("listDialogChannels returned error: %v", err)
	}

	if calls != 3 {
		t.Fatalf("dialogs requests = %d, want 3 pages", calls)
	}
	if len(channels) != 3 {
		t.Fatalf("channels length = %d, want 3: %+v", len(channels), channels)
	}
	if channels[2].TelegramChannelID != 2001 {
		t.Fatalf("last channel id = %d, want second page channel 2001", channels[2].TelegramChannelID)
	}
	if channels[2].Username != "" {
		t.Fatalf("private channel username = %q, want empty", channels[2].Username)
	}
}

func TestChannelFromTGExtractsPhotoID(t *testing.T) {
	tests := []struct {
		name            string
		channel         *tg.Channel
		wantPhotoID     int64
		wantAvatarState string
	}{
		{
			name: "channel with photo",
			channel: &tg.Channel{
				ID:         123456,
				AccessHash: 789012,
				Title:      "Test Channel",
				Photo:      &tg.ChatPhoto{PhotoID: 987654321},
			},
			wantPhotoID:     987654321,
			wantAvatarState: "available",
		},
		{
			name: "channel with empty photo",
			channel: &tg.Channel{
				ID:         123457,
				AccessHash: 789013,
				Title:      "Empty Photo Channel",
				Photo:      &tg.ChatPhotoEmpty{},
			},
			wantPhotoID:     0,
			wantAvatarState: "none",
		},
		{
			name: "channel with nil photo",
			channel: &tg.Channel{
				ID:         123458,
				AccessHash: 789014,
				Title:      "No Photo Channel",
				Photo:      nil,
			},
			wantPhotoID:     0,
			wantAvatarState: "none",
		},
		{
			name: "channel with zero photoID",
			channel: &tg.Channel{
				ID:         123459,
				AccessHash: 789015,
				Title:      "Zero PhotoID Channel",
				Photo:      &tg.ChatPhoto{PhotoID: 0},
			},
			wantPhotoID:     0,
			wantAvatarState: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := channelFromTG(tt.channel)
			if result.PhotoID != tt.wantPhotoID {
				t.Errorf("PhotoID = %d, want %d", result.PhotoID, tt.wantPhotoID)
			}
			if result.AvatarState != tt.wantAvatarState {
				t.Errorf("AvatarState = %q, want %q", result.AvatarState, tt.wantAvatarState)
			}
		})
	}
}

func TestApplyFullChannelMetadata(t *testing.T) {
	channel := Channel{
		TelegramChannelID: 1001,
		Title:             "Private Channel",
		MemberCount:       12,
		Description:       "dialog description",
	}
	full := &tg.ChannelFull{
		ID:    1001,
		About: "full description",
	}
	full.SetParticipantsCount(1234)

	got := applyFullChannelMetadata(channel, full)

	if got.Description != "full description" {
		t.Fatalf("description = %q, want full description", got.Description)
	}
	if got.MemberCount != 1234 {
		t.Fatalf("member_count = %d, want 1234", got.MemberCount)
	}
}

func TestProfileFromUserIncludesPhone(t *testing.T) {
	user := &tg.User{ID: 42}
	user.SetFirstName("Ada")
	user.SetLastName("Lovelace")
	user.SetUsername("ada")
	user.SetPhone("15550000000")

	profile := profileFromUser(user)

	if profile.TelegramUserID != 42 || profile.Phone != "+15550000000" || profile.Username != "ada" {
		t.Fatalf("profile = %+v, want id, normalized phone, username", profile)
	}
}

func testDialog(channelID int64) tg.DialogClass {
	return &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: channelID}}
}

func testDialogsResult(items []tg.DialogClass, count int) tg.MessagesDialogsClass {
	messages := make([]tg.MessageClass, 0, len(items))
	chats := make([]tg.ChatClass, 0, len(items))
	for i, item := range items {
		channelID := item.GetPeer().(*tg.PeerChannel).ChannelID
		messages = append(messages, &tg.Message{
			ID:     i + 1,
			Date:   1000 - i,
			PeerID: item.GetPeer(),
		})
		chats = append(chats, &tg.Channel{
			ID:         channelID,
			AccessHash: channelID + 5000,
			Photo:      &tg.ChatPhoto{PhotoID: channelID + 9000},
			Title:      "Private Channel",
		})
	}
	return &tg.MessagesDialogsSlice{
		Dialogs:  items,
		Messages: messages,
		Chats:    chats,
		Count:    count,
	}
}

func TestConvertMessageIncludesHiddenAndButtonURLs(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	message := &tg.Message{
		ID:      10,
		Date:    int(now.Unix()),
		PeerID:  &tg.PeerChannel{ChannelID: 200},
		Message: "🔗 链接: 115网盘",
	}
	message.SetEntities([]tg.MessageEntityClass{
		&tg.MessageEntityTextURL{
			Offset: 4,
			Length: 5,
			URL:    "https://115cdn.com/s/sws61os33xj?password=re39",
		},
	})
	message.SetReplyMarkup(&tg.ReplyInlineMarkup{
		Rows: []tg.KeyboardButtonRow{{
			Buttons: []tg.KeyboardButtonClass{
				&tg.KeyboardButtonURL{Text: "打开", URL: "https://pan.quark.cn/s/hidden"},
			},
		}},
	})

	converted := convertMessage(message)

	for _, want := range []string{
		"🔗 链接: 115网盘",
		"https://115cdn.com/s/sws61os33xj?password=re39",
		"https://pan.quark.cn/s/hidden",
	} {
		if !strings.Contains(converted.Text, want) {
			t.Fatalf("converted text %q missing %q", converted.Text, want)
		}
		if !strings.Contains(converted.RawJSON, want) {
			t.Fatalf("raw json %q missing %q", converted.RawJSON, want)
		}
	}
}

func TestConvertMessageExtractsDocumentFileMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	media := &tg.MessageMediaDocument{}
	media.SetDocument(&tg.Document{
		ID:       42,
		MimeType: "application/pdf",
		Size:     12345,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeFilename{FileName: "guide.pdf"},
		},
	})
	message := &tg.Message{
		ID:      10,
		Date:    int(now.Unix()),
		PeerID:  &tg.PeerChannel{ChannelID: 200},
		Message: "document",
	}
	message.SetMedia(media)

	converted := convertMessage(message)

	if len(converted.Files) != 1 {
		t.Fatalf("files = %+v, want one file", converted.Files)
	}
	file := converted.Files[0]
	if file.TelegramFileID != 42 || file.FileName != "guide.pdf" || file.Extension != ".pdf" || file.MimeType != "application/pdf" || file.SizeBytes != 12345 {
		t.Fatalf("file = %+v, want guide.pdf metadata", file)
	}
}

func TestConvertMessageClassifiesPhotoMedia(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	message := &tg.Message{
		ID:      10,
		Date:    int(now.Unix()),
		PeerID:  &tg.PeerChannel{ChannelID: 200},
		Message: "poster",
	}
	message.SetMedia(&tg.MessageMediaPhoto{
		Photo: &tg.Photo{ID: 42},
	})

	converted := convertMessage(message)

	if converted.MessageType != "photo" || converted.MediaSummary != "photo" {
		t.Fatalf("media metadata = %q/%q, want photo/photo", converted.MessageType, converted.MediaSummary)
	}
	if len(converted.Files) != 1 {
		t.Fatalf("files = %+v, want one photo file", converted.Files)
	}
	file := converted.Files[0]
	if file.TelegramFileID != 42 || file.FileName != "telegram-photo-42.jpg" || file.Extension != ".jpg" || file.MimeType != "image/jpeg" || file.Category != "image" {
		t.Fatalf("photo file = %+v, want synthetic jpeg metadata", file)
	}
}

func TestConvertMessageExtractsVideoFileMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	media := &tg.MessageMediaDocument{}
	media.SetDocument(&tg.Document{
		ID:       42,
		MimeType: "video/mp4",
		Size:     12345,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeVideo{W: 1920, H: 1080, Duration: 60},
		},
	})
	message := &tg.Message{
		ID:      10,
		Date:    int(now.Unix()),
		PeerID:  &tg.PeerChannel{ChannelID: 200},
		Message: "video",
	}
	message.SetMedia(media)

	converted := convertMessage(message)

	if converted.MessageType != "video" || converted.MediaSummary != "video/mp4" {
		t.Fatalf("media metadata = %q/%q, want video/video/mp4", converted.MessageType, converted.MediaSummary)
	}
	if len(converted.Files) != 1 {
		t.Fatalf("files = %+v, want one video file", converted.Files)
	}
	file := converted.Files[0]
	if file.TelegramFileID != 42 || file.FileName != "telegram-video-42.mp4" || file.Extension != ".mp4" || file.MimeType != "video/mp4" || file.SizeBytes != 12345 || file.Category != "video" {
		t.Fatalf("video file = %+v, want mp4 metadata", file)
	}
}

func TestConvertMessageExtractsAudioFileMetadata(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	media := &tg.MessageMediaDocument{}
	media.SetDocument(&tg.Document{
		ID:       42,
		MimeType: "audio/mpeg",
		Size:     12345,
		Attributes: []tg.DocumentAttributeClass{
			&tg.DocumentAttributeAudio{Duration: 180},
		},
	})
	message := &tg.Message{
		ID:      10,
		Date:    int(now.Unix()),
		PeerID:  &tg.PeerChannel{ChannelID: 200},
		Message: "audio",
	}
	message.SetMedia(media)

	converted := convertMessage(message)

	if converted.MessageType != "audio" || converted.MediaSummary != "audio/mpeg" {
		t.Fatalf("media metadata = %q/%q, want audio/audio/mpeg", converted.MessageType, converted.MediaSummary)
	}
	if len(converted.Files) != 1 {
		t.Fatalf("files = %+v, want one audio file", converted.Files)
	}
	file := converted.Files[0]
	if file.TelegramFileID != 42 || file.FileName != "telegram-audio-42.mp3" || file.Extension != ".mp3" || file.MimeType != "audio/mpeg" || file.SizeBytes != 12345 || file.Category != "audio" {
		t.Fatalf("audio file = %+v, want mp3 metadata", file)
	}
}

func TestConvertMessageClassifiesWebPagePhotoMedia(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	webPage := &tg.WebPage{ID: 7, URL: "https://pan.quark.cn/s/abc"}
	webPage.SetPhoto(&tg.Photo{ID: 42})
	message := &tg.Message{
		ID:      10,
		Date:    int(now.Unix()),
		PeerID:  &tg.PeerChannel{ChannelID: 200},
		Message: "https://pan.quark.cn/s/abc",
	}
	message.SetMedia(&tg.MessageMediaWebPage{Webpage: webPage})

	converted := convertMessage(message)

	if converted.MessageType != "photo" || converted.MediaSummary != "webpage_photo" {
		t.Fatalf("media metadata = %q/%q, want photo/webpage_photo", converted.MessageType, converted.MediaSummary)
	}
}
