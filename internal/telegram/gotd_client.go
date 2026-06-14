package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gotd/td/session"
	gotdtelegram "github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/query/dialogs"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"
)

type GotdClient struct {
	credentials CredentialsProvider
	logger      *zap.Logger
	runtime     RuntimeConfig
}

func NewGotdClient(credentials CredentialsProvider, logger *zap.Logger, runtimeConfig ...RuntimeConfig) *GotdClient {
	if logger == nil {
		logger = zap.NewNop()
	}
	if credentials == nil {
		credentials = StaticCredentialsProvider{}
	}
	runtime := DefaultRuntimeConfig()
	if len(runtimeConfig) > 0 {
		runtime = normalizeRuntimeConfig(runtimeConfig[0])
	}
	return &GotdClient{credentials: credentials, logger: logger, runtime: runtime}
}

func (g *GotdClient) SendCode(ctx context.Context, phone string, sessionPath string) (SentCode, error) {
	var result SentCode
	err := g.withClient(ctx, sessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		sent, err := client.Auth().SendCode(ctx, phone, auth.SendCodeOptions{})
		if err != nil {
			return err
		}
		code, ok := sent.(*tg.AuthSentCode)
		if !ok {
			return fmt.Errorf("unexpected send-code result %T", sent)
		}
		result.PhoneCodeHash = code.PhoneCodeHash
		return nil
	})
	return result, err
}

func (g *GotdClient) SignIn(ctx context.Context, phone string, code string, phoneCodeHash string, sessionPath string) (Profile, error) {
	var profile Profile
	err := g.withClient(ctx, sessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		authorization, err := client.Auth().SignIn(ctx, phone, code, phoneCodeHash)
		if err != nil {
			if err == auth.ErrPasswordAuthNeeded {
				return ErrPasswordRequired
			}
			return err
		}
		profile = profileFromAuthorization(authorization)
		return nil
	})
	return profile, err
}

func (g *GotdClient) Password(ctx context.Context, password string, sessionPath string) (Profile, error) {
	var profile Profile
	err := g.withClient(ctx, sessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		authorization, err := client.Auth().Password(ctx, password)
		if err != nil {
			return err
		}
		profile = profileFromAuthorization(authorization)
		return nil
	})
	return profile, err
}

func (g *GotdClient) ListChannels(ctx context.Context, account AccountSession) ([]Channel, error) {
	var out []Channel
	err := g.withClient(ctx, account.SessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		out = append(out, Channel{
			TelegramChannelID: self.ID,
			Title:             "Saved Messages",
			Type:              "saved_messages",
		})

		channels, err := listDialogChannels(ctx, dialogs.NewQueryBuilder(client.API()).GetDialogs().BatchSize(100).Iter())
		if err != nil {
			return err
		}
		channels = g.enrichChannels(ctx, client.API(), channels)
		out = append(out, channels...)
		return nil
	})
	return out, err
}

func (g *GotdClient) Logout(ctx context.Context, account AccountSession) error {
	return g.withClient(ctx, account.SessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		_, err := client.API().AuthLogOut(ctx)
		return err
	})
}

type dialogIterator interface {
	Next(context.Context) bool
	Value() dialogs.Elem
	Err() error
}

func listDialogChannels(ctx context.Context, iter dialogIterator) ([]Channel, error) {
	var out []Channel
	for iter.Next(ctx) {
		value := iter.Value()
		channelID, ok := peerChannelID(value.Dialog.GetPeer())
		if !ok {
			continue
		}
		channel, ok := value.Entities.Channel(channelID)
		if !ok || channel.Left {
			continue
		}
		out = append(out, channelFromTG(channel))
	}
	return out, iter.Err()
}

func peerChannelID(peer tg.PeerClass) (int64, bool) {
	channel, ok := peer.(*tg.PeerChannel)
	if !ok {
		return 0, false
	}
	return channel.ChannelID, true
}

func channelFromTG(channel *tg.Channel) Channel {
	accessHash, _ := channel.GetAccessHash()
	username, _ := channel.GetUsername()
	participants, _ := channel.GetParticipantsCount()
	typ := "channel"
	if channel.Megagroup {
		typ = "supergroup"
	}
	avatarState := "none"
	var photoID int64
	// Use AsNotEmpty() for safer photo extraction, but check for nil first
	if channel.Photo != nil {
		if photo, ok := channel.Photo.AsNotEmpty(); ok && photo.PhotoID > 0 {
			photoID = photo.PhotoID
			avatarState = "available"
		}
	}
	return Channel{
		TelegramChannelID: channel.ID,
		AccessHash:        accessHash,
		Title:             channel.Title,
		Username:          username,
		Type:              typ,
		MemberCount:       int64(participants),
		AvatarState:       avatarState,
		PhotoID:           photoID,
	}
}

func (g *GotdClient) enrichChannels(ctx context.Context, api *tg.Client, channels []Channel) []Channel {
	for i := range channels {
		full, err := api.ChannelsGetFullChannel(ctx, inputChannel(channels[i]))
		if err != nil {
			g.logger.Debug("failed to load full channel metadata", zap.Error(err))
			continue
		}
		channelFull, ok := full.FullChat.(*tg.ChannelFull)
		if !ok {
			continue
		}
		channels[i] = applyFullChannelMetadata(channels[i], channelFull)
		// Extract photo from the updated Chat objects returned alongside FullChat
		for _, chat := range full.Chats {
			if ch, ok := chat.(*tg.Channel); ok && ch.ID == channels[i].TelegramChannelID {
				// Use AsNotEmpty() for safer photo extraction, but check for nil first
				if ch.Photo != nil {
					if photo, ok := ch.Photo.AsNotEmpty(); ok && photo.PhotoID > 0 {
						channels[i].PhotoID = photo.PhotoID
						channels[i].AvatarState = "available"
					}
				}
				break
			}
		}
	}
	return channels
}

func applyFullChannelMetadata(channel Channel, full *tg.ChannelFull) Channel {
	if full == nil {
		return channel
	}
	if full.About != "" {
		channel.Description = full.About
	}
	if participants, ok := full.GetParticipantsCount(); ok && participants > 0 {
		channel.MemberCount = int64(participants)
	}
	return channel
}

func (g *GotdClient) FetchHistory(ctx context.Context, account AccountSession, channel ChannelRef, offsetID int64, limit int) ([]Message, error) {
	var out []Message
	err := g.withClient(ctx, account.SessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		result, err := client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     inputPeer(channel),
			OffsetID: int(offsetID),
			Limit:    limit,
		})
		if err != nil {
			return err
		}
		for _, item := range historyMessages(result) {
			message, ok := item.(*tg.Message)
			if !ok {
				continue
			}
			out = append(out, convertMessage(message))
		}
		return nil
	})
	return out, err
}

func (g *GotdClient) SearchMessages(ctx context.Context, account AccountSession, channel ChannelRef, query string, limit int) ([]Message, error) {
	var out []Message
	err := g.withClient(ctx, account.SessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		result, err := client.API().MessagesSearch(ctx, &tg.MessagesSearchRequest{
			Peer:   inputPeer(channel),
			Q:      query,
			Filter: &tg.InputMessagesFilterEmpty{},
			Limit:  limit,
		})
		if err != nil {
			return err
		}
		for _, item := range historyMessages(result) {
			message, ok := item.(*tg.Message)
			if !ok {
				continue
			}
			out = append(out, convertMessage(message))
		}
		return nil
	})
	return out, err
}

func (g *GotdClient) DownloadChannelAvatar(ctx context.Context, session AccountSession, channelID int64, accessHash int64, photoID int64) (ImageFile, error) {
	var out ImageFile
	err := g.withClient(ctx, session.SessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		var err error
		out, err = downloadChatPhoto(ctx, client.API(), channelID, accessHash, photoID)
		return err
	})
	return out, err
}

func (g *GotdClient) DownloadUserAvatar(ctx context.Context, session AccountSession, userID int64, photoID int64) (ImageFile, error) {
	var out ImageFile
	err := g.withClient(ctx, session.SessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		var err error
		out, err = downloadUserPhoto(ctx, client.API(), userID, photoID)
		return err
	})
	return out, err
}

func (g *GotdClient) GetUserProfile(ctx context.Context, session AccountSession) (Profile, error) {
	var profile Profile
	err := g.withClient(ctx, session.SessionPath, func(ctx context.Context, client *gotdtelegram.Client) error {
		users, err := client.API().UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
		if err != nil {
			return err
		}
		if len(users) == 0 {
			return fmt.Errorf("no user returned from GetUsers")
		}
		user, ok := users[0].(*tg.User)
		if !ok {
			return fmt.Errorf("unexpected user type %T", users[0])
		}
		profile = profileFromUser(user)
		return nil
	})
	return profile, err
}


func (g *GotdClient) withClient(ctx context.Context, sessionPath string, fn func(context.Context, *gotdtelegram.Client) error) error {
	credentials, err := g.credentials.TelegramCredentials(ctx)
	if err != nil {
		return err
	}
	opts, err := BuildOptions(ctx, BuildOptionsInput{
		Runtime:        g.runtime,
		SessionStorage: &session.FileStorage{Path: sessionPath},
		NoUpdates:      true,
	})
	if err != nil {
		return err
	}
	opts.Logger = g.logger
	client := gotdtelegram.NewClient(credentials.APIID, credentials.APIHash, opts)
	return client.Run(ctx, func(ctx context.Context) error {
		return fn(ctx, client)
	})
}

func profileFromAuthorization(authorization *tg.AuthAuthorization) Profile {
	user, ok := authorization.User.(*tg.User)
	if !ok {
		return Profile{}
	}
	return profileFromUser(user)
}

func profileFromUser(user *tg.User) Profile {
	first, _ := user.GetFirstName()
	last, _ := user.GetLastName()
	username, _ := user.GetUsername()
	phone, _ := user.GetPhone()
	if phone != "" && phone[0] != '+' {
		phone = "+" + phone
	}
	var photoID int64
	if photo, ok := user.GetPhoto(); ok {
		if userPhoto, ok := photo.(*tg.UserProfilePhoto); ok {
			photoID = userPhoto.PhotoID
		}
	}
	return Profile{
		TelegramUserID: user.ID,
		Phone:          phone,
		FirstName:      first,
		LastName:       last,
		Username:       username,
		PhotoID:        photoID,
	}
}

func dialogChats(dialogs tg.MessagesDialogsClass) []tg.ChatClass {
	switch d := dialogs.(type) {
	case *tg.MessagesDialogs:
		return d.Chats
	case *tg.MessagesDialogsSlice:
		return d.Chats
	default:
		return nil
	}
}

func historyMessages(messages tg.MessagesMessagesClass) []tg.MessageClass {
	switch m := messages.(type) {
	case *tg.MessagesMessages:
		return m.Messages
	case *tg.MessagesMessagesSlice:
		return m.Messages
	case *tg.MessagesChannelMessages:
		return m.Messages
	default:
		return nil
	}
}

func inputPeer(channel ChannelRef) tg.InputPeerClass {
	if channel.Type == "saved_messages" {
		return &tg.InputPeerSelf{}
	}
	return &tg.InputPeerChannel{
		ChannelID:  channel.TelegramChannelID,
		AccessHash: channel.AccessHash,
	}
}

func inputChannel(channel Channel) tg.InputChannelClass {
	return &tg.InputChannel{
		ChannelID:  channel.TelegramChannelID,
		AccessHash: channel.AccessHash,
	}
}

func convertMessage(message *tg.Message) Message {
	var editDate *time.Time
	if raw, ok := message.GetEditDate(); ok && raw > 0 {
		t := time.Unix(int64(raw), 0).UTC()
		editDate = &t
	}
	indexedText, messageURLs := IndexedMessageText(message)
	messageType, mediaSummary := MessageMediaMetadata(message)
	rawJSON, _ := json.Marshal(map[string]any{
		"id":           message.ID,
		"date":         message.Date,
		"message":      message.Message,
		"message_urls": messageURLs,
	})
	return Message{
		TelegramMessageID: int64(message.ID),
		SenderID:          peerID(message.FromID),
		MessageType:       messageType,
		MediaSummary:      mediaSummary,
		Text:              indexedText,
		RawJSON:           string(rawJSON),
		Date:              time.Unix(int64(message.Date), 0).UTC(),
		EditDate:          editDate,
		Files:             FilesFromMessage(message),
	}
}

func peerID(peer tg.PeerClass) int64 {
	switch p := peer.(type) {
	case *tg.PeerUser:
		return p.UserID
	case *tg.PeerChat:
		return p.ChatID
	case *tg.PeerChannel:
		return p.ChannelID
	default:
		return 0
	}
}
