package task

type GapRecoveryPayload struct {
	AccountID         int64 `json:"account_id"`
	ChannelID         int64 `json:"channel_id"`
	FromMessageID     int64 `json:"from_message_id"`
	ToMessageID       int64 `json:"to_message_id"`
	TriggerMessageID  int64 `json:"trigger_message"`
	TelegramChannelID int64 `json:"telegram_channel"`
}

type HistorySyncPayload struct {
	ChannelID   int64   `json:"channel_id,omitempty"`
	ChannelIDs  []int64 `json:"channel_ids,omitempty"`
	MaxMessages int     `json:"max_messages,omitempty"`
}

type AIMediaMetadataPayload struct {
	MessageID int64 `json:"message_id"`
}

type RepairMediaTitlePayload struct {
	DryRun bool `json:"dry_run"`
}
