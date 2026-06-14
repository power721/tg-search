<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useDialog } from 'naive-ui'
import type { ListenRulesPayload, TelegramChannel, WatchRule } from '@/api/types'
import Avatar from '@/components/common/Avatar.vue'
import WebAccessBadge from '@/components/channels/WebAccessBadge.vue'
import { useChannelsStore } from '@/stores/channels'
import { normalizePrivateChannelID } from '@/utils/telegramLinks'

const channels = useChannelsStore()
const dialog = useDialog()
const showSyncModal = ref(false)
const showRuleModal = ref(false)
const syncTarget = ref<TelegramChannel | null>(null)
const ruleTarget = ref<TelegramChannel | null>(null)
const ruleScope = ref<'global' | 'channel'>('global')
const syncMaxMessages = ref<number | null>(1000)
const searchQuery = ref('')
const typeFilter = ref('')
const syncStateFilter = ref('')
const listenStateFilter = ref('')
const webAccessFilter = ref('')
const sortKey = ref<'title' | 'username' | 'indexed' | null>(null)
const sortDirection = ref<'asc' | 'desc'>('asc')
const syncingChannelIds = ref(new Set<number>())
const checkingWebAccessChannelIds = ref(new Set<number>())
const listeningChannelIds = ref(new Set<number>())
const clearingChannelIds = ref(new Set<number>())
const batchCheckingWebAccess = ref(false)
const ruleLoading = ref(false)
const ruleSaving = ref(false)
const channelRuleId = ref<number | null>(null)
const canConfirmSync = computed(() => Number.isInteger(syncMaxMessages.value) && Number(syncMaxMessages.value) > 0)

const defaultMessageTypes = ['link', 'text', 'image', 'video', 'audio']
const defaultLinkTypes = ['cloud_drive', 'magnet', 'ed2k', 'other']
const defaultIgnoredLinkPatterns = ['t.me', 'toapp.mypikpak.com', 'telegra.ph', 'www.themoviedb.org']

const ruleForm = ref({
  enabled: true,
  includes: '',
  excludes: '',
  message_types: [...defaultMessageTypes],
  link_types: [...defaultLinkTypes],
  ignored_link_patterns: joinTerms(defaultIgnoredLinkPatterns)
})

const ruleTitle = computed(() => {
  if (ruleScope.value === 'global') return '全局监听规则'
  return `${ruleTarget.value?.title ?? '频道'} 监听规则`
})

const canUseGlobalRule = computed(() => ruleScope.value === 'channel' && channelRuleId.value !== null)

const typeOptions = [
  { label: '全部类型', value: '' },
  { label: '频道', value: 'channel' },
  { label: '群组', value: 'group' },
  { label: '超级群组', value: 'supergroup' },
  { label: '保存的消息', value: 'saved_messages' }
]

const syncStateOptions = [
  { label: '全部同步状态', value: '' },
  { label: '无', value: 'metadata_only' },
  { label: '待同步', value: 'pending' },
  { label: '同步中', value: 'syncing' },
  { label: '已同步', value: 'synced' },
  { label: '同步失败', value: 'failed' },
  { label: '未启用', value: 'disabled' }
]

const listenStateOptions = [
  { label: '全部监听状态', value: '' },
  { label: '监听中', value: 'enabled' },
  { label: '未监听', value: 'disabled' },
  { label: '监听异常', value: 'error' }
]

const webAccessOptions = [
  { label: '全部网页访问', value: '' },
  { label: '可访问', value: 'accessible' },
  { label: '不可访问', value: 'inaccessible' },
  { label: '未检测', value: 'unknown' },
  { label: '检测失败', value: 'error' }
]

const descriptionTooltipStyle = {
  maxWidth: '360px',
  whiteSpace: 'normal',
  overflowWrap: 'anywhere',
  wordBreak: 'break-word',
  lineHeight: '1.5'
}

const filteredChannels = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return channels.items
    .filter((channel) => {
      if (query) {
        const haystack = `${channel.title} ${channel.username}`.toLowerCase()
        if (!haystack.includes(query)) return false
      }
      if (typeFilter.value && channel.type !== typeFilter.value) return false
      if (syncStateFilter.value && channel.sync_state !== syncStateFilter.value) return false
      if (listenStateFilter.value && channel.listen_state !== listenStateFilter.value) return false
      if (webAccessFilter.value && webAccessState(channel) !== webAccessFilter.value) return false
      return true
    })
})
const displayChannels = computed(() => {
  // User hasn't clicked any column header — use backend's default order
  if (sortKey.value === null) {
    return filteredChannels.value
  }

  // User clicked a column header — sort accordingly
  return [...filteredChannels.value].sort(compareChannels)
})
const visibleWebCheckChannelIds = computed(() =>
  filteredChannels.value
    .filter((channel) => canCheckWebAccess(channel))
    .filter((channel) => channel.web_access !== false)
    .map((channel) => channel.id)
    .sort((left, right) => left - right)
)

onMounted(() => {
  void channels.loadChannels()
})

function username(channel: TelegramChannel) {
  return channel.username ? `@${channel.username}` : '-'
}

function channelWebUrl(channel: TelegramChannel) {
  if (!channel.username) return ''
  return `https://t.me/s/${encodeURIComponent(channel.username)}`
}

function channelDeepLink(channel: TelegramChannel) {
  if (channel.username) return `tg://resolve?domain=${encodeURIComponent(channel.username)}`
  const normalized = normalizePrivateChannelID(channel.telegram_channel_id)
  if (normalized) return `tg://privatepost?channel=${normalized}`
  return ''
}

function channelTypeLabel(type: string) {
  const labels: Record<string, string> = {
    channel: '频道',
    group: '群组',
    supergroup: '超级群组',
    saved_messages: '保存的消息'
  }
  return labels[type] ?? type
}

function syncStateLabel(state: string) {
  const labels: Record<string, string> = {
    metadata_only: '无',
    pending: '待同步',
    syncing: '同步中',
    synced: '已同步',
    failed: '同步失败',
    disabled: '未启用'
  }
  return labels[state] ?? state
}

function listenStateLabel(state: string) {
  const labels: Record<string, string> = {
    enabled: '监听中',
    disabled: '未监听',
    error: '监听异常'
  }
  return labels[state] ?? state
}

function syncStateClass(state: string) {
  if (state === 'synced') return 'status-success'
  if (state === 'syncing' || state === 'pending') return 'status-info'
  if (state === 'failed') return 'status-danger'
  return 'status-muted'
}

function listenStateClass(state: string) {
  if (state === 'enabled') return 'status-success'
  if (state === 'error') return 'status-danger'
  return 'status-muted'
}

function webAccessState(channel: TelegramChannel) {
  if (channel.web_access_error) return 'error'
  if (channel.web_access === true) return 'accessible'
  if (channel.web_access === false) return 'inaccessible'
  return 'unknown'
}

function webAccessUrl(channel: TelegramChannel) {
  if (channel.web_access !== true) return ''
  return channelWebUrl(channel)
}

function canCheckWebAccess(channel: TelegramChannel) {
  return channel.type !== 'saved_messages' && Boolean(channel.username)
}

function compareChannels(left: TelegramChannel, right: TelegramChannel) {
  // Default order: by ID (fast numeric comparison)
  if (sortKey.value === null) {
    return left.id - right.id
  }

  const direction = sortDirection.value === 'asc' ? 1 : -1
  let result = 0
  switch (sortKey.value) {
    case 'username':
      result = compareText(left.username, right.username)
      break
    case 'indexed':
      result = left.indexed_message_count - right.indexed_message_count
      break
    case 'title':
    default:
      result = compareText(left.title, right.title)
      break
  }
  return result * direction || compareText(left.title, right.title)
}

function compareText(left: string, right: string) {
  return left.localeCompare(right, 'zh-Hans-CN', { numeric: true, sensitivity: 'base' })
}

function sortBy(key: 'title' | 'username' | 'indexed') {
  if (sortKey.value === key) {
    // Same column clicked - cycle through states
    if (sortDirection.value === 'asc') {
      sortDirection.value = 'desc'
    } else {
      // Third click: reset to default order
      sortKey.value = null
      sortDirection.value = 'asc'
    }
  } else {
    // Different column clicked - start fresh
    sortKey.value = key
    sortDirection.value = 'asc'
  }
}

function sortIndicator(key: 'title' | 'username' | 'indexed') {
  if (sortKey.value !== key) return ''
  return sortDirection.value === 'asc' ? ' ↑' : ' ↓'
}

function syncHistory(channel: TelegramChannel) {
  syncTarget.value = channel
  syncMaxMessages.value = 1000
  showSyncModal.value = true
}

function closeSyncModal() {
  showSyncModal.value = false
  syncTarget.value = null
}

function closeRuleModal() {
  showRuleModal.value = false
  ruleTarget.value = null
  channelRuleId.value = null
}

function setLoadingChannel(target: typeof syncingChannelIds, channelId: number, loading: boolean) {
  const next = new Set(target.value)
  if (loading) {
    next.add(channelId)
  } else {
    next.delete(channelId)
  }
  target.value = next
}

function refreshChannels() {
  return channels.loadChannels()
}

async function confirmSyncHistory() {
  if (!syncTarget.value || !canConfirmSync.value || syncMaxMessages.value === null) return
  const channelId = syncTarget.value.id
  setLoadingChannel(syncingChannelIds, channelId, true)
  try {
    await channels.syncChannels([channelId], syncMaxMessages.value)
    closeSyncModal()
  } finally {
    setLoadingChannel(syncingChannelIds, channelId, false)
  }
}

async function checkWebAccess(channel: TelegramChannel) {
  if (!canCheckWebAccess(channel)) return
  setLoadingChannel(checkingWebAccessChannelIds, channel.id, true)
  try {
    await channels.checkWebAccess([channel.id])
  } finally {
    setLoadingChannel(checkingWebAccessChannelIds, channel.id, false)
  }
}

async function batchCheckWebAccess() {
  if (visibleWebCheckChannelIds.value.length === 0) return
  batchCheckingWebAccess.value = true
  try {
    await channels.checkWebAccess(visibleWebCheckChannelIds.value)
  } finally {
    batchCheckingWebAccess.value = false
  }
}

function isListeningEnabled(channel: TelegramChannel) {
  return channel.listen_enabled || channel.listen_state === 'enabled'
}

async function toggleListening(channel: TelegramChannel) {
  setLoadingChannel(listeningChannelIds, channel.id, true)
  try {
    await channels.updateControl(channel.id, {
      history_sync_enabled: channel.history_sync_enabled,
      sync_profile: channel.sync_profile,
      listen_enabled: !isListeningEnabled(channel),
      remote_search_allowed: channel.remote_search_allowed
    })
  } finally {
    setLoadingChannel(listeningChannelIds, channel.id, false)
  }
}

async function clearChannel(channel: TelegramChannel) {
  setLoadingChannel(clearingChannelIds, channel.id, true)
  try {
    await channels.clearChannel(channel.id)
  } finally {
    setLoadingChannel(clearingChannelIds, channel.id, false)
  }
}

function confirmClearChannel(channel: TelegramChannel) {
  dialog.warning({
    title: '清空频道',
    content: `清空「${channel.title}」？这会取消监听，并删除这个频道的所有消息和资源。`,
    positiveText: '清空',
    positiveButtonProps: { type: 'error' },
    negativeText: '取消',
    onPositiveClick: () => clearChannel(channel)
  })
}

function terms(value: string) {
  return value
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function joinTerms(value: string[] | undefined) {
  return (value ?? []).join(', ')
}

function applyRuleToForm(rule?: Partial<ListenRulesPayload & Pick<WatchRule, 'enabled'>>) {
  ruleForm.value = {
    enabled: rule?.enabled ?? true,
    includes: joinTerms(rule?.includes),
    excludes: joinTerms(rule?.excludes),
    message_types: rule?.message_types?.length ? [...rule.message_types] : [...defaultMessageTypes],
    link_types: rule?.link_types?.length ? [...rule.link_types] : [...defaultLinkTypes],
    ignored_link_patterns:
      rule?.ignored_link_patterns !== undefined
        ? joinTerms(rule.ignored_link_patterns)
        : joinTerms(defaultIgnoredLinkPatterns)
  }
}

function rulePayload(): ListenRulesPayload {
  const payload: ListenRulesPayload = {
    includes: terms(ruleForm.value.includes),
    excludes: terms(ruleForm.value.excludes),
    message_types: [...ruleForm.value.message_types],
    link_types: [...ruleForm.value.link_types]
  }
  if (ruleScope.value === 'global') {
    payload.ignored_link_patterns = terms(ruleForm.value.ignored_link_patterns)
  }
  return payload
}

async function openGlobalRules() {
  ruleScope.value = 'global'
  ruleTarget.value = null
  channelRuleId.value = null
  applyRuleToForm()
  showRuleModal.value = true
  ruleLoading.value = true
  try {
    const rules = await channels.loadGlobalListenRules()
    applyRuleToForm(rules)
  } finally {
    ruleLoading.value = false
  }
}

async function openChannelRules(channel: TelegramChannel) {
  ruleScope.value = 'channel'
  ruleTarget.value = channel
  channelRuleId.value = null
  applyRuleToForm()
  showRuleModal.value = true
  ruleLoading.value = true
  try {
    const analysis = await channels.analyzeChannel(channel.id)
    if (analysis.watch_rule) {
      channelRuleId.value = analysis.watch_rule.id
      applyRuleToForm(analysis.watch_rule)
    }
  } finally {
    ruleLoading.value = false
  }
}

async function saveRule() {
  ruleSaving.value = true
  try {
    const payload = rulePayload()
    if (ruleScope.value === 'global') {
      await channels.updateGlobalListenRules(payload)
    } else if (ruleTarget.value) {
      const channelPayload = {
        channel_id: ruleTarget.value.id,
        enabled: ruleForm.value.enabled,
        ...payload
      }
      if (channelRuleId.value === null) {
        await channels.createWatchRule(channelPayload)
      } else {
        await channels.updateWatchRule(channelRuleId.value, channelPayload)
      }
      await channels.loadChannels()
    }
    closeRuleModal()
  } finally {
    ruleSaving.value = false
  }
}

async function useGlobalRule() {
  if (channelRuleId.value === null) return
  ruleSaving.value = true
  try {
    await channels.deleteWatchRule(channelRuleId.value)
    await channels.loadChannels()
    closeRuleModal()
  } finally {
    ruleSaving.value = false
  }
}
</script>

<template>
  <section class="page-section">
    <div class="page-header">
      <div>
        <p class="page-kicker">Telegram</p>
        <h1 class="page-title">频道</h1>
        <p class="page-subtitle">筛选频道、检查网页访问、启动历史同步并管理监听规则。</p>
      </div>
      <n-button :loading="channels.loading" @click="refreshChannels">刷新</n-button>
    </div>

    <div class="channel-toolbar">
      <label class="filter-label" for="channel-search">搜索</label>
      <n-input id="channel-search" v-model:value="searchQuery" class="channel-search" clearable placeholder="搜索标题或用户名" />
      <label class="filter-label" for="channel-type">类型</label>
      <n-select id="channel-type" v-model:value="typeFilter" class="type-filter" :options="typeOptions" />
      <label class="filter-label" for="channel-sync">同步</label>
      <n-select id="channel-sync" v-model:value="syncStateFilter" class="sync-state-filter" :options="syncStateOptions" />
      <label class="filter-label" for="channel-listen">监听</label>
      <n-select id="channel-listen" v-model:value="listenStateFilter" class="listen-state-filter" :options="listenStateOptions" />
      <label class="filter-label" for="channel-web">网页访问</label>
      <n-select id="channel-web" v-model:value="webAccessFilter" class="web-access-filter" :options="webAccessOptions" />
      <n-button
        class="batch-web-access-check"
        :disabled="visibleWebCheckChannelIds.length === 0"
        :loading="batchCheckingWebAccess"
        @click="batchCheckWebAccess"
      >
        批量检测
      </n-button>
      <n-button class="global-rule-button" :loading="ruleLoading && ruleScope === 'global'" @click="openGlobalRules">
        全局规则
      </n-button>
    </div>

    <div class="table-panel">
      <table class="data-table">
        <thead>
          <tr>
            <th style="width: 60px">头像</th>
            <th>
              <button class="sort-header" type="button" data-sort-key="title" @click="sortBy('title')">
                标题{{ sortIndicator('title') }}
              </button>
            </th>
            <th>
              <button class="sort-header" type="button" data-sort-key="username" @click="sortBy('username')">
                用户名{{ sortIndicator('username') }}
              </button>
            </th>
            <th>类型</th>
            <th>成员数</th>
            <th>同步状态</th>
            <th>监听状态</th>
            <th>
              <button class="sort-header" type="button" data-sort-key="indexed" @click="sortBy('indexed')">
                已索引消息{{ sortIndicator('indexed') }}
              </button>
            </th>
            <th>网页访问</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="channels.loading && channels.items.length === 0">
            <td colspan="10">
              <div class="loading-stack" aria-label="正在加载频道">
                <span class="skeleton-line" />
                <span class="skeleton-line" />
                <span class="skeleton-line short" />
              </div>
            </td>
          </tr>
          <tr v-for="channel in displayChannels" :key="channel.id">
            <td>
              <Avatar
                :id="channel.id"
                :photo-id="channel.photo_id"
                type="channel"
                :name="channel.title"
                :size="40"
              />
            </td>
            <td class="title-cell">
              <div class="channel-title-row">
                <div class="channel-title-text">
                  <n-tooltip v-if="channel.description" trigger="hover" :content-style="descriptionTooltipStyle">
                    <template #trigger>
                      <a v-if="channelDeepLink(channel)" class="channel-title-link" :href="channelDeepLink(channel)">{{ channel.title }}</a>
                      <span v-else class="title-with-description">{{ channel.title }}</span>
                    </template>
                    {{ channel.description }}
                  </n-tooltip>
                  <template v-else>
                    <a v-if="channelDeepLink(channel)" class="channel-title-link" :href="channelDeepLink(channel)">{{ channel.title }}</a>
                    <span v-else>{{ channel.title }}</span>
                  </template>
                </div>
              </div>
            </td>
            <td>
              <a
                v-if="webAccessUrl(channel)"
                class="channel-username-link"
                :href="webAccessUrl(channel)"
                target="_blank"
                rel="noreferrer"
              >
                {{ username(channel) }}
              </a>
              <span v-else>{{ username(channel) }}</span>
            </td>
            <td>{{ channelTypeLabel(channel.type) }}</td>
            <td>{{ channel.member_count }}</td>
            <td>
              <span class="status-pill" :class="syncStateClass(channel.sync_state)">
                {{ syncStateLabel(channel.sync_state) }}
              </span>
            </td>
            <td>
              <span class="status-pill" :class="listenStateClass(channel.listen_state)">
                {{ listenStateLabel(channel.listen_state) }}
              </span>
            </td>
            <td>
              <RouterLink
                class="indexed-resource-link"
                :to="{ name: 'resources', query: { channel_id: String(channel.id) } }"
              >
                {{ channel.indexed_message_count }}
              </RouterLink>
            </td>
            <td :title="channel.web_access_error || undefined">
              <a
                v-if="webAccessUrl(channel)"
                class="web-access-link"
                :href="webAccessUrl(channel)"
                target="_blank"
                rel="noreferrer"
              >
                <WebAccessBadge :value="channel.web_access" :error="channel.web_access_error" />
              </a>
              <WebAccessBadge v-else :value="channel.web_access" :error="channel.web_access_error" />
            </td>
            <td class="actions">
              <n-button size="small" :loading="syncingChannelIds.has(channel.id)" @click="syncHistory(channel)">同步</n-button>
              <n-button
                size="small"
                :disabled="!canCheckWebAccess(channel)"
                :loading="checkingWebAccessChannelIds.has(channel.id)"
                @click="checkWebAccess(channel)"
              >
                检测
              </n-button>
              <n-button size="small" :type="isListeningEnabled(channel) ? '' : 'primary'" :loading="listeningChannelIds.has(channel.id)" @click="toggleListening(channel)">
                {{ isListeningEnabled(channel) ? '取消监听' : '开启监听' }}
              </n-button>
              <n-button size="small" :loading="ruleLoading && ruleTarget?.id === channel.id" @click="openChannelRules(channel)">
                规则
              </n-button>
              <n-button size="small" type="error" :loading="clearingChannelIds.has(channel.id)" @click="confirmClearChannel(channel)">
                清空
              </n-button>
            </td>
          </tr>
          <tr v-if="!channels.loading && displayChannels.length === 0">
            <td colspan="10">
              <div class="empty-state">
                <strong>暂无频道</strong>
                <span>调整筛选条件，或刷新 Telegram 元数据。</span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <div class="mobile-cards">
        <div v-if="channels.loading && channels.items.length === 0" class="mobile-loading">
          <div class="loading-stack" aria-label="正在加载频道">
            <span class="skeleton-line" />
            <span class="skeleton-line" />
            <span class="skeleton-line short" />
          </div>
        </div>
        <div v-for="channel in displayChannels" :key="channel.id" class="mobile-card">
          <div class="mobile-card-header">
            <Avatar
              :id="channel.id"
              :photo-id="channel.photo_id"
              type="channel"
              :name="channel.title"
              :size="48"
              style="margin-right: 12px"
            />
            <div class="mobile-card-title">
              <a v-if="channelDeepLink(channel)" class="channel-title-link" :href="channelDeepLink(channel)">{{ channel.title }}</a>
              <span v-else>{{ channel.title }}</span>
              <span class="mobile-card-sub">{{ username(channel) }} · {{ channelTypeLabel(channel.type) }} · {{ channel.member_count }} 成员</span>
            </div>
          </div>
          <div class="mobile-card-badges">
            <span class="status-pill" :class="syncStateClass(channel.sync_state)">
              {{ syncStateLabel(channel.sync_state) }}
            </span>
            <span class="status-pill" :class="listenStateClass(channel.listen_state)">
              {{ listenStateLabel(channel.listen_state) }}
            </span>
            <WebAccessBadge :value="channel.web_access" :error="channel.web_access_error" />
          </div>
          <div class="mobile-card-meta">
            <span>{{ channel.indexed_message_count }} 已索引</span>
          </div>
          <div class="mobile-card-actions">
            <n-button size="small" :loading="syncingChannelIds.has(channel.id)" @click="syncHistory(channel)">同步</n-button>
            <n-button size="small" :disabled="!canCheckWebAccess(channel)" :loading="checkingWebAccessChannelIds.has(channel.id)" @click="checkWebAccess(channel)">检测</n-button>
            <n-button size="small" :type="isListeningEnabled(channel) ? '' : 'primary'" :loading="listeningChannelIds.has(channel.id)" @click="toggleListening(channel)">
              {{ isListeningEnabled(channel) ? '取消监听' : '开启监听' }}
            </n-button>
            <n-button size="small" :loading="ruleLoading && ruleTarget?.id === channel.id" @click="openChannelRules(channel)">规则</n-button>
            <n-button size="small" type="error" :loading="clearingChannelIds.has(channel.id)" @click="confirmClearChannel(channel)">清空</n-button>
          </div>
        </div>
        <div v-if="!channels.loading && displayChannels.length === 0" class="empty-state">
          <strong>暂无频道</strong>
          <span>调整筛选条件，或刷新 Telegram 元数据。</span>
        </div>
      </div>
    </div>

    <n-modal v-model:show="showSyncModal">
      <n-card class="sync-modal" :bordered="false">
        <h2 class="sync-modal-title">同步记录最大条数</h2>
        <n-input-number v-model:value="syncMaxMessages" :min="1" :precision="0" />
        <div class="sync-modal-actions">
          <n-button @click="closeSyncModal">取消</n-button>
          <n-button
            type="primary"
            :disabled="!canConfirmSync"
            :loading="syncTarget ? syncingChannelIds.has(syncTarget.id) : false"
            @click="confirmSyncHistory"
          >
            开始同步
          </n-button>
        </div>
      </n-card>
    </n-modal>

    <n-modal v-model:show="showRuleModal">
      <n-card class="listen-rule-modal" :bordered="false">
        <h2 class="rule-modal-title">{{ ruleTitle }}</h2>
        <n-form class="rule-form">
          <n-form-item v-if="ruleScope === 'channel'" label="规则状态">
            <n-checkbox v-model:checked="ruleForm.enabled">启用专属规则</n-checkbox>
          </n-form-item>
          <n-form-item label="包含关键词">
            <n-input v-model:value="ruleForm.includes" class="rule-includes" placeholder="多个关键词用英文逗号分隔" />
          </n-form-item>
          <n-form-item label="排除关键词">
            <n-input v-model:value="ruleForm.excludes" class="rule-excludes" placeholder="多个关键词用英文逗号分隔" />
          </n-form-item>
          <n-form-item v-if="ruleScope === 'global'" label="忽略链接">
            <n-input
              v-model:value="ruleForm.ignored_link_patterns"
              class="rule-ignored-links"
              placeholder="t.me, *.t.me, example.com"
            />
          </n-form-item>
          <n-form-item label="消息类型">
            <n-checkbox-group v-model:value="ruleForm.message_types" class="rule-message-types">
              <n-checkbox value="link">链接</n-checkbox>
              <n-checkbox value="image">图片</n-checkbox>
              <n-checkbox value="video">视频</n-checkbox>
              <n-checkbox value="audio">音频</n-checkbox>
              <n-checkbox value="file">文件</n-checkbox>
              <n-checkbox value="text">文本</n-checkbox>
            </n-checkbox-group>
          </n-form-item>
          <n-form-item label="链接类型">
            <n-checkbox-group v-model:value="ruleForm.link_types" class="rule-link-types">
              <n-checkbox value="cloud_drive">网盘</n-checkbox>
              <n-checkbox value="magnet">磁力</n-checkbox>
              <n-checkbox value="ed2k">ED2K</n-checkbox>
              <n-checkbox value="other">其他</n-checkbox>
            </n-checkbox-group>
          </n-form-item>
        </n-form>
        <div class="rule-modal-actions">
          <n-button v-if="canUseGlobalRule" :loading="ruleSaving" @click="useGlobalRule">使用全局规则</n-button>
          <n-button @click="closeRuleModal">取消</n-button>
          <n-button type="primary" :loading="ruleSaving || ruleLoading" @click="saveRule">保存规则</n-button>
        </div>
      </n-card>
    </n-modal>
  </section>
</template>

<style scoped>
.channel-toolbar {
  grid-template-columns:
    auto minmax(220px, 1.3fr)
    auto minmax(130px, 0.8fr)
    auto minmax(130px, 0.8fr)
    auto minmax(130px, 0.8fr)
    auto minmax(130px, 0.8fr)
    auto auto;
}

table {
  min-width: 980px;
}

.title-cell {
  max-width: 260px;
}

.channel-title-row {
  align-items: center;
  display: flex;
  gap: 10px;
}

.channel-title-text {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.title-with-description {
  cursor: help;
  text-decoration: underline dotted var(--app-border-strong);
  text-underline-offset: 3px;
}

.actions {
  align-items: center;
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 120px;
}

.channel-title-link {
  color: inherit;
  text-decoration: none;
}

.channel-title-link:hover {
  color: var(--app-accent);
  text-decoration: underline;
}

.channel-username-link {
  color: var(--app-accent);
  text-decoration: none;
}

.channel-username-link:hover {
  text-decoration: underline;
}

.indexed-resource-link {
  color: var(--app-accent);
  font-weight: 600;
  text-decoration: none;
}

.indexed-resource-link:hover {
  text-decoration: underline;
}

.web-access-link {
  color: inherit;
  display: inline-flex;
  text-decoration: none;
}

.sync-modal {
  max-width: 420px;
  width: calc(100vw - 32px);
}

.sync-modal-title,
.rule-modal-title {
  color: var(--app-heading);
  font-size: 18px;
  font-weight: 600;
  margin: 0 0 14px;
}

.listen-rule-modal {
  max-width: 560px;
  width: calc(100vw - 32px);
}

.rule-form {
  display: grid;
  gap: 4px;
}

.sync-modal-actions,
.rule-modal-actions {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
  margin-top: 16px;
}

.loading-stack {
  display: grid;
  gap: 8px;
  padding: 8px 0;
}

.loading-stack .short {
  width: 58%;
}

@media (max-width: 1120px) {
  .channel-toolbar {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .channel-toolbar {
    grid-template-columns: 1fr;
  }
}

.mobile-cards {
  display: none;
}

.mobile-card {
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  display: none;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
}

.mobile-card-header {
  align-items: center;
  display: flex;
  gap: 10px;
}

.mobile-card-title {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.mobile-card-sub {
  color: var(--app-text-muted);
  font-size: 12px;
}

.mobile-card-badges {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.mobile-card-meta {
  color: var(--app-text-muted);
  font-size: 12px;
}

.mobile-card-actions {
  border-top: 1px solid var(--app-border);
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-top: 8px;
}

@media (max-width: 760px) {
  .table-panel table {
    display: none;
  }

  .mobile-cards {
    display: flex;
    flex-direction: column;
    gap: 8px;
  }

  .mobile-card {
    display: flex;
  }
}
</style>
