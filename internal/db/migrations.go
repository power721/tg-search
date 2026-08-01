package db

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		name:    "fresh_tg_search_schema",
		sql: `
CREATE TABLE IF NOT EXISTS telegram_accounts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  phone TEXT UNIQUE,
  telegram_user_id INTEGER,
  first_name TEXT,
  last_name TEXT,
  username TEXT,
  status TEXT NOT NULL,
  session_path TEXT NOT NULL DEFAULT '',
  last_online_at DATETIME,
  last_error TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS telegram_channels (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER NOT NULL,
  telegram_channel_id INTEGER NOT NULL,
  access_hash INTEGER,
  title TEXT,
  username TEXT,
  type TEXT NOT NULL,
  member_count INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT '',
  avatar_state TEXT NOT NULL DEFAULT 'unknown',
  sync_state TEXT NOT NULL DEFAULT 'metadata_only',
  listen_state TEXT NOT NULL DEFAULT 'disabled',
  history_sync_enabled INTEGER NOT NULL DEFAULT 0,
  sync_profile TEXT NOT NULL DEFAULT 'Normal',
  listen_enabled INTEGER NOT NULL DEFAULT 0,
  remote_search_allowed INTEGER NOT NULL DEFAULT 1,
  last_message_id INTEGER NOT NULL DEFAULT 0,
  last_sync_time DATETIME,
  web_access INTEGER,
  web_access_checked_at DATETIME,
  web_access_error TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(account_id, telegram_channel_id, type),
  FOREIGN KEY(account_id) REFERENCES telegram_accounts(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS telegram_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER NOT NULL,
  channel_id INTEGER NOT NULL,
  telegram_message_id INTEGER NOT NULL,
  sender_id INTEGER,
  message_type TEXT NOT NULL DEFAULT 'text',
  media_summary TEXT NOT NULL DEFAULT '',
  date DATETIME,
  edit_date DATETIME,
  deleted INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(channel_id, telegram_message_id),
  FOREIGN KEY(account_id) REFERENCES telegram_accounts(id) ON DELETE CASCADE,
  FOREIGN KEY(channel_id) REFERENCES telegram_channels(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS telegram_message_contents (
  message_id INTEGER PRIMARY KEY,
  text TEXT NOT NULL DEFAULT '',
  raw_json TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  FOREIGN KEY(message_id) REFERENCES telegram_messages(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS telegram_sync_cursors (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER NOT NULL,
  channel_id INTEGER NOT NULL,
  cursor_type TEXT NOT NULL,
  last_message_id INTEGER NOT NULL DEFAULT 0,
  pts INTEGER NOT NULL DEFAULT 0,
  qts INTEGER NOT NULL DEFAULT 0,
  date DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(account_id, channel_id, cursor_type),
  FOREIGN KEY(account_id) REFERENCES telegram_accounts(id) ON DELETE CASCADE,
  FOREIGN KEY(channel_id) REFERENCES telegram_channels(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS telegram_links (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id INTEGER NOT NULL,
  type TEXT NOT NULL,
  url TEXT NOT NULL,
  password TEXT,
  note TEXT,
  source_snippet TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  UNIQUE(message_id, type, url),
  FOREIGN KEY(message_id) REFERENCES telegram_messages(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS telegram_files (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id INTEGER NOT NULL,
  file_name TEXT NOT NULL,
  extension TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  category TEXT NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(message_id, file_name, size_bytes),
  FOREIGN KEY(message_id) REFERENCES telegram_messages(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS telegram_watch_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_id INTEGER NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1,
  includes_json TEXT NOT NULL DEFAULT '[]',
  excludes_json TEXT NOT NULL DEFAULT '[]',
  message_types_json TEXT NOT NULL DEFAULT '[]',
  link_types_json TEXT NOT NULL DEFAULT '[]',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  FOREIGN KEY(channel_id) REFERENCES telegram_channels(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS remote_search_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id INTEGER NOT NULL,
  channel_id INTEGER NOT NULL,
  query TEXT NOT NULL,
  status TEXT NOT NULL,
  source TEXT NOT NULL DEFAULT 'remote',
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  FOREIGN KEY(account_id) REFERENCES telegram_accounts(id) ON DELETE CASCADE,
  FOREIGN KEY(channel_id) REFERENCES telegram_channels(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS sync_tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  status TEXT NOT NULL,
  progress INTEGER NOT NULL DEFAULT 0,
  total INTEGER NOT NULL DEFAULT 0,
  message TEXT NOT NULL DEFAULT '',
  error_code TEXT NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  retry_count INTEGER NOT NULL DEFAULT 0,
  next_run_at DATETIME,
  payload_json TEXT NOT NULL DEFAULT '{}',
  started_at DATETIME,
  finished_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS recent_activities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,
  message TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL,
  last_login_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  key_hash TEXT NOT NULL,
  prefix TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  last_used_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_telegram_messages_channel_date ON telegram_messages(channel_id, date);
CREATE INDEX IF NOT EXISTS idx_telegram_messages_telegram_message_id ON telegram_messages(telegram_message_id);
CREATE INDEX IF NOT EXISTS idx_telegram_messages_account_id ON telegram_messages(account_id);
CREATE INDEX IF NOT EXISTS idx_telegram_links_type ON telegram_links(type);
CREATE INDEX IF NOT EXISTS idx_telegram_links_message_id ON telegram_links(message_id);
CREATE INDEX IF NOT EXISTS idx_telegram_links_category_message_id ON telegram_links(category, message_id);
CREATE INDEX IF NOT EXISTS idx_telegram_files_message_id ON telegram_files(message_id);
CREATE INDEX IF NOT EXISTS idx_telegram_files_category_message_id ON telegram_files(category, message_id);
CREATE INDEX IF NOT EXISTS idx_telegram_channels_account_id ON telegram_channels(account_id);
CREATE INDEX IF NOT EXISTS idx_telegram_messages_account_date_id ON telegram_messages(account_id, date DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_telegram_messages_channel_date_id ON telegram_messages(channel_id, date DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_telegram_message_contents_updated_at ON telegram_message_contents(updated_at);
CREATE INDEX IF NOT EXISTS idx_telegram_sync_cursors_channel_type ON telegram_sync_cursors(channel_id, cursor_type);
CREATE INDEX IF NOT EXISTS idx_telegram_links_type_message_id ON telegram_links(type, message_id);
CREATE INDEX IF NOT EXISTS idx_api_keys_prefix ON api_keys(prefix);
CREATE INDEX IF NOT EXISTS idx_sync_tasks_status_updated_at ON sync_tasks(status, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_sync_tasks_next_run_at ON sync_tasks(next_run_at);
CREATE INDEX IF NOT EXISTS idx_recent_activities_created_at ON recent_activities(created_at DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS telegram_messages_fts
USING fts5(text, content='telegram_message_contents', content_rowid='message_id');

CREATE TRIGGER IF NOT EXISTS telegram_message_contents_ai AFTER INSERT ON telegram_message_contents
WHEN (SELECT deleted FROM telegram_messages WHERE id = new.message_id) = 0
BEGIN
  INSERT INTO telegram_messages_fts(rowid, text) VALUES (new.message_id, new.text);
END;

CREATE TRIGGER IF NOT EXISTS telegram_message_contents_ad AFTER DELETE ON telegram_message_contents
WHEN (SELECT deleted FROM telegram_messages WHERE id = old.message_id) = 0
BEGIN
  INSERT INTO telegram_messages_fts(telegram_messages_fts, rowid, text)
  VALUES ('delete', old.message_id, old.text);
END;

CREATE TRIGGER IF NOT EXISTS telegram_message_contents_au AFTER UPDATE ON telegram_message_contents
WHEN (SELECT deleted FROM telegram_messages WHERE id = new.message_id) = 0
BEGIN
  INSERT INTO telegram_messages_fts(telegram_messages_fts, rowid, text)
  VALUES ('delete', old.message_id, old.text);
  INSERT INTO telegram_messages_fts(rowid, text) VALUES (new.message_id, new.text);
END;

CREATE TRIGGER IF NOT EXISTS telegram_messages_deleted_au AFTER UPDATE OF deleted ON telegram_messages
WHEN old.deleted <> new.deleted
BEGIN
  INSERT INTO telegram_messages_fts(telegram_messages_fts, rowid, text)
  SELECT 'delete', old.id, c.text
  FROM telegram_message_contents c
  WHERE c.message_id = old.id AND old.deleted = 0;
  INSERT INTO telegram_messages_fts(rowid, text)
  SELECT new.id, c.text
  FROM telegram_message_contents c
  WHERE c.message_id = new.id AND new.deleted = 0;
END;
`,
	},
	{
		version: 2,
		name:    "resource_group_count_cache",
		sql: `
CREATE TABLE IF NOT EXISTS resource_group_counts (
  category TEXT PRIMARY KEY,
  count INTEGER NOT NULL,
  updated_at DATETIME NOT NULL
);
`,
	},
	{
		version: 3,
		name:    "api_key_ciphertext",
		sql: `
ALTER TABLE api_keys ADD COLUMN key_ciphertext TEXT NOT NULL DEFAULT '';
`,
	},
	{
		version: 4,
		name:    "link_media_metadata",
		sql: `
ALTER TABLE telegram_links ADD COLUMN media_title TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_year TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_season TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_episode TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_quality TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_size TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_tmdb_id TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_category TEXT NOT NULL DEFAULT '';
ALTER TABLE telegram_links ADD COLUMN media_tags TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_telegram_links_media_title ON telegram_links(media_title);
`,
	},
	{
		version: 5,
		name:    "file_telegram_file_id",
		sql: `
ALTER TABLE telegram_files ADD COLUMN telegram_file_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX IF NOT EXISTS idx_telegram_files_telegram_file_id ON telegram_files(telegram_file_id);
`,
	},
	{
		version: 6,
		name:    "api_key_usage_count",
		sql: `
ALTER TABLE api_keys ADD COLUMN usage_count INTEGER NOT NULL DEFAULT 0;
`,
	},
	{
		version: 7,
		name:    "saved_searches_notifications",
		sql: `
CREATE TABLE IF NOT EXISTS saved_searches (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  keyword TEXT NOT NULL,
  filters_json TEXT NOT NULL DEFAULT '{}',
  notify_rss INTEGER NOT NULL DEFAULT 1,
  notify_webhook INTEGER NOT NULL DEFAULT 0,
  notify_telegram INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS webhooks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  events_json TEXT NOT NULL DEFAULT '[]',
  secret TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE TABLE IF NOT EXISTS notification_deliveries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_type TEXT NOT NULL,
  target_type TEXT NOT NULL,
  target_id INTEGER NOT NULL DEFAULT 0,
  payload_json TEXT NOT NULL DEFAULT '{}',
  status TEXT NOT NULL,
  retry_count INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  next_run_at DATETIME,
  delivered_at DATETIME,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_saved_searches_enabled ON saved_searches(enabled);
CREATE INDEX IF NOT EXISTS idx_webhooks_enabled ON webhooks(enabled);
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_status_next_run_at ON notification_deliveries(status, next_run_at);
CREATE INDEX IF NOT EXISTS idx_notification_deliveries_event_type ON notification_deliveries(event_type);
`,
	},
	{
		version: 8,
		name:    "telegram_bot_subscriptions",
		sql: `
CREATE TABLE IF NOT EXISTS telegram_bot_subscriptions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id INTEGER NOT NULL,
  saved_search_id INTEGER NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  UNIQUE(chat_id, saved_search_id),
  FOREIGN KEY(saved_search_id) REFERENCES saved_searches(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_telegram_bot_subscriptions_saved_search ON telegram_bot_subscriptions(saved_search_id, enabled);
CREATE INDEX IF NOT EXISTS idx_telegram_bot_subscriptions_chat ON telegram_bot_subscriptions(chat_id, enabled);
`,
	},
	{
		version: 9,
		name:    "telegram_bot_sent_resources",
		sql: `
CREATE TABLE IF NOT EXISTS telegram_bot_sent_resources (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_id INTEGER NOT NULL,
  resource_id TEXT NOT NULL,
  created_at DATETIME NOT NULL,
  UNIQUE(chat_id, resource_id)
);

CREATE INDEX IF NOT EXISTS idx_telegram_bot_sent_resources_chat ON telegram_bot_sent_resources(chat_id);
`,
	},
	{
		version: 10,
		name:    "telegram_bot_chats",
		sql: `
CREATE TABLE IF NOT EXISTS telegram_bot_chats (
  chat_id INTEGER PRIMARY KEY,
  title TEXT NOT NULL DEFAULT '',
  username TEXT NOT NULL DEFAULT '',
  first_name TEXT NOT NULL DEFAULT '',
  last_name TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT '',
  last_seen_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_telegram_bot_chats_last_seen_at ON telegram_bot_chats(last_seen_at);
`,
	},
	{
		version: 11,
		name:    "channel_avatar_photo_id",
		sql: `
ALTER TABLE telegram_channels ADD COLUMN photo_id INTEGER NOT NULL DEFAULT 0;
`,
	},
	{
		version: 12,
		name:    "account_avatar_photo_id",
		sql: `
ALTER TABLE telegram_accounts ADD COLUMN photo_id INTEGER NOT NULL DEFAULT 0;
`,
	},
	{
		version: 13,
		name:    "admin_sessions",
		sql: `
CREATE TABLE IF NOT EXISTS admin_sessions (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL,
  expires_at DATETIME NOT NULL,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_admin_sessions_expires_at ON admin_sessions(expires_at);
CREATE INDEX IF NOT EXISTS idx_admin_sessions_user_id ON admin_sessions(user_id);
`,
	},
	{
		version: 14,
		name:    "telegram_links_url_index",
		sql: `
CREATE INDEX IF NOT EXISTS idx_telegram_links_url ON telegram_links(url);
`,
	},
	{
		version: 15,
		name:    "resource_index_read_model",
		sql: `
CREATE TABLE IF NOT EXISTS resource_index (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  resource_id TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,
  source_key TEXT NOT NULL UNIQUE,
  source_message_id INTEGER NOT NULL DEFAULT 0,
  url TEXT NOT NULL DEFAULT '',
  type TEXT NOT NULL DEFAULT '',
  category TEXT NOT NULL,
  password TEXT NOT NULL DEFAULT '',
  note TEXT NOT NULL DEFAULT '',
  title TEXT NOT NULL DEFAULT '',
  source_snippet TEXT NOT NULL DEFAULT '',
  telegram_file_id INTEGER NOT NULL DEFAULT 0,
  file_name TEXT NOT NULL DEFAULT '',
  extension TEXT NOT NULL DEFAULT '',
  mime_type TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL DEFAULT 0,
  media_title TEXT NOT NULL DEFAULT '',
  media_year TEXT NOT NULL DEFAULT '',
  media_season TEXT NOT NULL DEFAULT '',
  media_episode TEXT NOT NULL DEFAULT '',
  media_quality TEXT NOT NULL DEFAULT '',
  media_size TEXT NOT NULL DEFAULT '',
  media_tmdb_id TEXT NOT NULL DEFAULT '',
  media_category TEXT NOT NULL DEFAULT '',
  media_tags TEXT NOT NULL DEFAULT '',
  media_summary TEXT NOT NULL DEFAULT '',
  datetime DATETIME NOT NULL,
  account_id INTEGER NOT NULL,
  channel_id INTEGER NOT NULL,
  telegram_channel_id INTEGER NOT NULL,
  channel_title TEXT NOT NULL DEFAULT '',
  channel_username TEXT NOT NULL DEFAULT '',
  telegram_message_id INTEGER NOT NULL,
  message_type TEXT NOT NULL DEFAULT '',
  source_channel_count INTEGER NOT NULL DEFAULT 1,
  message_count INTEGER NOT NULL DEFAULT 1,
  provider_count INTEGER NOT NULL DEFAULT 1,
  score INTEGER NOT NULL DEFAULT 0,
  updated_at DATETIME NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_resource_index_category_datetime ON resource_index(category, datetime DESC, resource_id DESC);
CREATE INDEX IF NOT EXISTS idx_resource_index_type_datetime ON resource_index(type, datetime DESC, resource_id DESC);
CREATE INDEX IF NOT EXISTS idx_resource_index_datetime ON resource_index(datetime DESC, resource_id DESC);
CREATE INDEX IF NOT EXISTS idx_resource_index_channel_datetime ON resource_index(channel_id, datetime DESC, resource_id DESC);
CREATE INDEX IF NOT EXISTS idx_resource_index_account_datetime ON resource_index(account_id, datetime DESC, resource_id DESC);
CREATE INDEX IF NOT EXISTS idx_resource_index_score_datetime ON resource_index(score DESC, datetime DESC, resource_id DESC);
CREATE INDEX IF NOT EXISTS idx_resource_index_kind_datetime ON resource_index(kind, datetime DESC, resource_id DESC);
CREATE INDEX IF NOT EXISTS idx_resource_index_source_message ON resource_index(source_message_id);

CREATE VIRTUAL TABLE IF NOT EXISTS resource_index_fts
USING fts5(title, note, source_snippet, url, type, category, media_title, media_tags, file_name, content='resource_index', content_rowid='id');

CREATE TRIGGER IF NOT EXISTS resource_index_ai AFTER INSERT ON resource_index BEGIN
  INSERT INTO resource_index_fts(rowid, title, note, source_snippet, url, type, category, media_title, media_tags, file_name)
  VALUES (new.id, new.title, new.note, new.source_snippet, new.url, new.type, new.category, new.media_title, new.media_tags, new.file_name);
END;

CREATE TRIGGER IF NOT EXISTS resource_index_ad AFTER DELETE ON resource_index BEGIN
  INSERT INTO resource_index_fts(resource_index_fts, rowid, title, note, source_snippet, url, type, category, media_title, media_tags, file_name)
  VALUES ('delete', old.id, old.title, old.note, old.source_snippet, old.url, old.type, old.category, old.media_title, old.media_tags, old.file_name);
END;

CREATE TRIGGER IF NOT EXISTS resource_index_au AFTER UPDATE ON resource_index BEGIN
  INSERT INTO resource_index_fts(resource_index_fts, rowid, title, note, source_snippet, url, type, category, media_title, media_tags, file_name)
  VALUES ('delete', old.id, old.title, old.note, old.source_snippet, old.url, old.type, old.category, old.media_title, old.media_tags, old.file_name);
  INSERT INTO resource_index_fts(rowid, title, note, source_snippet, url, type, category, media_title, media_tags, file_name)
  VALUES (new.id, new.title, new.note, new.source_snippet, new.url, new.type, new.category, new.media_title, new.media_tags, new.file_name);
END;
`,
	},
	{
		version: 16,
		name:    "resource_index_category_type_datetime",
		sql: `
CREATE INDEX IF NOT EXISTS idx_resource_index_category_type_datetime
ON resource_index(category, type, datetime DESC, resource_id DESC);`,
	},
}

func Migrate(ctx context.Context, conn *sql.DB) error {
	if _, err := conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		applied, err := migrationApplied(ctx, conn, m.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := WithTx(ctx, conn, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				return fmt.Errorf("apply migration %03d %s: %w", m.version, m.name, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
				return fmt.Errorf("record migration %03d: %w", m.version, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, conn *sql.DB, version int) (bool, error) {
	var count int
	if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations WHERE version = ?`, version).Scan(&count); err != nil {
		return false, fmt.Errorf("check migration %03d: %w", version, err)
	}
	return count > 0, nil
}
