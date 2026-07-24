package model

import (
	"encoding/json"
	"time"
)

const (
	AccountStatusNew           = "NEW"
	AccountStatusLoginRequired = "LOGIN_REQUIRED"
	AccountStatusSyncing       = "SYNCING"
	AccountStatusOnline        = "ONLINE"
	AccountStatusReconnecting  = "RECONNECTING"
	AccountStatusFloodWait     = "FLOOD_WAIT"
	AccountStatusDisconnected  = "DISCONNECTED"
)

const (
	ChannelTypeChannel       = "channel"
	ChannelTypeSupergroup    = "supergroup"
	ChannelTypeSavedMessages = "saved_messages"
)

const UserRoleAdmin = "admin"

type Account struct {
	ID             int64      `json:"id"`
	Phone          string     `json:"phone"`
	TelegramUserID int64      `json:"telegram_user_id"`
	FirstName      string     `json:"first_name"`
	LastName       string     `json:"last_name"`
	Username       string     `json:"username"`
	PhotoID        int64      `json:"photo_id"`
	Status         string     `json:"status"`
	SessionPath    string     `json:"session_path"`
	LastOnlineAt   *time.Time `json:"last_online_at,omitempty"`
	LastError      string     `json:"last_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type User struct {
	ID           int64      `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	LastLoginAt  *time.Time `json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type AdminSession struct {
	Token     string    `json:"token"`
	UserID    int64     `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type APIKey struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	KeyHash       string     `json:"-"`
	KeyCiphertext string     `json:"-"`
	Prefix        string     `json:"prefix"`
	Enabled       bool       `json:"enabled"`
	UsageCount    int64      `json:"usage_count"`
	LastUsedAt    *time.Time `json:"last_used_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type APIKeyResponse struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Key        string     `json:"key"`
	UsageCount int64      `json:"usage_count"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type SetupStatus struct {
	Complete              bool   `json:"complete"`
	AdminConfigured       bool   `json:"admin_configured"`
	APIKeyConfigured      bool   `json:"api_key_configured"`
	APIKeyStepComplete    bool   `json:"api_key_step_complete"`
	TelegramConfigured    bool   `json:"telegram_configured"`
	TelegramLoginComplete bool   `json:"telegram_login_complete"`
	ListenRulesConfigured bool   `json:"listen_rules_configured"`
	CurrentStep           string `json:"current_step"`
}

type TelegramAPISettings struct {
	AppID   int    `json:"app_id"`
	AppHash string `json:"-"`
}

type TelegramAPISettingsResponse struct {
	Configured bool `json:"configured"`
	AppID      int  `json:"app_id"`
	AppHashSet bool `json:"app_hash_set"`
}

type TelegramBotSettingsResponse struct {
	Enabled      bool   `json:"enabled"`
	Configured   bool   `json:"configured"`
	TokenSet     bool   `json:"token_set"`
	PollInterval string `json:"poll_interval"`
}

type StorageUsage struct {
	DBBytes         int64 `json:"db_bytes"`
	IndexBytes      int64 `json:"index_bytes"`
	MediaCacheBytes int64 `json:"media_cache_bytes"`
	TotalBytes      int64 `json:"total_bytes"`
	MaxDBBytes      int64 `json:"max_db_bytes"`
	MaxMediaBytes   int64 `json:"max_media_bytes"`
	DBOverQuota     bool  `json:"db_over_quota"`
	MediaOverQuota  bool  `json:"media_over_quota"`
}

type VersionInfoResponse struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version,omitempty"`
	LatestURL       string `json:"latest_url,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
}

type SystemInfoResponse struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	GoVersion    string `json:"go_version"`
	CPUCount     int    `json:"cpu_count"`
	Hostname     string `json:"hostname"`
}

type Channel struct {
	ID                  int64      `json:"id"`
	AccountID           int64      `json:"account_id"`
	TelegramChannelID   int64      `json:"telegram_channel_id"`
	AccessHash          int64      `json:"access_hash"`
	Title               string     `json:"title"`
	Username            string     `json:"username"`
	Type                string     `json:"type"`
	MemberCount         int64      `json:"member_count"`
	Description         string     `json:"description"`
	AvatarState         string     `json:"avatar_state"`
	PhotoID             int64      `json:"photo_id"`
	SyncState           string     `json:"sync_state"`
	ListenState         string     `json:"listen_state"`
	HistorySyncEnabled  bool       `json:"history_sync_enabled"`
	SyncProfile         string     `json:"sync_profile"`
	ListenEnabled       bool       `json:"listen_enabled"`
	RemoteSearchAllowed bool       `json:"remote_search_allowed"`
	LastMessageID       int64      `json:"last_message_id"`
	LastSyncTime        *time.Time `json:"last_sync_time,omitempty"`
	WebAccess           *bool      `json:"web_access,omitempty"`
	WebAccessCheckedAt  *time.Time `json:"web_access_checked_at,omitempty"`
	WebAccessError      string     `json:"web_access_error"`
	IndexedMessageCount int64      `json:"indexed_message_count"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type ChannelControl struct {
	HistorySyncEnabled  bool   `json:"history_sync_enabled"`
	SyncProfile         string `json:"sync_profile"`
	ListenEnabled       bool   `json:"listen_enabled"`
	RemoteSearchAllowed bool   `json:"remote_search_allowed"`
}

type WatchRule struct {
	ID                  int64     `json:"id"`
	ChannelID           int64     `json:"channel_id"`
	Enabled             bool      `json:"enabled"`
	Includes            []string  `json:"includes"`
	Excludes            []string  `json:"excludes"`
	MessageTypes        []string  `json:"message_types"`
	LinkTypes           []string  `json:"link_types"`
	IgnoredLinkPatterns []string  `json:"ignored_link_patterns,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

type ListenRules struct {
	Includes            []string `json:"includes"`
	Excludes            []string `json:"excludes"`
	MessageTypes        []string `json:"message_types"`
	LinkTypes           []string `json:"link_types"`
	IgnoredLinkPatterns []string `json:"ignored_link_patterns"`
}

const (
	NotificationEventResourceCreated     = "resource.created"
	NotificationEventResourceUpdated     = "resource.updated"
	NotificationEventTaskCompleted       = "task.completed"
	NotificationEventTaskFailed          = "task.failed"
	NotificationEventAccountOffline      = "account.offline"
	NotificationEventChannelSyncComplete = "channel.sync.completed"
	NotificationEventSavedSearchMatched  = "saved_search.matched"
)

const (
	NotificationTargetSavedSearch = "saved_search"
	NotificationTargetWebhook     = "webhook"
	NotificationTargetTelegram    = "telegram"
)

const (
	NotificationDeliveryPending   = "pending"
	NotificationDeliverySucceeded = "succeeded"
	NotificationDeliveryFailed    = "failed"
)

type SavedSearchFilters struct {
	Type       string   `json:"type,omitempty"`
	Category   string   `json:"category,omitempty"`
	CloudTypes []string `json:"cloud_types,omitempty"`
	AccountID  int64    `json:"account_id,omitempty"`
	ChannelID  int64    `json:"channel_id,omitempty"`
}

type SavedSearch struct {
	ID              int64              `json:"id"`
	Name            string             `json:"name"`
	Keyword         string             `json:"keyword"`
	Filters         SavedSearchFilters `json:"filters"`
	NotifyRSS       bool               `json:"notify_rss"`
	NotifyWebhook   bool               `json:"notify_webhook"`
	NotifyTelegram  bool               `json:"notify_telegram"`
	TelegramChatIDs []int64            `json:"telegram_chat_ids,omitempty"`
	Enabled         bool               `json:"enabled"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

type Webhook struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Secret    string    `json:"-"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type NotificationDelivery struct {
	ID          int64      `json:"id"`
	EventType   string     `json:"event_type"`
	TargetType  string     `json:"target_type"`
	TargetID    int64      `json:"target_id"`
	PayloadJSON string     `json:"payload_json"`
	Status      string     `json:"status"`
	RetryCount  int64      `json:"retry_count"`
	LastError   string     `json:"last_error"`
	NextRunAt   *time.Time `json:"next_run_at,omitempty"`
	DeliveredAt *time.Time `json:"delivered_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type TelegramBotSubscription struct {
	ID            int64     `json:"id"`
	ChatID        int64     `json:"chat_id"`
	SavedSearchID int64     `json:"saved_search_id"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	SavedSearch   string    `json:"saved_search,omitempty"`
	Keyword       string    `json:"keyword,omitempty"`
}

type TelegramBotChat struct {
	ChatID     int64     `json:"chat_id"`
	Title      string    `json:"title"`
	Username   string    `json:"username"`
	FirstName  string    `json:"first_name"`
	LastName   string    `json:"last_name"`
	Type       string    `json:"type"`
	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type ChannelIndexedCounts struct {
	Messages int64 `json:"messages"`
	Links    int64 `json:"links"`
	Files    int64 `json:"files"`
}

type ChannelAnalysis struct {
	Channel       Channel              `json:"channel"`
	Control       ChannelControl       `json:"control"`
	WatchRule     *WatchRule           `json:"watch_rule,omitempty"`
	IndexedCounts ChannelIndexedCounts `json:"indexed_counts"`
}

const RemoteSearchStatusQueued = "queued"

const (
	TaskStatusQueued       = "queued"
	TaskStatusRunning      = "running"
	TaskStatusSucceeded    = "succeeded"
	TaskStatusFailed       = "failed"
	TaskStatusCanceling    = "canceling"
	TaskStatusCanceled     = "canceled"
	TaskStatusPaused       = "paused"
	TaskStatusFloodWait    = "flood_wait"
	TaskStatusReconnecting = "reconnecting"
)

const (
	TaskTypeMetadataSync       = "metadata_sync"
	TaskTypeChannelAnalysis    = "channel_analysis"
	TaskTypeWebAccessDetection = "web_access_detection"
	TaskTypeHistorySync        = "history_sync"
	TaskTypeListenerRecovery   = "listener_recovery"
	TaskTypeRemoteSearch       = "remote_search"
	TaskTypeBackup             = "backup"
	TaskTypeGapRecovery        = "gap_recovery"
	TaskTypeAIMediaMetadata    = "ai_media_metadata"
	TaskTypeRepairMediaTitle   = "repair_media_title"
)

type RemoteSearchTask struct {
	ID        int64     `json:"id"`
	AccountID int64     `json:"account_id"`
	ChannelID int64     `json:"channel_id"`
	Query     string    `json:"query"`
	Status    string    `json:"status"`
	Source    string    `json:"source"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Task struct {
	ID           int64      `json:"id"`
	Type         string     `json:"type"`
	Status       string     `json:"status"`
	Progress     int64      `json:"progress"`
	Total        int64      `json:"total"`
	Message      string     `json:"message"`
	ErrorCode    string     `json:"error_code"`
	ErrorMessage string     `json:"error_message"`
	RetryCount   int64      `json:"retry_count"`
	NextRunAt    *time.Time `json:"next_run_at,omitempty"`
	PayloadJSON  string     `json:"payload_json"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type RemoteSearchItem struct {
	Source            string     `json:"source"`
	AccountID         int64      `json:"account_id"`
	ChannelID         int64      `json:"channel_id"`
	TelegramChannelID int64      `json:"telegram_channel_id"`
	ChannelTitle      string     `json:"channel_title"`
	ChannelUsername   string     `json:"channel_username"`
	TelegramMessageID int64      `json:"telegram_message_id"`
	SenderID          int64      `json:"sender_id"`
	MessageType       string     `json:"message_type,omitempty"`
	MediaSummary      string     `json:"media_summary,omitempty"`
	Text              string     `json:"text"`
	RawJSON           string     `json:"raw_json"`
	Date              time.Time  `json:"date"`
	EditDate          *time.Time `json:"edit_date,omitempty"`
	Media             *MediaURLs `json:"media,omitempty"`

	Files []File `json:"-"`
}

type RemoteSearchResults struct {
	Task  RemoteSearchTask   `json:"task"`
	Items []RemoteSearchItem `json:"items"`
}

type Message struct {
	ID                int64      `json:"id"`
	AccountID         int64      `json:"account_id"`
	ChannelID         int64      `json:"channel_id"`
	TelegramMessageID int64      `json:"telegram_message_id"`
	SenderID          int64      `json:"sender_id"`
	MessageType       string     `json:"message_type"`
	MediaSummary      string     `json:"media_summary"`
	Text              string     `json:"text"`
	RawJSON           string     `json:"raw_json"`
	Date              time.Time  `json:"date"`
	EditDate          *time.Time `json:"edit_date,omitempty"`
	Deleted           bool       `json:"deleted"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	OriginalLinkInputs []Link `json:"-"`
	Files              []File `json:"-"`
}

type SyncCursor struct {
	ID            int64     `json:"id"`
	AccountID     int64     `json:"account_id"`
	ChannelID     int64     `json:"channel_id"`
	CursorType    string    `json:"cursor_type"`
	LastMessageID int64     `json:"last_message_id"`
	PTS           int64     `json:"pts"`
	QTS           int64     `json:"qts"`
	Date          time.Time `json:"date"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type Link struct {
	ID            int64     `json:"id"`
	MessageID     int64     `json:"message_id"`
	Type          string    `json:"type"`
	URL           string    `json:"url"`
	Password      string    `json:"password,omitempty"`
	Note          string    `json:"note,omitempty"`
	SourceSnippet string    `json:"source_snippet,omitempty"`
	Category      string    `json:"category,omitempty"`
	MediaTitle    string    `json:"media_title,omitempty"`
	MediaYear     string    `json:"media_year,omitempty"`
	MediaSeason   string    `json:"media_season,omitempty"`
	MediaEpisode  string    `json:"media_episode,omitempty"`
	MediaQuality  string    `json:"media_quality,omitempty"`
	MediaSize     string    `json:"media_size,omitempty"`
	MediaTMDBID   string    `json:"media_tmdb_id,omitempty"`
	MediaCategory string    `json:"media_category,omitempty"`
	MediaTags     string    `json:"media_tags,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

type File struct {
	ID             int64     `json:"id"`
	MessageID      int64     `json:"message_id"`
	TelegramFileID int64     `json:"telegram_file_id,omitempty"`
	FileName       string    `json:"file_name"`
	Extension      string    `json:"extension"`
	MimeType       string    `json:"mime_type"`
	SizeBytes      int64     `json:"size_bytes"`
	Category       string    `json:"category"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SearchResult struct {
	Message
	AccountPhone      string     `json:"account_phone"`
	AccountUsername   string     `json:"account_username"`
	AccountFirstName  string     `json:"account_first_name"`
	ChannelTitle      string     `json:"channel_title"`
	ChannelUsername   string     `json:"channel_username"`
	TelegramChannelID int64      `json:"telegram_channel_id"`
	Links             []Link     `json:"links"`
	Media             *MediaURLs `json:"media,omitempty"`
}

type MediaURLs struct {
	ImageURL string `json:"image_url,omitempty"`
	VideoURL string `json:"video_url,omitempty"`
}

type ListResult[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

func (r ListResult[T]) MarshalJSON() ([]byte, error) {
	items := r.Items
	if items == nil {
		items = []T{}
	}
	return json.Marshal(struct {
		Items []T `json:"items"`
		Total int `json:"total"`
	}{
		Items: items,
		Total: r.Total,
	})
}

type LinkResult struct {
	Link
	MessageText       string     `json:"message_text"`
	MessageDate       time.Time  `json:"message_date"`
	MessageType       string     `json:"message_type,omitempty"`
	MediaSummary      string     `json:"media_summary,omitempty"`
	AccountID         int64      `json:"account_id"`
	ChannelID         int64      `json:"channel_id"`
	TelegramChannelID int64      `json:"telegram_channel_id"`
	ChannelTitle      string     `json:"channel_title"`
	ChannelUsername   string     `json:"channel_username"`
	TelegramMessageID int64      `json:"telegram_message_id"`
	Media             *MediaURLs `json:"media,omitempty"`
}

type FileResult struct {
	File
	MessageText       string     `json:"message_text"`
	MessageDate       time.Time  `json:"message_date"`
	AccountID         int64      `json:"account_id"`
	ChannelID         int64      `json:"channel_id"`
	TelegramChannelID int64      `json:"telegram_channel_id"`
	ChannelTitle      string     `json:"channel_title"`
	ChannelUsername   string     `json:"channel_username"`
	TelegramMessageID int64      `json:"telegram_message_id"`
	Media             *MediaURLs `json:"media,omitempty"`
}

type ChannelSearchResult struct {
	Channel
	AccountPhone    string `json:"account_phone"`
	AccountUsername string `json:"account_username"`
}

type GlobalSearchResult struct {
	Messages ListResult[SearchResult]        `json:"messages"`
	Links    ListResult[LinkResult]          `json:"links"`
	Files    ListResult[FileResult]          `json:"files"`
	Channels ListResult[ChannelSearchResult] `json:"channels"`
}

type MergedLink struct {
	URL               string    `json:"url"`
	Password          string    `json:"password,omitempty"`
	Note              string    `json:"note,omitempty"`
	MediaTitle        string    `json:"media_title,omitempty"`
	MediaYear         string    `json:"media_year,omitempty"`
	MediaSeason       string    `json:"media_season,omitempty"`
	MediaEpisode      string    `json:"media_episode,omitempty"`
	MediaQuality      string    `json:"media_quality,omitempty"`
	MediaSize         string    `json:"media_size,omitempty"`
	MediaTMDBID       string    `json:"media_tmdb_id,omitempty"`
	MediaCategory     string    `json:"media_category,omitempty"`
	MediaTags         string    `json:"media_tags,omitempty"`
	Datetime          time.Time `json:"datetime"`
	Source            string    `json:"source,omitempty"`
	ChannelID         int64     `json:"channel_id"`
	TelegramMessageID int64     `json:"telegram_message_id"`
}

type MergedLinks map[string][]MergedLink

type MergedLinksResponse struct {
	Total        int         `json:"total"`
	MergedByType MergedLinks `json:"merged_by_type"`
}

type StatusCounts struct {
	Accounts      int64            `json:"accounts"`
	Channels      int64            `json:"channels"`
	Messages      int64            `json:"messages"`
	Links         int64            `json:"links"`
	AccountStates map[string]int64 `json:"account_states"`
}
