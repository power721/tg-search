package telegram

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"tg-search/internal/model"
)

var ErrUnavailable = errors.New("telegram client is unavailable")
var ErrPasswordRequired = errors.New("telegram password required")
var ErrCredentialsNotConfigured = errors.New("telegram api settings are not configured")

const (
	DefaultAPIID   = 26375241
	DefaultAPIHash = "70f574f48a016d683c64f2f7a217d04f"
)

const (
	QRLoginStatusPending = "pending"
	QRLoginStatusOnline  = "online"
)

type SentCode struct {
	PhoneCodeHash string
}

type Profile struct {
	TelegramUserID int64
	Phone          string
	FirstName      string
	LastName       string
	Username       string
	PhotoID        int64
}

type QRLoginToken struct {
	URL       string
	ExpiresAt time.Time
}

type QRLoginPollResult struct {
	Status  string
	Token   QRLoginToken
	Profile Profile
}

type QRLoginSession interface {
	Token() QRLoginToken
	Poll(context.Context) (QRLoginPollResult, error)
	Cancel(context.Context) error
}

type AccountSession struct {
	AccountID   int64
	Phone       string
	SessionPath string
}

type Channel struct {
	TelegramChannelID int64
	AccessHash        int64
	Title             string
	Username          string
	Type              string
	MemberCount       int64
	Description       string
	AvatarState       string
	PhotoID           int64
}

type ChannelRef struct {
	TelegramChannelID int64
	AccessHash        int64
	Type              string
}

type MediaChannelRef struct {
	Username          string
	TelegramChannelID int64
	AccessHash        int64
	Type              string
}

type VideoFile struct {
	ID            int64
	AccessHash    int64
	FileReference []byte
	Size          int64
	MIMEType      string
}

type ImageFile struct {
	Data     []byte
	MIMEType string
}

type Message struct {
	TelegramMessageID int64
	SenderID          int64
	MessageType       string
	MediaSummary      string
	Text              string
	RawJSON           string
	Date              time.Time
	EditDate          *time.Time
	Files             []model.File
}

type Client interface {
	SendCode(ctx context.Context, phone string, sessionPath string) (SentCode, error)
	SignIn(ctx context.Context, phone string, code string, phoneCodeHash string, sessionPath string) (Profile, error)
	Password(ctx context.Context, password string, sessionPath string) (Profile, error)
	StartQRLogin(ctx context.Context, sessionPath string) (QRLoginSession, error)
	LoginWithTelethonSession(ctx context.Context, sessionString string, sessionPath string) (Profile, error)
	Logout(ctx context.Context, session AccountSession) error
	ListChannels(ctx context.Context, session AccountSession) ([]Channel, error)
	FetchHistory(ctx context.Context, session AccountSession, channel ChannelRef, offsetID int64, limit int) ([]Message, error)
	SearchMessages(ctx context.Context, session AccountSession, channel ChannelRef, query string, limit int) ([]Message, error)
	VideoFile(ctx context.Context, session AccountSession, channel MediaChannelRef, messageID int) (VideoFile, error)
	StreamVideoRange(ctx context.Context, session AccountSession, channel MediaChannelRef, messageID int, file VideoFile, offset int64, length int64, w io.Writer) error
	DownloadMessageImage(ctx context.Context, session AccountSession, channel MediaChannelRef, messageID int) (ImageFile, error)
	DownloadChannelAvatar(ctx context.Context, session AccountSession, channelID int64, accessHash int64, photoID int64) (ImageFile, error)
	DownloadUserAvatar(ctx context.Context, session AccountSession, userID int64, photoID int64) (ImageFile, error)
	GetUserProfile(ctx context.Context, session AccountSession) (Profile, error)
}

type Credentials struct {
	APIID   int
	APIHash string
}

type CredentialsProvider interface {
	TelegramCredentials(context.Context) (Credentials, error)
}

type StaticCredentialsProvider struct {
	Credentials Credentials
}

func (p StaticCredentialsProvider) TelegramCredentials(context.Context) (Credentials, error) {
	if p.Credentials.APIID <= 0 || p.Credentials.APIHash == "" {
		return Credentials{}, ErrCredentialsNotConfigured
	}
	return p.Credentials, nil
}

type CodeStore struct {
	mu     sync.Mutex
	hashes map[string]string
}

func NewCodeStore() *CodeStore {
	return &CodeStore{hashes: map[string]string{}}
}

func (s *CodeStore) Save(phone string, hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hashes[phone] = hash
}

func (s *CodeStore) Take(phone string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hash, ok := s.hashes[phone]
	if ok {
		delete(s.hashes, phone)
	}
	return hash, ok
}

type NopClient struct{}

func (NopClient) SendCode(context.Context, string, string) (SentCode, error) {
	return SentCode{}, ErrUnavailable
}

func (NopClient) SignIn(context.Context, string, string, string, string) (Profile, error) {
	return Profile{}, ErrUnavailable
}

func (NopClient) Password(context.Context, string, string) (Profile, error) {
	return Profile{}, ErrUnavailable
}

func (NopClient) StartQRLogin(context.Context, string) (QRLoginSession, error) {
	return nil, ErrUnavailable
}

func (NopClient) LoginWithTelethonSession(context.Context, string, string) (Profile, error) {
	return Profile{}, ErrUnavailable
}

func (NopClient) Logout(context.Context, AccountSession) error {
	return ErrUnavailable
}

func (NopClient) ListChannels(context.Context, AccountSession) ([]Channel, error) {
	return nil, ErrUnavailable
}

func (NopClient) FetchHistory(context.Context, AccountSession, ChannelRef, int64, int) ([]Message, error) {
	return nil, ErrUnavailable
}

func (NopClient) SearchMessages(context.Context, AccountSession, ChannelRef, string, int) ([]Message, error) {
	return nil, ErrUnavailable
}

func (NopClient) VideoFile(context.Context, AccountSession, MediaChannelRef, int) (VideoFile, error) {
	return VideoFile{}, ErrUnavailable
}

func (NopClient) StreamVideoRange(context.Context, AccountSession, MediaChannelRef, int, VideoFile, int64, int64, io.Writer) error {
	return ErrUnavailable
}

func (NopClient) DownloadMessageImage(context.Context, AccountSession, MediaChannelRef, int) (ImageFile, error) {
	return ImageFile{}, ErrUnavailable
}

func (NopClient) DownloadChannelAvatar(context.Context, AccountSession, int64, int64, int64) (ImageFile, error) {
	return ImageFile{}, ErrUnavailable
}

func (NopClient) DownloadUserAvatar(context.Context, AccountSession, int64, int64) (ImageFile, error) {
	return ImageFile{}, ErrUnavailable
}

func (NopClient) GetUserProfile(context.Context, AccountSession) (Profile, error) {
	return Profile{}, ErrUnavailable
}
