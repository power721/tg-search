export interface ErrorEnvelope {
  error: {
    code: string
    message: string
  }
}

export interface SetupStatus {
  complete: boolean
  admin_configured: boolean
  api_key_configured: boolean
  api_key_step_complete: boolean
  telegram_configured: boolean
  telegram_login_complete: boolean
  listen_rules_configured: boolean
  current_step: 'admin' | 'api_key' | 'telegram_api' | 'telegram_login' | 'listen_rules' | 'channel_selection' | 'complete'
}

export interface ListenRulesPayload {
  includes: string[]
  excludes: string[]
  message_types: string[]
  link_types: string[]
  ignored_link_patterns?: string[]
}

export interface APIKeyResponse {
  id: number
  name: string
  prefix: string
  key: string
  usage_count: number
  last_used_at?: string
  created_at?: string
  updated_at?: string
}

export type APIKeySetupResponse = APIKeyResponse

export interface WatchRulePayload extends ListenRulesPayload {
  channel_id: number
  enabled: boolean
}

export interface WatchRule extends WatchRulePayload {
  id: number
}

export interface User {
  id: number
  username: string
  role: string
  last_login_at?: string
  created_at?: string
  updated_at?: string
}

export interface ServiceStatus {
  service: string
  accounts: number
  channels: number
  messages: number
  links: number
  account_states: Record<string, number>
}

export interface StorageUsage {
  db_bytes: number
  index_bytes: number
  media_cache_bytes: number
  total_bytes: number
  max_db_bytes: number
  max_media_bytes: number
  db_over_quota: boolean
  media_over_quota: boolean
}

export interface VersionInfoResponse {
  current_version: string
  latest_version?: string
  latest_url?: string
  update_available: boolean
}

export interface SystemInfoResponse {
  name: string
  version: string
  architecture: string
  go_version: string
  cpu_count: number
  hostname: string
}

export interface TelegramAPISettingsResponse {
  configured: boolean
  app_id: number
  app_hash_set: boolean
}

export interface TelegramBotSettingsResponse {
  enabled: boolean
  configured: boolean
  token_set: boolean
  poll_interval: string
}

export interface RuntimeSettings {
  sync: {
    workers: number
    history_batch_size: number
    telegram_request_interval: string
  }
  storage: {
    max_db_size: number
    max_media_cache: number
  }
  telegram: {
    proxy: string
    reconnect_timeout: string
    dial_timeout: string
    rate_limit: {
      enabled: boolean
      rate_per_second: number
      burst: number
    }
    stream: {
      concurrency: number
      buffers: number
      chunk_timeout: string
    }
    media: {
      concurrency: number
    }
  }
  ai: {
    media_metadata: {
      enabled: boolean
      provider?: string
      base_url: string
      api_key?: string
      api_key_set?: boolean
      model: string
      fallback_enabled?: boolean
      providers?: AIMediaMetadataProviderSettings[]
    }
  }
}

export interface AIMediaMetadataProviderSettings {
  id: string
  name?: string
  provider: string
  base_url: string
  api_key?: string
  api_key_set?: boolean
  model: string
  enabled: boolean
}

export interface AIModelsResponse {
  items: string[]
}

export interface AITestResponse {
  ok: boolean
  model: string
  latency_ms: number
}

export interface AIProviderPreset {
  id: string
  name: string
  base_url: string
  default_model: string
  api_key_env?: string
  website: string
  free: boolean
  local: boolean
  requires_api_key: boolean
}

export interface AIProvidersResponse {
  items: AIProviderPreset[]
}

export interface TelegramAccount {
  id: number
  phone: string
  telegram_user_id: number
  first_name: string
  last_name: string
  username: string
  photo_id: number
  status: string
  session_path?: string
  last_online_at?: string
  last_error: string
}

export interface TelegramLoginResponse {
  status: string
  phone?: string
  password_required?: boolean
  account?: TelegramAccount
  metadata_sync?: {
    status: string
    channel_count: number
    error?: string
  }
}

export interface TelegramQRLoginStartResponse {
  login_id: string
  status: 'pending'
  qr_url: string
  expires_at: string
}

export interface TelegramQRLoginStatusResponse extends TelegramLoginResponse {
  login_id: string
  status: 'pending' | 'online'
  qr_url?: string
  expires_at?: string
}

export interface TelegramAccountsResponse {
  items: TelegramAccount[]
}

export type SyncProfile = 'Quick' | 'Normal' | 'Deep' | 'Full'

export interface TelegramChannel {
  id: number
  account_id: number
  telegram_channel_id: number
  access_hash: number
  title: string
  username: string
  type: string
  member_count: number
  description: string
  photo_id: number
  avatar_state: string
  sync_state: string
  listen_state: string
  history_sync_enabled: boolean
  sync_profile: SyncProfile
  listen_enabled: boolean
  remote_search_allowed: boolean
  last_message_id: number
  last_sync_time?: string
  web_access?: boolean
  web_access_checked_at?: string
  web_access_error: string
  indexed_message_count: number
}

export interface ChannelControlPayload {
  history_sync_enabled: boolean
  sync_profile: SyncProfile
  listen_enabled: boolean
  remote_search_allowed: boolean
}

export interface ChannelsResponse {
  items: TelegramChannel[]
}

export interface ChannelClearResponse {
  channel: TelegramChannel
  deleted: {
    messages: number
    links: number
    files: number
  }
}

export interface WebAccessCheckResponse {
  items: Array<{
    channel_id: number
    web_access: boolean
    checked_at: string
    web_access_error: string
  }>
}

export interface ChannelAnalysis {
  channel: TelegramChannel
  control: ChannelControlPayload
  watch_rule?: WatchRule
  indexed_counts: {
    messages: number
    links: number
    files: number
  }
}

export interface RemoteSearchTask {
  id: number
  account_id: number
  channel_id: number
  query: string
  status: string
  source: string
  expires_at: string
}

export interface SavedSearchFilters {
  type?: string
  category?: string
  cloud_types?: string[]
  account_id?: number
  channel_id?: number
}

export interface SavedSearch {
  id: number
  name: string
  keyword: string
  filters: SavedSearchFilters
  notify_rss: boolean
  notify_webhook: boolean
  notify_telegram: boolean
  telegram_chat_ids?: number[]
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface SavedSearchesResponse {
  items: SavedSearch[]
}

export interface TelegramBotChat {
  chat_id: number
  title: string
  username: string
  first_name: string
  last_name: string
  type: string
  last_seen_at?: string
  created_at?: string
  updated_at?: string
}

export interface TelegramBotChatsResponse {
  items: TelegramBotChat[]
}

export interface Webhook {
  id: number
  name: string
  url: string
  events: string[]
  enabled: boolean
  created_at?: string
  updated_at?: string
}

export interface WebhooksResponse {
  items: Webhook[]
}

export interface NotificationDelivery {
  id: number
  event_type: string
  target_type: string
  target_id: number
  status: string
  retry_count: number
  last_error: string
  delivered_at?: string
  created_at?: string
  updated_at?: string
}

export interface NotificationDeliveriesResponse {
  items: NotificationDelivery[]
}

export interface Task {
  id: number
  type: string
  status: string
  progress: number
  total: number
  message?: string
  error_code?: string
  error_message?: string
  retry_count: number
  next_run_at?: string
  payload_json?: string
  started_at?: string
  finished_at?: string
  created_at?: string
  updated_at?: string
}

export interface TasksResponse {
  items: Task[]
  total: number
}

export interface RuntimeEvent<T = unknown> {
  type: string
  payload?: T
  created_at: string
}

export interface LogFileInfo {
  name: string
  size: number
  mod_time?: string
}

export interface LogEntry {
  file: string
  time?: string
  level?: string
  message?: string
  caller?: string
  fields?: Record<string, unknown>
  raw: string
}

export interface LogsResponse {
  items: LogEntry[]
  total: number
  files: LogFileInfo[]
  limit: number
  offset: number
  order: 'asc' | 'desc'
}

export interface ListResult<T> {
  items: T[]
  total: number
}

export interface Link {
  id: number
  message_id: number
  type: string
  url: string
  password?: string
  note?: string
  source_snippet?: string
  category?: string
  media_title?: string
  media_year?: string
  media_season?: string
  media_episode?: string
  media_quality?: string
  media_size?: string
  media_tmdb_id?: string
  media_category?: string
  media_tags?: string
}

export interface MediaURLs {
  image_url?: string
  video_url?: string
}

export interface ResourceMedia extends MediaURLs {
  title?: string
  year?: string
  season?: string
  episode?: string
  quality?: string
  size?: string
  tmdb_id?: string
  category?: string
  tags?: string
  summary?: string
}

export interface MessageSearchResult {
  id: number
  channel_id: number
  telegram_channel_id?: number
  telegram_message_id: number
  message_type?: string
  media_summary?: string
  media?: MediaURLs
  text: string
  raw_json?: string
  date?: string
  channel_title?: string
  channel_username?: string
  links?: Link[]
  source?: 'local' | 'remote'
}

export interface LinkSearchResult extends Link {
  message_text?: string
  message_date?: string
  message_type?: string
  media_summary?: string
  channel_id?: number
  telegram_channel_id?: number
  channel_title?: string
  channel_username?: string
  telegram_message_id?: number
  media?: MediaURLs
  source?: 'local' | 'remote'
}

export interface FileSearchResult {
  id: number
  message_id: number
  telegram_file_id?: number
  file_name: string
  extension: string
  mime_type: string
  size_bytes: number
  category: string
  message_text?: string
  message_date?: string
  channel_id?: number
  telegram_channel_id?: number
  channel_title?: string
  channel_username?: string
  telegram_message_id?: number
  media?: MediaURLs
  source?: 'local' | 'remote'
}

export interface ChannelSearchResult extends TelegramChannel {
  source?: 'local' | 'remote'
}

export interface GlobalSearchResult {
  messages: ListResult<MessageSearchResult>
  links: ListResult<LinkSearchResult>
  files: ListResult<FileSearchResult>
  channels: ListResult<ChannelSearchResult>
}

export interface RemoteSearchItem {
  source: 'remote'
  channel_id: number
  telegram_channel_id?: number
  channel_title: string
  channel_username?: string
  telegram_message_id: number
  message_type?: string
  media_summary?: string
  media?: MediaURLs
  text: string
  raw_json?: string
  date?: string
}

export interface RemoteSearchResults {
  task: RemoteSearchTask
  items: RemoteSearchItem[]
}

export interface ResourceItem {
  id: string
  kind: 'link' | 'file'
  type?: string
  category: string
  url?: string
  password?: string
  telegram_file_id?: number
  file_name?: string
  extension?: string
  mime_type?: string
  size_bytes?: number
  note?: string
  title?: string
  source_snippet?: string
  datetime?: string
  channel_id?: number
  telegram_channel_id?: number
  channel_title?: string
  channel_username?: string
  telegram_message_id?: number
  message_type?: string
  media?: ResourceMedia
}

export interface ResourcesResponse {
  items: ResourceItem[]
  total: number
}

export interface ResourcesGroupedResponse {
  grouped: Record<string, number>
}

export interface DashboardResourceStatsResponse {
  grouped: Record<string, number>
}

export interface LinksGroupedResponse {
  grouped: Record<string, number>
}
