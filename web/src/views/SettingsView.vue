<script setup lang="ts">
import { useDialog, useMessage } from 'naive-ui'
import { computed, onMounted, ref, watch } from 'vue'
import { apiDelete, apiGet, apiPost, apiPut } from '@/api/client'
import type {
  AIMediaMetadataProviderSettings,
  AIProviderPreset,
  AIProvidersResponse,
  AIModelsResponse,
  AITestResponse,
  ChannelsResponse,
  NotificationDeliveriesResponse,
  NotificationDelivery,
  RuntimeSettings,
  SavedSearch,
  SavedSearchesResponse,
  StorageUsage,
  SystemInfoResponse,
  TelegramAccount,
  TelegramAccountsResponse,
  TelegramAPISettingsResponse,
  TelegramBotChat,
  TelegramBotChatsResponse,
  TelegramBotSettingsResponse,
  TelegramChannel,
  Webhook,
  WebhooksResponse,
  VersionInfoResponse
} from '@/api/types'
import { useAPIKeyStore } from '@/stores/apiKey'
import { useAuthStore } from '@/stores/auth'

const message = useMessage()
const dialog = useDialog()
const apiKey = useAPIKeyStore()
const auth = useAuthStore()
const showAPIKey = ref(false)
const credentialsUsername = ref('')
const currentPassword = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const credentialsLoading = ref(false)
const storageUsage = ref<StorageUsage | null>(null)
const telegramSettings = ref<TelegramAPISettingsResponse | null>(null)
const telegramAppID = ref('')
const telegramAppHash = ref('')
const telegramLoading = ref(false)
const telegramBotSettings = ref<TelegramBotSettingsResponse | null>(null)
const telegramBotLoading = ref(false)
const telegramBotForm = ref({
  enabled: false,
  token: '',
  pollInterval: '3s'
})
const versionInfo = ref<VersionInfoResponse | null>(null)
const versionLoading = ref(false)
const versionError = ref('')
const repairLoading = ref(false)
const systemInfo = ref<SystemInfoResponse | null>(null)
const currentRuntimeSettings = ref<RuntimeSettings | null>(null)
const activeTab = ref('security')
const runtimeLoading = ref(false)
const aiModels = ref<string[]>([])
const aiModelsLoading = ref(false)
const aiProviders = ref<AIProviderPreset[]>([])
const savedSearches = ref<SavedSearch[]>([])
const savedSearchLoading = ref(false)
const savedSearchSaving = ref(false)
const editingSavedSearchID = ref<number | null>(null)
const telegramBotChats = ref<TelegramBotChat[]>([])
const telegramBotChatsLoading = ref(false)
const savedSearchForm = ref({
  name: '',
  keyword: '',
  category: '',
  resourceTypes: [] as string[],
  accountID: 0,
  channelID: 0,
  telegramChatIDs: [] as number[],
  notifyRSS: true,
  notifyWebhook: false,
  notifyTelegram: false,
  enabled: true
})
const accounts = ref<TelegramAccount[]>([])
const channels = ref<TelegramChannel[]>([])
const webhooks = ref<Webhook[]>([])
const webhookLoading = ref(false)
const webhookSaving = ref(false)
const editingWebhookID = ref<number | null>(null)
const webhookForm = ref({
  name: '',
  url: '',
  secret: '',
  enabled: true,
  events: ['resource.created']
})
const deliveries = ref<NotificationDelivery[]>([])
const deliveryLoading = ref(false)
const notificationEventLabels: Record<string, string> = {
  'resource.created': '资源创建',
  'resource.updated': '资源更新',
  'saved_search.matched': '搜索订阅匹配',
  'task.completed': '任务完成',
  'task.failed': '任务失败',
  'account.offline': '账号离线',
  'channel.sync.completed': '频道同步完成'
}
const notificationTargetLabels: Record<string, string> = {
  webhook: 'Webhook',
  telegram: 'Telegram 消息',
  saved_search: '搜索订阅'
}
const notificationDeliveryStatusLabels: Record<string, string> = {
  pending: '待发送',
  running: '发送中',
  succeeded: '发送成功',
  failed: '发送失败'
}
const telegramChatTypeLabels: Record<string, string> = {
  private: '私聊',
  group: '群组',
  supergroup: '超级群组',
  channel: '频道'
}
const notificationEventOptions = [
  { value: 'resource.created', label: notificationEventLabels['resource.created'] },
  { value: 'resource.updated', label: notificationEventLabels['resource.updated'] },
  { value: 'saved_search.matched', label: notificationEventLabels['saved_search.matched'] },
  { value: 'task.completed', label: notificationEventLabels['task.completed'] },
  { value: 'task.failed', label: notificationEventLabels['task.failed'] },
  { value: 'account.offline', label: notificationEventLabels['account.offline'] },
  { value: 'channel.sync.completed', label: notificationEventLabels['channel.sync.completed'] }
]
const savedSearchCategoryOptions = [
  { label: '全部大类', value: '' },
  { label: '网盘', value: 'cloud_drive' },
  { label: '磁力', value: 'magnet' },
  { label: 'ED2K', value: 'ed2k' },
  { label: 'HTTP 链接', value: 'http' },
  { label: '文件', value: 'files' }
]
const savedSearchResourceTypeOptions = [
  { label: '夸克网盘', value: 'quark' },
  { label: '阿里云盘', value: 'aliyun' },
  { label: '百度网盘', value: 'baidu' },
  { label: 'UC 网盘', value: 'uc' },
  { label: '迅雷云盘', value: 'xunlei' },
  { label: '天翼云盘', value: 'tianyi' },
  { label: '115 网盘', value: '115' },
  { label: '移动云盘', value: 'mobile' },
  { label: 'PikPak', value: 'pikpak' },
  { label: '123 网盘', value: '123' },
  { label: '磁力', value: 'magnet' },
  { label: 'ED2K', value: 'ed2k' },
  { label: 'HTTP 链接', value: 'http' },
  { label: '图片文件', value: 'image' },
  { label: '视频文件', value: 'video' },
  { label: '音频文件', value: 'audio' },
  { label: '文档文件', value: 'document' },
  { label: '软件文件', value: 'software' },
  { label: '压缩包', value: 'archive' }
]
type SizeUnit = 'GB' | 'MB'
const sizeUnitOptions: Array<{ label: string; value: SizeUnit }> = [
  { label: 'GB', value: 'GB' },
  { label: 'MB', value: 'MB' }
]
const sizeUnitMultipliers: Record<SizeUnit, number> = {
  GB: 1024 * 1024 * 1024,
  MB: 1024 * 1024
}
const minStorageLimitBytes = 100 * 1024 * 1024
const runtimeForm = ref({
  workers: '',
  historyBatchSize: '',
  telegramRequestInterval: '',
  maxDBSize: '',
  maxDBSizeUnit: 'GB' as SizeUnit,
  maxMediaCache: '',
  maxMediaCacheUnit: 'GB' as SizeUnit,
  proxy: '',
  reconnectTimeout: '',
  dialTimeout: '',
  rateLimitEnabled: true,
  ratePerSecond: '',
  burst: '',
  streamConcurrency: '',
  streamBuffers: '',
  streamChunkTimeout: '',
  mediaConcurrency: '',
  aiMediaEnabled: false,
  aiProvider: 'openai_compatible',
  aiBaseURL: '',
  aiAPIKey: '',
  aiModel: '',
  aiFallbackEnabled: false,
  aiProviders: [] as AIMediaMetadataProviderSettings[]
})
const aiProviderOptions = computed(() => aiProviders.value.map((provider) => ({
  label: provider.name,
  value: provider.id
})))
const enabledAIProviders = computed(() => runtimeForm.value.aiProviders.filter((provider) => provider.enabled))

function aiProviderModelOptions(provider: AIMediaMetadataProviderSettings) {
  const values = new Set<string>()
  const preset = aiProviders.value.find((item) => item.id === provider.provider)
  if (preset?.default_model) values.add(preset.default_model)
  if (provider.model.trim()) values.add(provider.model.trim())
  for (const model of aiModels.value) {
    if (model.trim()) values.add(model.trim())
  }
  return Array.from(values).map((model) => ({ label: model, value: model }))
}
const accountOptions = computed(() => [
  { label: '全部账号', value: 0 },
  ...accounts.value.map((account) => ({
    label: account.username ? `${account.phone} (@${account.username})` : account.phone,
    value: account.id
  }))
])
const accountLabelByID = computed(() => new Map(accounts.value.map((account) => [account.id, account.username ? `${account.phone} (@${account.username})` : account.phone])))
const channelOptions = computed(() => {
  const accountID = savedSearchForm.value.accountID
  const visibleChannels = accountID > 0 ? channels.value.filter((channel) => channel.account_id === accountID) : channels.value
  return [
    { label: '全部频道', value: 0 },
    ...visibleChannels.map((channel) => ({
      label: channel.username
        ? `${channel.title} (@${channel.username})`
        : `${channel.title}${accountLabelByID.value.get(channel.account_id) ? ` - ${accountLabelByID.value.get(channel.account_id)}` : ''}`,
      value: channel.id
    }))
  ]
})
const telegramBotChatOptions = computed(() => telegramBotChats.value.map((chat) => ({
  label: telegramBotChatLabel(chat),
  value: chat.chat_id
})))

onMounted(() => {
  apiKey.load().catch((error) => {
    message.error(error instanceof Error ? error.message : '无法加载 API 密钥')
  })
  loadStorageUsage()
  loadTelegramSettings()
  loadTelegramBotSettings()
  loadRuntimeSettings()
  loadAIProviders()
  loadAccounts()
  loadChannels()
  loadTelegramBotChats()
  loadSavedSearches()
  loadWebhooks()
  loadDeliveries()
  loadVersionInfo(false)
  loadSystemInfo()
  if (!auth.loaded) {
    auth.loadMe().catch(() => {
      message.error('无法加载当前管理员')
    })
  }
})

watch(
  () => auth.user?.username,
  (username) => {
    credentialsUsername.value = username ?? credentialsUsername.value
  },
  { immediate: true }
)

watch(
  () => savedSearchForm.value.accountID,
  (accountID) => {
    if (accountID <= 0 || savedSearchForm.value.channelID <= 0) return
    const selected = channels.value.find((channel) => channel.id === savedSearchForm.value.channelID)
    if (selected && selected.account_id !== accountID) {
      savedSearchForm.value.channelID = 0
    }
  }
)

async function updateCredentials() {
  const username = credentialsUsername.value.trim()
  if (!username) {
    message.error('用户名不能为空')
    return
  }
  if (!currentPassword.value) {
    message.error('请输入当前密码')
    return
  }
  if (newPassword.value && newPassword.value.length < 8) {
    message.error('新密码至少 8 位')
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    message.error('两次输入的新密码不一致')
    return
  }
  credentialsLoading.value = true
  try {
    await auth.updateCredentials(username, currentPassword.value, newPassword.value)
    credentialsUsername.value = auth.user?.username ?? username
    currentPassword.value = ''
    newPassword.value = ''
    confirmPassword.value = ''
    message.success('管理员账号已更新')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法更新管理员账号')
  } finally {
    credentialsLoading.value = false
  }
}

async function loadStorageUsage() {
  try {
    storageUsage.value = await apiGet<StorageUsage>('/api/storage/usage')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载存储限额')
  }
}

async function loadTelegramSettings() {
  try {
    telegramSettings.value = await apiGet<TelegramAPISettingsResponse>('/api/settings/telegram-api')
    telegramAppID.value = telegramSettings.value.app_id ? String(telegramSettings.value.app_id) : ''
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载 Telegram API')
  }
}

async function loadTelegramBotSettings() {
  try {
    telegramBotSettings.value = await apiGet<TelegramBotSettingsResponse>('/api/settings/telegram-bot')
    telegramBotForm.value.enabled = telegramBotSettings.value.enabled
    telegramBotForm.value.pollInterval = telegramBotSettings.value.poll_interval || '3s'
    telegramBotForm.value.token = ''
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载 Telegram 机器人')
  }
}

async function loadSavedSearches() {
  savedSearchLoading.value = true
  try {
    const data = await apiGet<SavedSearchesResponse>('/api/saved-searches')
    savedSearches.value = Array.isArray(data.items) ? data.items : []
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载搜索订阅')
  } finally {
    savedSearchLoading.value = false
  }
}

async function loadTelegramBotChats() {
  telegramBotChatsLoading.value = true
  try {
    const data = await apiGet<TelegramBotChatsResponse>('/api/telegram-bot/chats')
    telegramBotChats.value = Array.isArray(data.items) ? data.items : []
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载 Telegram 接收人')
  } finally {
    telegramBotChatsLoading.value = false
  }
}

async function loadAccounts() {
  try {
    const data = await apiGet<TelegramAccountsResponse>('/api/accounts')
    accounts.value = Array.isArray(data.items) ? data.items : []
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载账号列表')
  }
}

async function loadChannels() {
  try {
    const data = await apiGet<ChannelsResponse>('/api/channels')
    channels.value = Array.isArray(data.items) ? data.items : []
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载频道列表')
  }
}

async function loadWebhooks() {
  webhookLoading.value = true
  try {
    const data = await apiGet<WebhooksResponse>('/api/webhooks')
    webhooks.value = Array.isArray(data.items) ? data.items : []
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载 Webhook')
  } finally {
    webhookLoading.value = false
  }
}

async function loadDeliveries() {
  deliveryLoading.value = true
  try {
    const data = await apiGet<NotificationDeliveriesResponse>('/api/notification-deliveries?limit=10&offset=0')
    deliveries.value = Array.isArray(data.items) ? data.items : []
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载通知记录')
  } finally {
    deliveryLoading.value = false
  }
}

async function loadRuntimeSettings() {
  try {
    const settings = await apiGet<RuntimeSettings>('/api/settings/runtime')
    fillRuntimeForm(settings)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载运行参数')
  }
}

async function loadAIProviders() {
  try {
    const data = await apiGet<AIProvidersResponse>('/api/settings/ai/providers')
    aiProviders.value = Array.isArray(data.items) ? data.items : []
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载 AI Provider')
  }
}

async function loadVersionInfo(checkUpdate = true) {
  versionLoading.value = true
  versionError.value = ''
  try {
    versionInfo.value = await apiGet<VersionInfoResponse>(checkUpdate ? '/api/settings/version?check_update=true' : '/api/settings/version')
  } catch (error) {
    versionError.value = error instanceof Error ? error.message : '无法检查更新'
    message.error(versionError.value)
  } finally {
    versionLoading.value = false
  }
}

async function loadSystemInfo() {
  try {
    systemInfo.value = await apiGet<SystemInfoResponse>('/api/settings/system-info')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法加载系统信息')
  }
}

function repairMediaTitles() {
  dialog.warning({
    title: '修复媒体标题',
    content: '将重新解析历史消息并更新被网盘标签覆盖的标题（如“光鸭”）。任务在后台运行，可在「任务」页查看进度。是否继续？',
    positiveText: '修复',
    negativeText: '取消',
    onPositiveClick: async () => {
      repairLoading.value = true
      try {
        const res = await apiPost<{ task_id: number; status: string }>('/api/maintenance/media-title/repair')
        message.success(`修复任务已开始 #${res.task_id}，进度见「任务」页`)
      } catch (error) {
        message.error(error instanceof Error ? error.message : '启动修复任务失败')
      } finally {
        repairLoading.value = false
      }
    }
  })
}

async function updateTelegramAPI() {
  const appID = Number(telegramAppID.value)
  const appHash = telegramAppHash.value.trim()
  if (!Number.isInteger(appID) || appID <= 0) {
    message.error('App ID 必须大于 0')
    return
  }
  if (!appHash) {
    message.error('请输入 App Hash')
    return
  }
  telegramLoading.value = true
  try {
    telegramSettings.value = await apiPut<TelegramAPISettingsResponse>('/api/settings/telegram-api', {
      app_id: appID,
      app_hash: appHash
    })
    telegramAppID.value = String(telegramSettings.value.app_id)
    telegramAppHash.value = ''
    message.success('Telegram API 已更新')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法保存 Telegram API')
  } finally {
    telegramLoading.value = false
  }
}

async function updateTelegramBot() {
  if (!telegramBotForm.value.pollInterval.trim()) {
    message.error('请输入轮询间隔')
    return
  }
  telegramBotLoading.value = true
  try {
    telegramBotSettings.value = await apiPut<TelegramBotSettingsResponse>('/api/settings/telegram-bot', {
      enabled: telegramBotForm.value.enabled,
      token: telegramBotForm.value.token.trim(),
      poll_interval: telegramBotForm.value.pollInterval.trim()
    })
    telegramBotForm.value.enabled = telegramBotSettings.value.enabled
    telegramBotForm.value.pollInterval = telegramBotSettings.value.poll_interval
    telegramBotForm.value.token = ''
    message.success('Telegram 机器人已保存并生效')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法保存 Telegram 机器人')
  } finally {
    telegramBotLoading.value = false
  }
}

async function saveSavedSearch() {
  const keyword = savedSearchForm.value.keyword.trim()
  if (!keyword) {
    message.error('关键词不能为空')
    return
  }
  savedSearchSaving.value = true
  try {
    const payload = savedSearchPayload()
    if (editingSavedSearchID.value) {
      await apiPut<SavedSearch>(`/api/saved-searches/${editingSavedSearchID.value}`, payload)
      message.success('搜索订阅已更新')
    } else {
      await apiPost<SavedSearch>('/api/saved-searches', payload)
      message.success('搜索订阅已创建')
    }
    resetSavedSearchForm()
    await loadSavedSearches()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法保存搜索订阅')
  } finally {
    savedSearchSaving.value = false
  }
}

async function toggleSavedSearch(item: SavedSearch) {
  await updateSavedSearchItem(item, { enabled: !item.enabled })
}

async function deleteSavedSearch(id: number) {
  try {
    await apiDelete<{ deleted: boolean }>(`/api/saved-searches/${id}`)
    if (editingSavedSearchID.value === id) resetSavedSearchForm()
    await loadSavedSearches()
    message.success('搜索订阅已删除')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法删除搜索订阅')
  }
}

async function testSavedSearch(id: number) {
  try {
    const data = await apiPost<{ total: number }>(`/api/saved-searches/${id}/test`)
    message.success(`匹配 ${data.total} 条资源`)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法测试搜索订阅')
  }
}

function editSavedSearch(item: SavedSearch) {
  editingSavedSearchID.value = item.id
  savedSearchForm.value = {
    name: item.name,
    keyword: item.keyword,
    category: item.filters.category ?? '',
    resourceTypes: [...new Set([...(item.filters.cloud_types ?? []), item.filters.type ?? ''].filter(Boolean))],
    accountID: item.filters.account_id ?? 0,
    channelID: item.filters.channel_id ?? 0,
    telegramChatIDs: item.telegram_chat_ids ?? [],
    notifyRSS: item.notify_rss,
    notifyWebhook: item.notify_webhook,
    notifyTelegram: item.notify_telegram,
    enabled: item.enabled
  }
}

function resetSavedSearchForm() {
  editingSavedSearchID.value = null
  savedSearchForm.value = {
    name: '',
    keyword: '',
    category: '',
    resourceTypes: [],
    accountID: 0,
    channelID: 0,
    telegramChatIDs: [],
    notifyRSS: true,
    notifyWebhook: false,
    notifyTelegram: false,
    enabled: true
  }
}

async function updateSavedSearchItem(item: SavedSearch, patch: Partial<SavedSearch>) {
  try {
    await apiPut<SavedSearch>(`/api/saved-searches/${item.id}`, {
      name: patch.name ?? item.name,
      keyword: patch.keyword ?? item.keyword,
      filters: patch.filters ?? item.filters,
      notify_rss: patch.notify_rss ?? item.notify_rss,
      notify_webhook: patch.notify_webhook ?? item.notify_webhook,
      notify_telegram: patch.notify_telegram ?? item.notify_telegram,
      telegram_chat_ids: item.telegram_chat_ids ?? [],
      enabled: patch.enabled ?? item.enabled
    })
    await loadSavedSearches()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法更新搜索订阅')
  }
}

function savedSearchPayload() {
  const filters: Record<string, unknown> = {}
  const category = savedSearchForm.value.category.trim()
  const resourceTypes = savedSearchForm.value.resourceTypes
  if (category) filters.category = category
  if (resourceTypes.length) filters.cloud_types = resourceTypes
  if (savedSearchForm.value.accountID > 0) filters.account_id = savedSearchForm.value.accountID
  if (savedSearchForm.value.channelID > 0) filters.channel_id = savedSearchForm.value.channelID
  return {
    name: savedSearchForm.value.name.trim(),
    keyword: savedSearchForm.value.keyword.trim(),
    filters,
    notify_rss: savedSearchForm.value.notifyRSS,
    notify_webhook: savedSearchForm.value.notifyWebhook,
    notify_telegram: savedSearchForm.value.notifyTelegram,
    telegram_chat_ids: savedSearchForm.value.notifyTelegram ? savedSearchForm.value.telegramChatIDs : [],
    enabled: savedSearchForm.value.enabled
  }
}

function telegramBotChatLabel(chat: TelegramBotChat) {
  const name = chat.title || (chat.username ? `@${chat.username}` : [chat.first_name, chat.last_name].filter(Boolean).join(' '))
  const base = name || String(chat.chat_id)
  return chat.type ? `${base} (${telegramChatTypeLabel(chat.type)})` : base
}

function telegramChatTypeLabel(type: string) {
  return telegramChatTypeLabels[type] ?? type
}

function notificationEventLabel(type: string) {
  return notificationEventLabels[type] ?? type
}

function notificationTargetLabel(type: string) {
  return notificationTargetLabels[type] ?? type
}

function notificationDeliveryStatusLabel(status: string) {
  return notificationDeliveryStatusLabels[status] ?? status
}

function savedSearchNotificationLabels(item: SavedSearch) {
  return [
    item.notify_rss ? 'RSS 订阅' : '',
    item.notify_webhook ? 'Webhook' : '',
    item.notify_telegram ? 'Telegram 消息' : ''
  ].filter(Boolean).join(' / ') || '-'
}

function webhookEventLabels(events: string[]) {
  return events.map((event) => notificationEventLabel(event)).join('，')
}

async function saveWebhook() {
  const url = webhookForm.value.url.trim()
  if (!url) {
    message.error('Webhook URL 不能为空')
    return
  }
  if (webhookForm.value.events.length === 0) {
    message.error('至少选择一个事件')
    return
  }
  webhookSaving.value = true
  try {
    const payload = webhookPayload()
    if (editingWebhookID.value) {
      await apiPut<Webhook>(`/api/webhooks/${editingWebhookID.value}`, payload)
      message.success('Webhook 已更新')
    } else {
      await apiPost<Webhook>('/api/webhooks', payload)
      message.success('Webhook 已创建')
    }
    resetWebhookForm()
    await loadWebhooks()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法保存 Webhook')
  } finally {
    webhookSaving.value = false
  }
}

async function toggleWebhook(item: Webhook) {
  await updateWebhookItem(item, { enabled: !item.enabled })
}

async function deleteWebhook(id: number) {
  try {
    await apiDelete<{ deleted: boolean }>(`/api/webhooks/${id}`)
    if (editingWebhookID.value === id) resetWebhookForm()
    await loadWebhooks()
    message.success('Webhook 已删除')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法删除 Webhook')
  }
}

function editWebhook(item: Webhook) {
  editingWebhookID.value = item.id
  webhookForm.value = {
    name: item.name,
    url: item.url,
    secret: '',
    enabled: item.enabled,
    events: [...item.events]
  }
}

function resetWebhookForm() {
  editingWebhookID.value = null
  webhookForm.value = {
    name: '',
    url: '',
    secret: '',
    enabled: true,
    events: ['resource.created']
  }
}

async function updateWebhookItem(item: Webhook, patch: Partial<Webhook>) {
  try {
    await apiPut<Webhook>(`/api/webhooks/${item.id}`, {
      name: patch.name ?? item.name,
      url: patch.url ?? item.url,
      events: patch.events ?? item.events,
      enabled: patch.enabled ?? item.enabled
    })
    await loadWebhooks()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法更新 Webhook')
  }
}

function webhookPayload() {
  const payload: Record<string, unknown> = {
    name: webhookForm.value.name.trim(),
    url: webhookForm.value.url.trim(),
    events: webhookForm.value.events,
    enabled: webhookForm.value.enabled
  }
  const secret = webhookForm.value.secret.trim()
  if (secret) payload.secret = secret
  return payload
}

function toggleWebhookEvent(event: string, checked: boolean) {
  const set = new Set(webhookForm.value.events)
  if (checked) {
    set.add(event)
  } else {
    set.delete(event)
  }
  webhookForm.value.events = Array.from(set)
}

function onWebhookEventChange(event: string, domEvent: Event) {
  toggleWebhookEvent(event, Boolean((domEvent.target as HTMLInputElement | null)?.checked))
}

async function updateRuntimeSettings() {
  let payload: RuntimeSettings
  try {
    payload = runtimePayload()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '运行参数格式无效')
    return
  }
  runtimeLoading.value = true
  try {
    const mediaConcurrencyChanged =
      Boolean(currentRuntimeSettings.value) &&
      currentRuntimeSettings.value?.telegram.media.concurrency !== payload.telegram.media.concurrency
    const saved = await apiPut<RuntimeSettings>('/api/settings/runtime', payload)
    fillRuntimeForm(saved)
    await loadStorageUsage()
    message.success(mediaConcurrencyChanged ? '媒体下载并发已立即生效，其余运行参数重启后生效' : '运行参数已保存，重启后生效')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法保存运行参数')
  } finally {
    runtimeLoading.value = false
  }
}

async function loadAIModels(provider?: AIMediaMetadataProviderSettings) {
  const baseURL = (provider?.base_url ?? runtimeForm.value.aiBaseURL).trim()
  if (!baseURL) {
    message.error('请输入 AI Base URL')
    return
  }
  aiModelsLoading.value = true
  try {
    const data = await apiPost<AIModelsResponse>('/api/settings/ai/models', {
      provider: provider?.provider ?? runtimeForm.value.aiProvider,
      base_url: baseURL,
      api_key: (provider?.api_key ?? runtimeForm.value.aiAPIKey).trim()
    })
    aiModels.value = Array.isArray(data.items) ? data.items : []
    if (provider && !provider.model.trim() && aiModels.value.length > 0) {
      provider.model = aiModels.value[0]
    } else if (!provider && !runtimeForm.value.aiModel.trim() && aiModels.value.length > 0) {
      runtimeForm.value.aiModel = aiModels.value[0]
    }
    message.success('AI 模型列表已更新')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法拉取 AI 模型列表')
  } finally {
    aiModelsLoading.value = false
  }
}

function applyAIProviderRowPreset(provider: AIMediaMetadataProviderSettings, providerID: string) {
  const preset = aiProviders.value.find((item) => item.id === providerID)
  provider.provider = providerID
  if (!preset) return
  provider.base_url = preset.base_url
  provider.model = preset.default_model
  provider.name = preset.name
  if (!preset.requires_api_key) {
    provider.api_key = ''
  }
}

function addAIProvider() {
  const preset = aiProviders.value[0]
  const id = `provider-${Date.now()}`
  runtimeForm.value.aiProviders.push({
    id,
    name: preset?.name ?? '',
    provider: preset?.id ?? 'openai_compatible',
    base_url: preset?.base_url ?? '',
    api_key: '',
    model: preset?.default_model ?? '',
    enabled: true
  })
}

function removeAIProvider(index: number) {
  runtimeForm.value.aiProviders.splice(index, 1)
}

function moveAIProvider(index: number, direction: -1 | 1) {
  const next = index + direction
  if (next < 0 || next >= runtimeForm.value.aiProviders.length) return
  const items = runtimeForm.value.aiProviders
  const [item] = items.splice(index, 1)
  items.splice(next, 0, item)
}

async function testAIProvider(provider: AIMediaMetadataProviderSettings) {
  const baseURL = provider.base_url.trim()
  if (!baseURL) {
    message.error('请输入 AI Base URL')
    return
  }
  if (!provider.model.trim()) {
    message.error('请输入 AI 模型')
    return
  }
  try {
    const data = await apiPost<AITestResponse>('/api/settings/ai/test', {
      id: provider.id,
      provider: provider.provider,
      base_url: baseURL,
      api_key: (provider.api_key ?? '').trim(),
      model: provider.model.trim()
    })
    message.success(`AI Provider 有回复，耗时 ${data.latency_ms}ms`)
  } catch (error) {
    message.error(error instanceof Error ? error.message : 'AI Provider 无回复')
  }
}

async function regenerate() {
  try {
    await apiKey.regenerate()
    showAPIKey.value = false
    message.success('API 密钥已重新生成')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法重新生成 API 密钥')
  }
}

function toggleAPIKeyVisibility() {
  showAPIKey.value = !showAPIKey.value
}

async function copyAPIKey() {
  if (!apiKey.current?.key) return
  try {
    // 尝试使用现代 Clipboard API
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(apiKey.current.key)
      message.success('API 密钥已复制到剪贴板')
      return
    }

    // 降级方案：使用传统的 execCommand
    const textarea = document.createElement('textarea')
    textarea.value = apiKey.current.key
    textarea.style.position = 'fixed'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.select()
    const successful = document.execCommand('copy')
    document.body.removeChild(textarea)

    if (successful) {
      message.success('API 密钥已复制到剪贴板')
    } else {
      message.error('复制失败，请手动复制')
    }
  } catch (error) {
    message.error('复制失败，请手动复制')
  }
}

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString() : '-'
}

function formatCount(value = 0) {
  return value.toLocaleString()
}

function formatBytes(value = 0) {
  if (value >= sizeUnitMultipliers.GB) return `${(value / sizeUnitMultipliers.GB).toFixed(1)} GB`
  if (value >= sizeUnitMultipliers.MB) return `${(value / sizeUnitMultipliers.MB).toFixed(1)} MB`
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${value} B`
}

function splitSizeForInput(bytes: number) {
  if (bytes > 0 && bytes % sizeUnitMultipliers.GB === 0) {
    return { value: String(bytes / sizeUnitMultipliers.GB), unit: 'GB' as SizeUnit }
  }
  return { value: String(Math.ceil(bytes / sizeUnitMultipliers.MB)), unit: 'MB' as SizeUnit }
}

function fillRuntimeForm(settings: RuntimeSettings) {
  currentRuntimeSettings.value = settings
  const maxDBSize = splitSizeForInput(settings.storage.max_db_size)
  const maxMediaCache = splitSizeForInput(settings.storage.max_media_cache)
  const aiMedia = settings.ai?.media_metadata
  runtimeForm.value = {
    workers: String(settings.sync.workers),
    historyBatchSize: String(settings.sync.history_batch_size),
    telegramRequestInterval: settings.sync.telegram_request_interval,
    maxDBSize: maxDBSize.value,
    maxDBSizeUnit: maxDBSize.unit,
    maxMediaCache: maxMediaCache.value,
    maxMediaCacheUnit: maxMediaCache.unit,
    proxy: settings.telegram.proxy,
    reconnectTimeout: settings.telegram.reconnect_timeout,
    dialTimeout: settings.telegram.dial_timeout,
    rateLimitEnabled: settings.telegram.rate_limit.enabled,
    ratePerSecond: String(settings.telegram.rate_limit.rate_per_second),
    burst: String(settings.telegram.rate_limit.burst),
    streamConcurrency: String(settings.telegram.stream.concurrency),
    streamBuffers: String(settings.telegram.stream.buffers),
    streamChunkTimeout: settings.telegram.stream.chunk_timeout,
    mediaConcurrency: String(settings.telegram.media.concurrency),
    aiMediaEnabled: Boolean(aiMedia?.enabled),
    aiProvider: aiMedia?.provider || 'openai_compatible',
    aiBaseURL: aiMedia?.base_url ?? '',
    aiAPIKey: '',
    aiModel: aiMedia?.model ?? '',
    aiFallbackEnabled: Boolean(aiMedia?.fallback_enabled),
    aiProviders: normalizeAIProviderRows(aiMedia)
  }
  aiModels.value = aiMedia?.model ? [aiMedia.model] : []
}

function normalizeAIProviderRows(aiMedia: RuntimeSettings['ai']['media_metadata'] | undefined): AIMediaMetadataProviderSettings[] {
  if (Array.isArray(aiMedia?.providers) && aiMedia.providers.length > 0) {
    return aiMedia.providers.map((provider, index) => ({
      id: provider.id || `${provider.provider || 'provider'}-${index}`,
      name: provider.name ?? '',
      provider: provider.provider || 'openai_compatible',
      base_url: provider.base_url ?? '',
      api_key: '',
      api_key_set: provider.api_key_set,
      model: provider.model ?? '',
      enabled: provider.enabled !== false
    }))
  }
  if (!aiMedia) return []
  return [{
    id: aiMedia.provider || 'openai_compatible',
    provider: aiMedia.provider || 'openai_compatible',
    base_url: aiMedia.base_url ?? '',
    api_key: '',
    api_key_set: aiMedia.api_key_set,
    model: aiMedia.model ?? '',
    enabled: true
  }]
}

function runtimePayload(): RuntimeSettings {
  const firstProvider = runtimeForm.value.aiProviders[0]
  return {
    sync: {
      workers: positiveInteger(runtimeForm.value.workers),
      history_batch_size: positiveInteger(runtimeForm.value.historyBatchSize),
      telegram_request_interval: runtimeForm.value.telegramRequestInterval.trim()
    },
    storage: {
      max_db_size: sizeLimitBytes(runtimeForm.value.maxDBSize, runtimeForm.value.maxDBSizeUnit),
      max_media_cache: sizeLimitBytes(runtimeForm.value.maxMediaCache, runtimeForm.value.maxMediaCacheUnit)
    },
    telegram: {
      proxy: runtimeForm.value.proxy.trim(),
      reconnect_timeout: runtimeForm.value.reconnectTimeout.trim(),
      dial_timeout: runtimeForm.value.dialTimeout.trim(),
      rate_limit: {
        enabled: runtimeForm.value.rateLimitEnabled,
        rate_per_second: positiveInteger(runtimeForm.value.ratePerSecond),
        burst: positiveInteger(runtimeForm.value.burst)
      },
      stream: {
        concurrency: positiveInteger(runtimeForm.value.streamConcurrency),
        buffers: positiveInteger(runtimeForm.value.streamBuffers),
        chunk_timeout: runtimeForm.value.streamChunkTimeout.trim()
      },
      media: {
        concurrency: positiveInteger(runtimeForm.value.mediaConcurrency)
      }
    },
    ai: {
      media_metadata: {
        enabled: runtimeForm.value.aiMediaEnabled,
        provider: firstProvider?.provider ?? runtimeForm.value.aiProvider,
        base_url: firstProvider?.base_url.trim() ?? runtimeForm.value.aiBaseURL.trim(),
        api_key: firstProvider?.api_key?.trim() ?? runtimeForm.value.aiAPIKey.trim(),
        model: firstProvider?.model.trim() ?? runtimeForm.value.aiModel.trim(),
        fallback_enabled: runtimeForm.value.aiFallbackEnabled,
        providers: runtimeForm.value.aiProviders.map((provider) => ({
          id: provider.id,
          name: provider.name,
          provider: provider.provider,
          base_url: provider.base_url.trim(),
          api_key: (provider.api_key ?? '').trim(),
          model: provider.model.trim(),
          enabled: provider.enabled
        }))
      }
    }
  }
}

function positiveInteger(value: string) {
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error('invalid positive integer')
  }
  return parsed
}

function sizeLimitBytes(value: string, unit: SizeUnit) {
  const bytes = positiveInteger(value) * sizeUnitMultipliers[unit]
  if (bytes < minStorageLimitBytes) {
    throw new Error('storage limit must be at least 100MB')
  }
  return bytes
}

function versionStatusText() {
  if (versionError.value) return '检查失败'
  if (!versionInfo.value?.latest_version) return '尚未检查'
  if (versionInfo.value.update_available) return `发现新版本 ${versionInfo.value.latest_version}`
  return '已是最新版本'
}
</script>

<template>
  <section class="page-section">
    <div class="page-header">
      <div>
        <p class="page-kicker">配置</p>
        <h1 class="page-title">设置</h1>
        <p class="page-subtitle">管理账号、安全凭据、存储限额和运行参数。</p>
      </div>
    </div>
    <n-tabs v-model:value="activeTab" type="line" animated class="settings-tabs">
      <n-tab-pane name="security" tab="账号与安全">
        <div class="settings-panel-grid security-grid">
          <div class="security-column-left">
            <section class="panel admin-panel">
              <h2>管理员账号</h2>
              <n-form class="credential-form" @submit.prevent="updateCredentials">
                <n-form-item label="用户名">
                  <n-input
                    v-model:value="credentialsUsername"
                    data-testid="admin-username-input"
                    autocomplete="username"
                    placeholder="请输入用户名"
                  />
                </n-form-item>
                <n-form-item label="当前密码">
                  <n-input
                    v-model:value="currentPassword"
                    data-testid="current-password-input"
                    type="password"
                    autocomplete="current-password"
                    placeholder="请输入密码"
                  />
                </n-form-item>
                <n-form-item label="新密码">
                  <n-input
                    v-model:value="newPassword"
                    data-testid="new-password-input"
                    type="password"
                    autocomplete="new-password"
                    placeholder="留空则不修改"
                  />
                </n-form-item>
                <n-form-item label="确认新密码">
                  <n-input
                    v-model:value="confirmPassword"
                    data-testid="confirm-password-input"
                    type="password"
                    autocomplete="new-password"
                    placeholder="留空则不修改"
                  />
                </n-form-item>
                <div class="form-actions">
                  <n-button
                    data-testid="save-admin-credentials"
                    type="primary"
                    :loading="credentialsLoading"
                    @click="updateCredentials"
                  >
                    保存
                  </n-button>
                </div>
              </n-form>
            </section>
          </div>
          <div class="security-column-right">
            <section class="panel api-key-panel">
              <div class="panel-header">
                <h2>API 密钥</h2>
                <n-button data-testid="regenerate-api-key" size="small" type="primary" :loading="apiKey.loading" @click="regenerate">
                  重新生成
                </n-button>
              </div>
              <dl v-if="apiKey.current">
                <div>
                  <dt>创建时间</dt>
                  <dd>{{ formatTime(apiKey.current.created_at) }}</dd>
                </div>
                <div>
                  <dt>最后使用</dt>
                  <dd>{{ formatTime(apiKey.current.last_used_at) }}</dd>
                </div>
                <div>
                  <dt>使用次数</dt>
                  <dd data-testid="api-key-usage-count">{{ formatCount(apiKey.current.usage_count) }}</dd>
                </div>
              </dl>
              <div v-if="apiKey.current" class="api-key-field">
                <input
                  data-testid="api-key-input"
                  class="api-key-input"
                  :type="showAPIKey ? 'text' : 'password'"
                  :value="apiKey.current.key"
                  readonly
                  autocomplete="off"
                />
                <n-button
                  data-testid="toggle-api-key-visibility"
                  size="small"
                  secondary
                  @click="toggleAPIKeyVisibility"
                >
                  {{ showAPIKey ? '隐藏' : '显示' }}
                </n-button>
                <n-button
                  data-testid="copy-api-key"
                  size="small"
                  secondary
                  @click="copyAPIKey"
                >
                  复制
                </n-button>
              </div>
              <div v-else class="loading-stack" aria-label="正在加载 API 密钥">
                <span class="skeleton-line" />
                <span class="skeleton-line short" />
              </div>
            </section>
            <section class="panel telegram-panel">
              <h2>Telegram API</h2>
              <n-form class="telegram-form" @submit.prevent="updateTelegramAPI">
                <n-form-item label="App ID">
                  <n-input
                    v-model:value="telegramAppID"
                    data-testid="telegram-app-id-input"
                    inputmode="numeric"
                    placeholder="请输入 App ID"
                  />
                </n-form-item>
                <n-form-item label="App Hash">
                  <n-input
                    v-model:value="telegramAppHash"
                    data-testid="telegram-app-hash-input"
                    type="password"
                    autocomplete="off"
                    :placeholder="telegramSettings?.app_hash_set ? '已设置，输入新 Hash 保存' : '请输入 App Hash'"
                  />
                </n-form-item>
                <div class="form-actions">
                  <n-button
                    data-testid="save-telegram-api"
                    type="primary"
                    :loading="telegramLoading"
                    @click="updateTelegramAPI"
                  >
                    保存
                  </n-button>
                </div>
              </n-form>
            </section>
          </div>
        </div>
      </n-tab-pane>

      <n-tab-pane name="storage" tab="存储">
        <section class="panel storage-panel">
          <h2>存储</h2>
          <dl>
            <div>
              <dt>最大数据库容量</dt>
              <dd>{{ formatBytes(storageUsage?.max_db_bytes) }}</dd>
            </div>
            <div>
              <dt>最大媒体缓存</dt>
              <dd>{{ formatBytes(storageUsage?.max_media_bytes) }}</dd>
            </div>
          </dl>
          <n-form class="runtime-form storage-limit-form" @submit.prevent="updateRuntimeSettings">
            <n-form-item label="数据库容量上限">
              <div class="size-limit-row">
                <n-input
                  v-model:value="runtimeForm.maxDBSize"
                  data-testid="runtime-max-db-size-input"
                  inputmode="numeric"
                  placeholder="10"
                />
                <n-select
                  v-model:value="runtimeForm.maxDBSizeUnit"
                  data-testid="runtime-max-db-size-unit"
                  :options="sizeUnitOptions"
                />
              </div>
            </n-form-item>
            <n-form-item label="媒体缓存上限">
              <div class="size-limit-row">
                <n-input
                  v-model:value="runtimeForm.maxMediaCache"
                  data-testid="runtime-max-media-cache-input"
                  inputmode="numeric"
                  placeholder="20"
                />
                <n-select
                  v-model:value="runtimeForm.maxMediaCacheUnit"
                  data-testid="runtime-max-media-cache-unit"
                  :options="sizeUnitOptions"
                />
              </div>
            </n-form-item>
            <div class="form-actions">
              <n-button data-testid="save-runtime-storage" type="primary" :loading="runtimeLoading" @click="updateRuntimeSettings">
                保存
              </n-button>
            </div>
          </n-form>
        </section>
      </n-tab-pane>

      <n-tab-pane name="runtime" tab="运行参数">
        <section class="panel runtime-panel">
          <div class="panel-header">
            <h2>运行参数</h2>
            <span class="restart-note">除媒体下载并发外，保存后重启生效</span>
          </div>
          <n-form class="runtime-form" @submit.prevent="updateRuntimeSettings">
            <div class="runtime-section">
              <h3>同步</h3>
              <div class="runtime-grid">
                <n-form-item label="同步 workers">
                  <n-input v-model:value="runtimeForm.workers" data-testid="runtime-workers-input" inputmode="numeric" />
                </n-form-item>
                <n-form-item label="历史批量大小">
                  <n-input
                    v-model:value="runtimeForm.historyBatchSize"
                    data-testid="runtime-history-batch-size-input"
                    inputmode="numeric"
                  />
                </n-form-item>
                <n-form-item label="Telegram 请求间隔">
                  <n-input v-model:value="runtimeForm.telegramRequestInterval" data-testid="runtime-request-interval-input" />
                </n-form-item>
              </div>
            </div>

            <div class="runtime-section">
              <h3>Telegram 网络</h3>
              <div class="runtime-grid">
                <n-form-item label="代理">
                  <n-input v-model:value="runtimeForm.proxy" data-testid="runtime-proxy-input" placeholder="socks5://127.0.0.1:1080" />
                </n-form-item>
                <n-form-item label="重连超时">
                  <n-input v-model:value="runtimeForm.reconnectTimeout" data-testid="runtime-reconnect-timeout-input" />
                </n-form-item>
                <n-form-item label="拨号超时">
                  <n-input v-model:value="runtimeForm.dialTimeout" data-testid="runtime-dial-timeout-input" />
                </n-form-item>
              </div>
            </div>

            <div class="runtime-section">
              <h3>限速</h3>
              <label class="checkbox-row">
                <input
                  v-model="runtimeForm.rateLimitEnabled"
                  data-testid="runtime-rate-enabled-input"
                  type="checkbox"
                />
                启用 Telegram 请求限速
              </label>
              <div class="runtime-grid">
                <n-form-item label="每秒请求数">
                  <n-input v-model:value="runtimeForm.ratePerSecond" data-testid="runtime-rate-per-second-input" inputmode="numeric" />
                </n-form-item>
                <n-form-item label="突发容量">
                  <n-input v-model:value="runtimeForm.burst" data-testid="runtime-rate-burst-input" inputmode="numeric" />
                </n-form-item>
              </div>
            </div>

            <div class="runtime-section">
              <h3>媒体与流式读取</h3>
              <div class="runtime-grid">
                <n-form-item label="流式并发">
                  <n-input v-model:value="runtimeForm.streamConcurrency" data-testid="runtime-stream-concurrency-input" inputmode="numeric" />
                </n-form-item>
                <n-form-item label="预取缓冲数">
                  <n-input v-model:value="runtimeForm.streamBuffers" data-testid="runtime-stream-buffers-input" inputmode="numeric" />
                </n-form-item>
                <n-form-item label="分片超时">
                  <n-input v-model:value="runtimeForm.streamChunkTimeout" data-testid="runtime-stream-timeout-input" />
                </n-form-item>
                <n-form-item label="媒体下载并发">
                  <n-input v-model:value="runtimeForm.mediaConcurrency" data-testid="runtime-media-concurrency-input" inputmode="numeric" />
                </n-form-item>
              </div>
            </div>

            <div class="form-actions">
              <n-button data-testid="save-runtime-settings" type="primary" :loading="runtimeLoading" @click="updateRuntimeSettings">
                保存
              </n-button>
            </div>
          </n-form>
        </section>
      </n-tab-pane>

      <n-tab-pane name="ai" tab="AI增强">
        <section class="panel runtime-panel">
          <div class="panel-header">
            <h2>AI 媒体元数据</h2>
          </div>
          <n-form class="runtime-form" @submit.prevent="updateRuntimeSettings">
            <div class="runtime-section">
              <label class="checkbox-row">
                <input
                  v-model="runtimeForm.aiMediaEnabled"
                  data-testid="ai-media-enabled-input"
                  type="checkbox"
                />
                启用 AI 识别
              </label>
              <div class="runtime-grid">
                <n-form-item label="启用 Provider">
                  <span data-testid="ai-enabled-provider-count">{{ enabledAIProviders.length }}</span>
                </n-form-item>
              </div>
              <div class="ai-provider-list">
                <div v-for="(provider, index) in runtimeForm.aiProviders" :key="provider.id" class="ai-provider-row">
                  <label class="checkbox-row">
                    <input v-model="provider.enabled" :data-testid="`ai-provider-enabled-${index}`" type="checkbox" />
                    启用
                  </label>
                  <n-form-item label="Provider">
                    <n-select
                      v-model:value="provider.provider"
                      :data-testid="`ai-provider-input-${index}`"
                      :options="aiProviderOptions"
                      @update:value="(value: string) => applyAIProviderRowPreset(provider, value)"
                    />
                  </n-form-item>
                  <n-form-item label="Base URL">
                    <n-input v-model:value="provider.base_url" :data-testid="`ai-base-url-input-${index}`" placeholder="https://api.openai.com/v1" />
                  </n-form-item>
                  <n-form-item label="API Key">
                    <n-input
                      v-model:value="provider.api_key"
                      :data-testid="`ai-api-key-input-${index}`"
                      type="password"
                      autocomplete="off"
                      :placeholder="provider.api_key_set ? '已设置，留空则保留' : '请输入 API Key'"
                    />
                  </n-form-item>
                  <n-form-item label="模型">
                    <n-select
                      v-model:value="provider.model"
                      :data-testid="`ai-model-input-${index}`"
                      filterable
                      tag
                      :options="aiProviderModelOptions(provider)"
                    />
                  </n-form-item>
                  <div class="ai-provider-row-actions">
                    <n-button :data-testid="`test-ai-provider-${index}`" secondary @click="testAIProvider(provider)">测试 Provider</n-button>
                    <n-button :data-testid="`fetch-ai-models-${index}`" secondary :loading="aiModelsLoading" @click="loadAIModels(provider)">模型</n-button>
                    <n-button :data-testid="`move-ai-provider-up-${index}`" secondary :disabled="index === 0" @click="moveAIProvider(index, -1)">上移</n-button>
                    <n-button :data-testid="`move-ai-provider-down-${index}`" secondary :disabled="index === runtimeForm.aiProviders.length - 1" @click="moveAIProvider(index, 1)">下移</n-button>
                    <n-button :data-testid="`remove-ai-provider-${index}`" secondary @click="removeAIProvider(index)">移除 Provider</n-button>
                  </div>
                  <div class="ai-provider-meta" v-if="aiProviders.find((item) => item.id === provider.provider)">
                    <a
                      :data-testid="`ai-provider-website-${index}`"
                      :href="aiProviders.find((item) => item.id === provider.provider)?.website"
                      rel="noreferrer"
                      target="_blank"
                    >
                      官网 / API Key
                    </a>
                    <span v-if="aiProviders.find((item) => item.id === provider.provider)?.api_key_env">环境变量：{{ aiProviders.find((item) => item.id === provider.provider)?.api_key_env }}</span>
                  </div>
                </div>
              </div>
              <label class="checkbox-row">
                <input
                  v-model="runtimeForm.aiFallbackEnabled"
                  data-testid="ai-fallback-enabled-input"
                  type="checkbox"
                />
                启用 Provider fallback
              </label>
              <div class="form-actions compact-actions">
                <n-button data-testid="add-ai-provider" secondary @click="addAIProvider">
                  添加 Provider
                </n-button>
              </div>
            </div>

            <div class="form-actions">
              <n-button data-testid="save-runtime-settings" type="primary" :loading="runtimeLoading" @click="updateRuntimeSettings">
                保存
              </n-button>
            </div>
          </n-form>
        </section>
      </n-tab-pane>

      <n-tab-pane name="notifications" tab="通知集成">
        <div class="notification-layout">
          <section class="panel bot-panel">
            <div class="panel-header">
              <h2>Telegram 机器人</h2>
            </div>
            <n-form class="notification-form bot-form" @submit.prevent="updateTelegramBot">
              <label class="checkbox-row">
                <input v-model="telegramBotForm.enabled" data-testid="telegram-bot-enabled-input" type="checkbox" />
                启用机器人
              </label>
              <div class="notification-grid">
                <n-form-item label="机器人令牌">
                  <n-input
                    v-model:value="telegramBotForm.token"
                    data-testid="telegram-bot-token-input"
                    type="password"
                    autocomplete="off"
                    :placeholder="telegramBotSettings?.token_set ? '已设置，输入新令牌后保存' : '请输入机器人令牌'"
                  />
                </n-form-item>
                <n-form-item label="轮询间隔">
                  <n-input v-model:value="telegramBotForm.pollInterval" data-testid="telegram-bot-poll-interval-input" placeholder="3s" />
                </n-form-item>
              </div>
              <div class="form-actions">
                <n-button data-testid="save-telegram-bot" type="primary" :loading="telegramBotLoading" @click="updateTelegramBot">
                  保存
                </n-button>
              </div>
            </n-form>
          </section>

          <div class="settings-panel-grid">
            <section class="panel notification-panel">
              <div class="panel-header">
                <h2>搜索订阅</h2>
                <n-button size="small" secondary @click="resetSavedSearchForm">新建</n-button>
              </div>
              <n-form class="notification-form" @submit.prevent="saveSavedSearch">
                <div class="notification-grid">
                  <n-form-item label="名称">
                    <n-input v-model:value="savedSearchForm.name" data-testid="saved-search-name-input" placeholder="默认使用关键词" />
                  </n-form-item>
                  <n-form-item label="关键词">
                    <n-input v-model:value="savedSearchForm.keyword" data-testid="saved-search-keyword-input" placeholder="哪吒3" />
                  </n-form-item>
                  <n-form-item label="资源大类">
                    <n-select
                      v-model:value="savedSearchForm.category"
                      data-testid="saved-search-category-select"
                      :options="savedSearchCategoryOptions"
                    />
                  </n-form-item>
                  <n-form-item label="资源类型/网盘">
                    <n-select
                      v-model:value="savedSearchForm.resourceTypes"
                      data-testid="saved-search-resource-types-select"
                      multiple
                      filterable
                      clearable
                      :options="savedSearchResourceTypeOptions"
                    />
                  </n-form-item>
                  <n-form-item label="账号">
                    <n-select
                      v-model:value="savedSearchForm.accountID"
                      data-testid="saved-search-account-select"
                      filterable
                      :options="accountOptions"
                    />
                  </n-form-item>
                  <n-form-item label="频道">
                    <n-select
                      v-model:value="savedSearchForm.channelID"
                      data-testid="saved-search-channel-select"
                      filterable
                      :options="channelOptions"
                    />
                  </n-form-item>
                  <n-form-item v-if="savedSearchForm.notifyTelegram" class="full-row" label="Telegram 接收人">
                    <n-select
                      v-model:value="savedSearchForm.telegramChatIDs"
                      data-testid="saved-search-telegram-chats-select"
                      multiple
                      filterable
                      clearable
                      :loading="telegramBotChatsLoading"
                      :options="telegramBotChatOptions"
                      placeholder="选择已和机器人对话的接收人"
                    />
                    <p v-if="!telegramBotChatsLoading && telegramBotChats.length === 0" class="form-hint">
                      先在 Telegram 里给机器人发送 /start 后可选择接收人。
                    </p>
                  </n-form-item>
                </div>
                <div class="checkbox-grid">
                  <label class="checkbox-row">
                    <input v-model="savedSearchForm.notifyRSS" data-testid="saved-search-notify-rss-input" type="checkbox" />
                    RSS
                  </label>
                  <label class="checkbox-row">
                    <input v-model="savedSearchForm.notifyWebhook" data-testid="saved-search-notify-webhook-input" type="checkbox" />
                    Webhook
                  </label>
                  <label class="checkbox-row">
                    <input v-model="savedSearchForm.notifyTelegram" data-testid="saved-search-notify-telegram-input" type="checkbox" />
                    Telegram 消息
                  </label>
                  <label class="checkbox-row">
                    <input v-model="savedSearchForm.enabled" data-testid="saved-search-enabled-input" type="checkbox" />
                    启用
                  </label>
                </div>
                <div class="form-actions">
                  <n-button data-testid="save-saved-search" type="primary" :loading="savedSearchSaving" @click="saveSavedSearch">
                    {{ editingSavedSearchID ? '更新' : '创建' }}
                  </n-button>
                </div>
              </n-form>

              <div class="table-wrap">
                <table class="settings-table">
                  <thead>
                    <tr>
                      <th>名称</th>
                      <th>关键词</th>
                      <th>通知</th>
                      <th>状态</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-if="savedSearchLoading">
                      <td colspan="5">加载中</td>
                    </tr>
                    <tr v-for="item in savedSearches" :key="item.id">
                      <td>{{ item.name }}</td>
                      <td>{{ item.keyword }}</td>
                      <td>{{ savedSearchNotificationLabels(item) }}</td>
                      <td>{{ item.enabled ? '启用' : '停用' }}</td>
                      <td class="table-actions">
                        <n-button size="tiny" secondary @click="editSavedSearch(item)">编辑</n-button>
                        <n-button size="tiny" secondary @click="toggleSavedSearch(item)">{{ item.enabled ? '停用' : '启用' }}</n-button>
                        <n-button size="tiny" secondary @click="testSavedSearch(item.id)">测试</n-button>
                        <n-button size="tiny" tertiary @click="deleteSavedSearch(item.id)">删除</n-button>
                      </td>
                    </tr>
                    <tr v-if="!savedSearchLoading && savedSearches.length === 0">
                      <td colspan="5">暂无搜索订阅</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>

            <section class="panel notification-panel">
              <div class="panel-header">
                <h2>Webhook</h2>
                <n-button size="small" secondary @click="resetWebhookForm">新建</n-button>
              </div>
              <n-form class="notification-form" @submit.prevent="saveWebhook">
                <div class="notification-grid">
                  <n-form-item label="名称">
                    <n-input v-model:value="webhookForm.name" data-testid="webhook-name-input" placeholder="默认使用 URL" />
                  </n-form-item>
                  <n-form-item label="URL">
                    <n-input v-model:value="webhookForm.url" data-testid="webhook-url-input" placeholder="https://example.com/hook" />
                  </n-form-item>
                  <n-form-item label="签名密钥">
                    <n-input
                      v-model:value="webhookForm.secret"
                      data-testid="webhook-secret-input"
                      type="password"
                      autocomplete="off"
                      placeholder="留空则不修改"
                    />
                  </n-form-item>
                </div>
                <div class="event-grid">
                  <label v-for="option in notificationEventOptions" :key="option.value" class="checkbox-row">
                    <input
                      type="checkbox"
                      :checked="webhookForm.events.includes(option.value)"
                      @change="onWebhookEventChange(option.value, $event)"
                    />
                    {{ option.label }}
                  </label>
                </div>
                <label class="checkbox-row">
                  <input v-model="webhookForm.enabled" type="checkbox" />
                  启用
                </label>
                <div class="form-actions">
                  <n-button data-testid="save-webhook" type="primary" :loading="webhookSaving" @click="saveWebhook">
                    {{ editingWebhookID ? '更新' : '创建' }}
                  </n-button>
                </div>
              </n-form>

              <div class="table-wrap">
                <table class="settings-table">
                  <thead>
                    <tr>
                      <th>名称</th>
                      <th>URL</th>
                      <th>事件</th>
                      <th>状态</th>
                      <th>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-if="webhookLoading">
                      <td colspan="5">加载中</td>
                    </tr>
                    <tr v-for="item in webhooks" :key="item.id">
                      <td>{{ item.name }}</td>
                      <td class="url-cell">{{ item.url }}</td>
                      <td>{{ webhookEventLabels(item.events) }}</td>
                      <td>{{ item.enabled ? '启用' : '停用' }}</td>
                      <td class="table-actions">
                        <n-button size="tiny" secondary @click="editWebhook(item)">编辑</n-button>
                        <n-button size="tiny" secondary @click="toggleWebhook(item)">{{ item.enabled ? '停用' : '启用' }}</n-button>
                        <n-button size="tiny" tertiary @click="deleteWebhook(item.id)">删除</n-button>
                      </td>
                    </tr>
                    <tr v-if="!webhookLoading && webhooks.length === 0">
                      <td colspan="5">暂无 Webhook</td>
                    </tr>
                  </tbody>
                </table>
              </div>
            </section>
          </div>

          <section class="panel notification-panel">
            <div class="panel-header">
              <h2>通知记录</h2>
              <n-button size="small" secondary :loading="deliveryLoading" @click="loadDeliveries">刷新</n-button>
            </div>
            <div class="table-wrap">
              <table class="settings-table">
                <thead>
                  <tr>
                    <th>事件</th>
                    <th>目标</th>
                    <th>状态</th>
                    <th>重试</th>
                    <th>时间</th>
                    <th>错误</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-if="deliveryLoading">
                    <td colspan="6">加载中</td>
                  </tr>
                  <tr v-for="item in deliveries" :key="item.id">
                    <td>{{ notificationEventLabel(item.event_type) }}</td>
                    <td>{{ notificationTargetLabel(item.target_type) }} #{{ item.target_id }}</td>
                    <td>{{ notificationDeliveryStatusLabel(item.status) }}</td>
                    <td>{{ item.retry_count }}</td>
                    <td>{{ formatTime(item.created_at) }}</td>
                    <td class="url-cell">{{ item.last_error || '-' }}</td>
                  </tr>
                  <tr v-if="!deliveryLoading && deliveries.length === 0">
                    <td colspan="6">暂无通知记录</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>
        </div>
      </n-tab-pane>

      <n-tab-pane name="system" tab="系统">
        <div class="settings-panel-grid">
          <section class="panel version-panel">
            <div class="panel-header">
              <h2>版本</h2>
              <n-button data-testid="check-version" size="small" type="primary" :loading="versionLoading" @click="loadVersionInfo()">
                检查更新
              </n-button>
            </div>
            <dl>
              <div>
                <dt>当前版本</dt>
                <dd data-testid="current-version">{{ versionInfo?.current_version ?? '-' }}</dd>
              </div>
              <div>
                <dt>更新状态</dt>
                <dd>{{ versionStatusText() }}</dd>
              </div>
            </dl>
            <a
              v-if="versionInfo?.latest_url"
              class="version-link"
              :href="versionInfo.latest_url"
              target="_blank"
              rel="noreferrer"
            >
              查看 GitHub Release
            </a>
          </section>
          <section class="panel system-panel">
            <h2>系统</h2>
            <dl>
              <div>
                <dt>名称</dt>
                <dd data-testid="system-name">{{ systemInfo?.name || '-' }}</dd>
              </div>
              <div>
                <dt>版本</dt>
                <dd>{{ systemInfo?.version || '-' }}</dd>
              </div>
              <div>
                <dt>架构</dt>
                <dd>{{ systemInfo?.architecture || '-' }}</dd>
              </div>
              <div>
                <dt>主机名</dt>
                <dd>{{ systemInfo?.hostname || '-' }}</dd>
              </div>
              <div>
                <dt>CPU</dt>
                <dd>{{ systemInfo?.cpu_count ?? '-' }}</dd>
              </div>
            </dl>
          </section>
          <section class="panel maintenance-panel">
            <div class="panel-header">
              <h2>媒体标题修复</h2>
              <n-button data-testid="repair-media-title" size="small" type="primary" :loading="repairLoading" @click="repairMediaTitles">
                修复
              </n-button>
            </div>
            <p class="panel-hint">修复被网盘标签覆盖的媒体标题（如“光鸭”）。任务在后台运行，进度见「任务」页。</p>
          </section>
        </div>
      </n-tab-pane>
    </n-tabs>
  </section>
</template>

<style scoped>
.settings-tabs {
  width: 100%;
}

.settings-panel-grid {
  align-items: start;
  display: grid;
  gap: 16px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.security-column-left,
.security-column-right {
  display: grid;
  gap: 16px;
}

.credential-form,
.telegram-form,
.runtime-form {
  display: grid;
}

.form-actions {
  display: flex;
  justify-content: flex-end;
}

.panel-header h2 {
  margin: 0;
}

.api-key-panel {
  display: grid;
  gap: 12px;
}

.version-panel {
  display: grid;
  gap: 12px;
}

.system-panel {
  display: grid;
  gap: 12px;
}

.runtime-panel {
  display: grid;
  gap: 16px;
}

.notification-layout,
.notification-panel,
.bot-panel {
  display: grid;
  gap: 16px;
}

.notification-form {
  display: grid;
  gap: 12px;
}

.notification-grid {
  display: grid;
  gap: 12px 16px;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.full-row {
  grid-column: 1 / -1;
}

.form-hint {
  color: var(--app-text-muted);
  font-size: 13px;
  margin: 6px 0 0;
}

.bot-form .notification-grid {
  grid-template-columns: minmax(0, 1fr) 180px;
}

.checkbox-grid,
.event-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
}

.table-wrap {
  overflow-x: auto;
  width: 100%;
}

.settings-table {
  border-collapse: collapse;
  color: var(--app-text);
  font-size: 13px;
  min-width: 680px;
  width: 100%;
}

.settings-table th,
.settings-table td {
  border-bottom: 1px solid var(--app-border-subtle);
  padding: 9px 10px;
  text-align: left;
  vertical-align: top;
}

.settings-table th {
  color: var(--app-text-muted);
  font-weight: 600;
}

.table-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 160px;
}

.url-cell {
  max-width: 260px;
  overflow-wrap: anywhere;
}

.storage-limit-form {
  margin-top: 16px;
}

.size-limit-row {
  display: grid;
  gap: 8px;
  grid-template-columns: minmax(0, 1fr) 92px;
  width: 100%;
}

.runtime-section {
  border-top: 1px solid var(--app-border);
  display: grid;
  gap: 10px;
  padding-top: 14px;
}

.runtime-section:first-child {
  border-top: 0;
  padding-top: 0;
}

.runtime-section h3 {
  color: var(--app-text);
  font-size: 15px;
  margin: 0;
}

.runtime-grid {
  display: grid;
  gap: 12px 16px;
  grid-template-columns: repeat(3, minmax(0, 1fr));
}

.restart-note {
  color: var(--app-text-muted);
  font-size: 13px;
}

.ai-provider-meta {
  align-items: center;
  color: var(--app-text-muted);
  display: flex;
  flex-wrap: wrap;
  font-size: 13px;
  gap: 8px 14px;
}

.ai-provider-meta a {
  color: var(--app-primary);
  font-weight: 600;
  text-decoration: none;
}

.ai-provider-meta a:hover {
  text-decoration: underline;
}

.ai-provider-list {
  display: grid;
  gap: 14px;
}

.ai-provider-row {
  border: 1px solid var(--app-border);
  border-radius: 8px;
  display: grid;
  gap: 10px;
  padding: 12px;
}

.ai-provider-row-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.checkbox-row {
  align-items: center;
  color: var(--app-text);
  display: inline-flex;
  gap: 8px;
  min-height: 32px;
}

.checkbox-row input {
  height: 16px;
  margin: 0;
  width: 16px;
}

.version-link {
  color: var(--app-primary);
  font-weight: 600;
  text-decoration: none;
}

.version-link:hover {
  text-decoration: underline;
}

dl {
  margin: 0;
}

dl div {
  display: flex;
  justify-content: space-between;
  padding: 7px 0;
}

dd {
  font-weight: 600;
  margin: 0;
}

.api-key-field {
  align-items: center;
  background: var(--app-surface-muted);
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  display: grid;
  gap: 8px;
  grid-template-columns: minmax(0, 1fr) auto auto;
  padding: 8px;
}

.api-key-input {
  background: transparent;
  border: 0;
  color: var(--app-text);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace;
  font-size: 14px;
  min-width: 0;
  overflow-wrap: anywhere;
  outline: 0;
  width: 100%;
}

.loading-stack {
  display: grid;
  gap: 8px;
}

.loading-stack .short {
  width: 58%;
}

@media (max-width: 900px) {
  .settings-panel-grid,
  .runtime-grid,
  .notification-grid,
  .bot-form .notification-grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 520px) {
  .api-key-field {
    grid-template-columns: 1fr;
  }

  .size-limit-row {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 760px) {
  .settings-table {
    min-width: auto;
  }

  .checkbox-row input {
    height: 20px;
    width: 20px;
  }

  .checkbox-row {
    min-height: 44px;
  }
}
</style>
