<script setup lang="ts">
import { computed } from 'vue'
import type { Task } from '@/api/types'

defineProps<{
  show: boolean
  task: Task | null
}>()

const emit = defineEmits<{
  'update:show': [value: boolean]
}>()

const drawerWidth = computed(() => Math.min(520, window.innerWidth * 0.9))

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

function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'medium'
  }).format(date)
}
</script>

<template>
  <n-drawer :show="show" :width="drawerWidth" @update:show="emit('update:show', $event)">
    <n-drawer-content title="任务详情">
      <n-descriptions v-if="task" :column="1" bordered>
        <n-descriptions-item label="ID">{{ task.id }}</n-descriptions-item>
        <n-descriptions-item label="类型">{{ taskTypeLabel(task.type) }}</n-descriptions-item>
        <n-descriptions-item label="状态">{{ statusLabel(task.status) }}</n-descriptions-item>
        <n-descriptions-item label="进度">{{ task.progress }} / {{ task.total }}</n-descriptions-item>
        <n-descriptions-item label="消息">{{ task.message || '-' }}</n-descriptions-item>
        <n-descriptions-item label="错误">{{ task.error_message || '-' }}</n-descriptions-item>
        <n-descriptions-item label="重试次数">{{ task.retry_count }}</n-descriptions-item>
        <n-descriptions-item label="创建时间">{{ formatDate(task.created_at) }}</n-descriptions-item>
        <n-descriptions-item label="开始时间">{{ formatDate(task.started_at) }}</n-descriptions-item>
        <n-descriptions-item label="结束时间">{{ formatDate(task.finished_at) }}</n-descriptions-item>
        <n-descriptions-item label="下次运行">{{ formatDate(task.next_run_at) }}</n-descriptions-item>
        <n-descriptions-item label="任务载荷">
          <pre>{{ task.payload_json || '{}' }}</pre>
        </n-descriptions-item>
      </n-descriptions>
    </n-drawer-content>
  </n-drawer>
</template>

<style scoped>
pre {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>
