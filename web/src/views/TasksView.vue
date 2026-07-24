<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useDialog } from 'naive-ui'
import type { Task } from '@/api/types'
import AppPagination from '@/components/common/AppPagination.vue'
import TaskDetailDrawer from '@/components/tasks/TaskDetailDrawer.vue'
import TaskTable from '@/components/tasks/TaskTable.vue'
import { useEventsStore } from '@/stores/events'
import { useTasksStore } from '@/stores/tasks'

type TaskSortKey = 'id' | 'type' | 'status' | 'progress' | 'retry_count' | 'created_at' | 'next_run_at' | 'message'
type SortDirection = 'asc' | 'desc'

const tasks = useTasksStore()
const events = useEventsStore()
const dialog = useDialog()
const detailOpen = ref(false)
const pageSizeOptions = [20, 50, 100]
const pageSize = ref(50)
const offset = ref(0)
const selectedTaskIds = ref<number[]>([])
const searchQuery = ref('')
const statusFilter = ref('')
const typeFilter = ref('')
const sortKey = ref<TaskSortKey | null>(null)
const sortDirection = ref<SortDirection>('desc')

const page = computed(() => Math.floor(offset.value / pageSize.value) + 1)

const statusOptions = [
  { label: '全部状态', value: '' },
  { label: '排队中', value: 'queued' },
  { label: '运行中', value: 'running' },
  { label: '取消中', value: 'canceling' },
  { label: '已取消', value: 'canceled' },
  { label: '已暂停', value: 'paused' },
  { label: '失败', value: 'failed' },
  { label: '成功', value: 'succeeded' },
  { label: '等待限流解除', value: 'flood_wait' },
  { label: '重连中', value: 'reconnecting' }
]

const typeOptions = [
  { label: '全部类型', value: '' },
  { label: '备份', value: 'backup' },
  { label: '频道分析', value: 'channel_analysis' },
  { label: '消息同步', value: 'gap_recovery' },
  { label: '历史同步', value: 'history_sync' },
  { label: '监听恢复', value: 'listener_recovery' },
  { label: '元数据同步', value: 'metadata_sync' },
  { label: '远程搜索', value: 'remote_search' },
  { label: '媒体标题修复', value: 'repair_media_title' },
  { label: '网页访问检测', value: 'web_access_detection' }
]

const activeFilters = computed(() => ({
  status: statusFilter.value,
  type: typeFilter.value,
  q: searchQuery.value.trim(),
  sort: sortKey.value ?? undefined,
  order: sortKey.value ? sortDirection.value : undefined,
  limit: pageSize.value,
  offset: offset.value
}))

function loadPage() {
  selectedTaskIds.value = []
  return tasks.loadTasks(activeFilters.value)
}

onMounted(() => {
  void loadPage()
  events.connect()
})

onUnmounted(() => {
  events.disconnect()
})

function selectTask(task: Task) {
  tasks.selected = task
  detailOpen.value = true
}

async function refreshTasks() {
  await loadPage()
}

async function changePage(pageNumber: number) {
  offset.value = (pageNumber - 1) * pageSize.value
  await loadPage()
}

async function changePageSize(value: number) {
  pageSize.value = value
  offset.value = 0
  await loadPage()
}

async function resetAndLoad() {
  offset.value = 0
  await loadPage()
}

async function resetFilters() {
  searchQuery.value = ''
  statusFilter.value = ''
  typeFilter.value = ''
  sortKey.value = null
  sortDirection.value = 'desc'
  offset.value = 0
  await loadPage()
}

async function sortTasks(key: TaskSortKey) {
  if (sortKey.value === key) {
    sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortKey.value = key
    sortDirection.value = 'asc'
  }
  offset.value = 0
  await loadPage()
}

function toggleTaskSelection(task: Task, selected: boolean) {
  const next = new Set(selectedTaskIds.value)
  if (selected) {
    next.add(task.id)
  } else {
    next.delete(task.id)
  }
  selectedTaskIds.value = [...next]
}

function toggleCurrentPageSelection(selected: boolean) {
  selectedTaskIds.value = selected ? tasks.items.map((task) => task.id) : []
}

function confirmDeleteTask(task: Task) {
  dialog.warning({
    title: '删除任务',
    content: `确定删除任务 ${task.id}？`,
    positiveText: '删除任务',
    positiveButtonProps: { type: 'error' },
    negativeText: '取消',
    onPositiveClick: async () => {
      await tasks.deleteTask(task.id)
      await loadPage()
    }
  })
}

function confirmDeleteSelectedTasks() {
  const ids = [...selectedTaskIds.value]
  if (ids.length === 0) return
  dialog.warning({
    title: '删除任务',
    content: `确定删除选中的 ${ids.length} 个任务？运行中和取消中的任务不会被删除。`,
    positiveText: '删除任务',
    positiveButtonProps: { type: 'error' },
    negativeText: '取消',
    onPositiveClick: async () => {
      await tasks.deleteTasks(ids)
      await loadPage()
    }
  })
}
</script>

<template>
  <section class="page-section">
    <div class="page-header">
      <div>
        <p class="page-kicker">运行状态</p>
        <h1 class="page-title">任务</h1>
        <p class="page-subtitle">查看后台同步、检测、重试和限流恢复任务。</p>
      </div>
      <div class="header-actions">
        <n-button
          :disabled="selectedTaskIds.length === 0 || tasks.loading"
          type="error"
          ghost
          @click="confirmDeleteSelectedTasks"
        >
          删除选中
        </n-button>
        <n-button :loading="tasks.loading" @click="refreshTasks">刷新</n-button>
      </div>
    </div>

    <div v-if="tasks.error" class="error-strip">{{ tasks.error }}</div>

    <form class="task-filters" @submit.prevent="resetAndLoad">
      <label class="filter-label" for="task-search">搜索</label>
      <n-input id="task-search" v-model:value="searchQuery" class="task-search" clearable placeholder="搜索 ID、消息、错误或载荷" />
      <label class="filter-label" for="task-status">状态</label>
      <n-select id="task-status" v-model:value="statusFilter" class="task-status-filter" :options="statusOptions" />
      <label class="filter-label" for="task-type">类型</label>
      <n-select id="task-type" v-model:value="typeFilter" class="task-type-filter" :options="typeOptions" />
      <n-button attr-type="submit" type="primary" :loading="tasks.loading">搜索</n-button>
      <n-button attr-type="button" :disabled="tasks.loading" @click="resetFilters">重置</n-button>
    </form>

    <TaskTable
      :tasks="tasks.items"
      :loading="tasks.loading"
      :selected-ids="selectedTaskIds"
      :sort-key="sortKey"
      :sort-direction="sortDirection"
      @select="selectTask"
      @sort="sortTasks"
      @toggle-select="toggleTaskSelection"
      @toggle-select-all="toggleCurrentPageSelection"
      @retry="tasks.retryTask($event.id)"
      @cancel="tasks.cancelTask($event.id)"
      @pause="tasks.pauseTask($event.id)"
      @resume="tasks.resumeTask($event.id)"
      @delete="confirmDeleteTask"
    />

    <AppPagination
      :loading="tasks.loading"
      :page="page"
      :page-size="pageSize"
      :page-size-options="pageSizeOptions"
      :total="tasks.total"
      @update:page="changePage"
      @update:page-size="changePageSize"
    />

    <TaskDetailDrawer v-model:show="detailOpen" :task="tasks.selected" />
  </section>
</template>
<style scoped>
.header-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.task-filters {
  align-items: center;
  background: var(--app-surface);
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  display: grid;
  gap: 8px;
  grid-template-columns: auto minmax(220px, 1fr) auto 180px auto 220px auto auto;
  padding: 10px;
}

@media (max-width: 860px) {
  .task-filters {
    grid-template-columns: 1fr;
  }
}
</style>
