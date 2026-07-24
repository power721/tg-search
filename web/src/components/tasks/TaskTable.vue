<script setup lang="ts">
import type { Task } from '@/api/types'

type TaskSortKey = 'id' | 'type' | 'status' | 'progress' | 'retry_count' | 'created_at' | 'next_run_at' | 'message'
type SortDirection = 'asc' | 'desc'

defineProps<{
  tasks: Task[]
  loading?: boolean
  selectedIds?: number[]
  sortKey?: TaskSortKey | null
  sortDirection?: SortDirection
}>()

const emit = defineEmits<{
  select: [task: Task]
  sort: [key: TaskSortKey]
  toggleSelect: [task: Task, selected: boolean]
  toggleSelectAll: [selected: boolean]
  retry: [task: Task]
  cancel: [task: Task]
  pause: [task: Task]
  resume: [task: Task]
  delete: [task: Task]
}>()

function progressLabel(task: Task) {
  if (task.total > 0) return `${task.progress} / ${task.total}`
  return `${task.progress}`
}

function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'medium'
  }).format(date)
}

function taskTypeLabel(type: string) {
  const labels: Record<string, string> = {
    backup: '备份',
    channel_analysis: '频道分析',
    gap_recovery: '消息同步',
    history_sync: '历史同步',
    listener_recovery: '监听恢复',
    metadata_sync: '元数据同步',
    remote_search: '远程搜索',
    repair_media_title: '媒体标题修复',
    web_access_detection: '网页访问检测'
  }
  return labels[type] ?? type
}

function statusLabel(status: string) {
  const labels: Record<string, string> = {
    queued: '排队中',
    running: '运行中',
    canceling: '取消中',
    canceled: '已取消',
    paused: '已暂停',
    failed: '失败',
    succeeded: '成功',
    flood_wait: '等待限流解除',
    reconnecting: '重连中'
  }
  return labels[status] ?? status
}

function statusClass(status: string) {
  if (status === 'succeeded') return 'status-success'
  if (['running', 'reconnecting'].includes(status)) return 'status-info'
  if (['queued', 'paused', 'flood_wait', 'canceling'].includes(status)) return 'status-warning'
  if (['failed', 'canceled'].includes(status)) return 'status-danger'
  return 'status-muted'
}

function canRetry(task: Task) {
  return ['failed', 'flood_wait', 'reconnecting'].includes(task.status)
}

function canCancel(task: Task) {
  return ['running', 'paused', 'flood_wait', 'reconnecting'].includes(task.status)
}

function canPause(task: Task) {
  return task.status === 'running'
}

function canResume(task: Task) {
  return task.status === 'paused'
}

function canDelete(task: Task) {
  return !['running', 'canceling'].includes(task.status)
}

function isSelected(task: Task, selectedIds: number[] = []) {
  return selectedIds.includes(task.id)
}

function allSelected(tasks: Task[], selectedIds: number[] = []) {
  return tasks.length > 0 && tasks.every((task) => selectedIds.includes(task.id))
}

function sortIndicator(activeKey: TaskSortKey, sortKey?: TaskSortKey | null, sortDirection?: SortDirection) {
  if (sortKey !== activeKey) return ''
  return sortDirection === 'asc' ? ' ↑' : ' ↓'
}

function sortAria(activeKey: TaskSortKey, sortKey?: TaskSortKey | null, sortDirection?: SortDirection) {
  if (sortKey !== activeKey) return 'none'
  return sortDirection === 'asc' ? 'ascending' : 'descending'
}
</script>

<template>
  <div class="table-panel">
    <table class="data-table">
      <thead>
        <tr>
          <th>
            <input
              aria-label="选择当前页任务"
              :checked="allSelected(tasks, selectedIds)"
              type="checkbox"
              @change="emit('toggleSelectAll', ($event.target as HTMLInputElement).checked)"
            />
          </th>
          <th :aria-sort="sortAria('id', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="id" @click="emit('sort', 'id')">
              ID{{ sortIndicator('id', sortKey, sortDirection) }}
            </button>
          </th>
          <th :aria-sort="sortAria('type', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="type" @click="emit('sort', 'type')">
              类型{{ sortIndicator('type', sortKey, sortDirection) }}
            </button>
          </th>
          <th :aria-sort="sortAria('status', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="status" @click="emit('sort', 'status')">
              状态{{ sortIndicator('status', sortKey, sortDirection) }}
            </button>
          </th>
          <th :aria-sort="sortAria('progress', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="progress" @click="emit('sort', 'progress')">
              进度{{ sortIndicator('progress', sortKey, sortDirection) }}
            </button>
          </th>
          <th :aria-sort="sortAria('retry_count', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="retry_count" @click="emit('sort', 'retry_count')">
              重试次数{{ sortIndicator('retry_count', sortKey, sortDirection) }}
            </button>
          </th>
          <th :aria-sort="sortAria('created_at', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="created_at" @click="emit('sort', 'created_at')">
              创建时间{{ sortIndicator('created_at', sortKey, sortDirection) }}
            </button>
          </th>
          <th :aria-sort="sortAria('next_run_at', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="next_run_at" @click="emit('sort', 'next_run_at')">
              下次运行{{ sortIndicator('next_run_at', sortKey, sortDirection) }}
            </button>
          </th>
          <th :aria-sort="sortAria('message', sortKey, sortDirection)">
            <button class="sort-header" type="button" data-sort-key="message" @click="emit('sort', 'message')">
              消息{{ sortIndicator('message', sortKey, sortDirection) }}
            </button>
          </th>
          <th>操作</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="loading">
          <td colspan="10">
            <div class="loading-stack" aria-label="正在加载任务">
              <span class="skeleton-line" />
              <span class="skeleton-line" />
              <span class="skeleton-line short" />
            </div>
          </td>
        </tr>
        <tr v-for="task in tasks" :key="task.id">
          <td>
            <input
              :aria-label="`选择任务 ${task.id}`"
              :checked="isSelected(task, selectedIds)"
              type="checkbox"
              @change="emit('toggleSelect', task, ($event.target as HTMLInputElement).checked)"
            />
          </td>
          <td>{{ task.id }}</td>
          <td>{{ taskTypeLabel(task.type) }}</td>
          <td><span class="status-pill" :class="statusClass(task.status)">{{ statusLabel(task.status) }}</span></td>
          <td>{{ progressLabel(task) }}</td>
          <td>{{ task.retry_count }}</td>
          <td>
            <time v-if="task.created_at" :datetime="task.created_at">{{ formatDate(task.created_at) }}</time>
            <template v-else>-</template>
          </td>
          <td>
            <time v-if="task.next_run_at" :datetime="task.next_run_at">{{ formatDate(task.next_run_at) }}</time>
            <template v-else>-</template>
          </td>
          <td class="message-cell">{{ task.error_message || task.message || '-' }}</td>
          <td class="actions">
            <n-button size="small" @click="emit('select', task)">详情</n-button>
            <n-button v-if="canRetry(task)" size="small" @click="emit('retry', task)">重试</n-button>
            <n-button v-if="canCancel(task)" size="small" @click="emit('cancel', task)">取消</n-button>
            <n-button v-if="canPause(task)" size="small" @click="emit('pause', task)">暂停</n-button>
            <n-button v-if="canResume(task)" size="small" @click="emit('resume', task)">恢复</n-button>
            <n-button v-if="canDelete(task)" size="small" type="error" ghost @click="emit('delete', task)">删除</n-button>
          </td>
        </tr>
        <tr v-if="!loading && tasks.length === 0">
          <td colspan="10">
            <div class="empty-state">
              <strong>暂无任务</strong>
              <span>同步、检测、清理等后台任务会显示在这里。</span>
            </div>
          </td>
        </tr>
      </tbody>
    </table>

    <div class="mobile-cards">
      <div v-if="loading" class="mobile-loading">
        <div class="loading-stack" aria-label="正在加载任务">
          <span class="skeleton-line" />
          <span class="skeleton-line" />
          <span class="skeleton-line short" />
        </div>
      </div>
      <div v-for="task in tasks" :key="task.id" class="mobile-card">
        <div class="mobile-card-header">
          <label class="mobile-card-check">
            <input
              :aria-label="`选择任务 ${task.id}`"
              :checked="isSelected(task, selectedIds)"
              type="checkbox"
              @change="emit('toggleSelect', task, ($event.target as HTMLInputElement).checked)"
            />
          </label>
          <div class="mobile-card-title">
            <span>#{{ task.id }} · {{ taskTypeLabel(task.type) }}</span>
          </div>
          <span class="status-pill" :class="statusClass(task.status)">{{ statusLabel(task.status) }}</span>
        </div>
        <div v-if="task.total > 0" class="mobile-card-progress">
          <div class="progress-bar">
            <div class="progress-fill" :style="{ width: `${task.total > 0 ? (task.progress / task.total * 100) : 0}%` }"></div>
          </div>
          <span class="progress-text">{{ progressLabel(task) }}</span>
        </div>
        <div class="mobile-card-meta">
          <span>创建：{{ formatDate(task.created_at) }}</span>
          <span>重试：{{ task.retry_count }} · 下次运行：{{ formatDate(task.next_run_at) }}</span>
        </div>
        <div v-if="task.error_message || task.message" class="mobile-card-message">
          {{ task.error_message || task.message }}
        </div>
        <div class="mobile-card-actions">
          <n-button size="small" @click="emit('select', task)">详情</n-button>
          <n-button v-if="canRetry(task)" size="small" @click="emit('retry', task)">重试</n-button>
          <n-button v-if="canCancel(task)" size="small" @click="emit('cancel', task)">取消</n-button>
          <n-button v-if="canPause(task)" size="small" @click="emit('pause', task)">暂停</n-button>
          <n-button v-if="canResume(task)" size="small" @click="emit('resume', task)">恢复</n-button>
          <n-button v-if="canDelete(task)" size="small" type="error" ghost @click="emit('delete', task)">删除</n-button>
        </div>
      </div>
      <div v-if="!loading && tasks.length === 0" class="empty-state">
        <strong>暂无任务</strong>
        <span>同步、检测、清理等后台任务会显示在这里。</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.table-panel {
  overflow-x: auto;
}

table {
  min-width: 1120px;
}

.message-cell {
  max-width: 260px;
  overflow-wrap: anywhere;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 260px;
}

.empty-cell {
  text-align: center;
}

.loading-stack {
  display: grid;
  gap: 8px;
  padding: 8px 0;
}

.loading-stack .short {
  width: 58%;
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
  gap: 8px;
}

.mobile-card-check {
  align-items: center;
  display: flex;
}

.mobile-card-title {
  flex: 1;
  font-weight: 600;
  min-width: 0;
}

.mobile-card-progress {
  align-items: center;
  display: flex;
  gap: 8px;
}

.progress-bar {
  background: var(--app-border);
  border-radius: 3px;
  flex: 1;
  height: 6px;
  overflow: hidden;
}

.progress-fill {
  background: var(--app-accent);
  border-radius: 3px;
  height: 100%;
  transition: width 0.2s;
}

.progress-text {
  color: var(--app-text-muted);
  font-size: 12px;
  white-space: nowrap;
}

.mobile-card-meta {
  color: var(--app-text-muted);
  display: flex;
  flex-direction: column;
  font-size: 12px;
  gap: 2px;
}

.mobile-card-message {
  color: var(--app-text-muted);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.mobile-card-actions {
  border-top: 1px solid var(--app-border);
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-top: 8px;
}

@media (max-width: 760px) {
  table {
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
