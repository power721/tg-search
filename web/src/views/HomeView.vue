<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useResourcesStore } from '@/stores/resources'
import { useStatusStore } from '@/stores/status'
import { useTasksStore } from '@/stores/tasks'

const router = useRouter()
const auth = useAuthStore()
const status = useStatusStore()
const resources = useResourcesStore()
const tasks = useTasksStore()
const searchQuery = ref('')

onMounted(() => {
  if (!auth.loaded) {
    void auth.loadMe()
  }
  void status.load()
  void resources.loadDashboardGrouped()
  void resources.loadLinkTypesGrouped()
  void tasks.loadTasks()
})

const adminUsername = computed(() => auth.user?.username ?? '-')

const cards = computed(() => [
  { label: '账号', value: status.service?.accounts ?? 0 },
  { label: '频道', value: status.service?.channels ?? 0 },
  { label: '消息', value: status.service?.messages ?? 0 },
  { label: '链接', value: status.service?.links ?? 0 },
  { label: '任务', value: tasks.total }
])

const resourceTypes = computed(() => [
  { label: '网盘', value: resources.dashboardGrouped.cloud_drive ?? 0 },
  { label: '磁力', value: resources.dashboardGrouped.magnet ?? 0 },
  { label: 'ED2K', value: resources.dashboardGrouped.ed2k ?? 0 },
  { label: 'HTTP', value: resources.dashboardGrouped.http ?? 0 },
  { label: '文件', value: resources.dashboardGrouped.files ?? 0 }
])

const linkTypes = computed(() =>
  Object.entries(resources.linkTypesGrouped)
    .map(([type, value]) => ({ label: linkTypeLabel(type), type, value }))
    .sort((a, b) => b.value - a.value || a.label.localeCompare(b.label))
)

const failedTasks = computed(() =>
  tasks.items.filter((task) => task.status === 'failed').slice(0, 4)
)

function formatBytes(value = 0) {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)} GB`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)} MB`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)} KB`
  return `${value} B`
}

function taskTypeLabel(type: string) {
  const labels: Record<string, string> = {
    history_sync: '历史同步',
    web_access_detection: '网页访问检测',
    metadata_sync: '元数据同步',
    cleanup: '清理'
  }
  return labels[type] ?? type
}

function linkTypeLabel(type: string) {
  const labels: Record<string, string> = {
    '115': '115云盘',
    '123': '123网盘',
    aliyun: '阿里云盘',
    baidu: '百度网盘',
    ed2k: 'ED2K',
    guangya: '光鸭云盘',
    magnet: '磁力链接',
    mobile: '移动云盘',
    pikpak: 'PikPak',
    quark: '夸克网盘',
    tianyi: '天翼云盘',
    uc: 'UC网盘',
    url: '普通链接',
    xunlei: '迅雷云盘'
  }
  return labels[type] ?? type
}

async function submitGlobalSearch() {
  const query = searchQuery.value.trim()
  if (!query) return
  await router.push({ path: '/search', query: { q: query } })
}
</script>

<template>
  <section class="page-section">
    <div class="page-header">
      <div>
        <p class="page-kicker">概览</p>
        <h1 class="page-title">本地 Telegram 索引</h1>
        <p class="page-subtitle">查看索引健康、资源增长、任务错误和存储使用情况。</p>
      </div>
      <div class="admin-account" aria-label="当前管理员账号">
        <span>管理员账号</span>
        <strong>{{ adminUsername }}</strong>
      </div>
    </div>

    <form class="home-search filter-bar" @submit.prevent="submitGlobalSearch">
      <label class="filter-label" for="home-search-input">全局搜索</label>
      <input
        id="home-search-input"
        v-model="searchQuery"
        name="q"
        type="search"
        placeholder="搜索消息、链接、文件、频道"
      />
      <button type="submit">搜索</button>
    </form>

    <div class="metric-grid">
      <div v-for="card in cards" :key="card.label" class="metric-card">
        <span>{{ card.label }}</span>
        <strong>{{ card.value }}</strong>
      </div>
    </div>

    <div class="dashboard-grid">
      <section class="panel">
        <h2>存储使用</h2>
        <dl>
          <div>
            <dt>数据库</dt>
            <dd>{{ formatBytes(status.storage?.db_bytes) }}</dd>
          </div>
          <div>
            <dt>索引</dt>
            <dd>{{ formatBytes(status.storage?.index_bytes) }}</dd>
          </div>
          <div>
            <dt>媒体缓存</dt>
            <dd>{{ formatBytes(status.storage?.media_cache_bytes) }}</dd>
          </div>
          <div>
            <dt>总计</dt>
            <dd>{{ formatBytes(status.storage?.total_bytes) }}</dd>
          </div>
        </dl>
      </section>

      <section class="panel">
        <h2>资源类型统计</h2>
        <div class="resource-types">
          <span v-for="item in resourceTypes" :key="item.label">
            {{ item.label }}
            <strong>{{ item.value }}</strong>
          </span>
        </div>
      </section>

      <section class="panel">
        <h2>链接类型统计</h2>
        <div v-if="linkTypes.length === 0" class="muted">暂无链接类型统计</div>
        <div v-else class="resource-types">
          <span v-for="item in linkTypes" :key="item.type">
            {{ item.label }}
            <strong>{{ item.value }}</strong>
          </span>
        </div>
      </section>

      <section class="panel">
        <h2>最近任务错误</h2>
        <div v-if="failedTasks.length === 0" class="empty-state">
          <strong>暂无任务错误</strong>
          <span>失败、限流和重连问题会显示在这里。</span>
        </div>
        <ul v-else class="task-errors">
          <li v-for="task in failedTasks" :key="task.id">
            <span>{{ taskTypeLabel(task.type) }}</span>
            <strong>{{ task.error_message || task.message || task.status }}</strong>
          </li>
        </ul>
      </section>
    </div>
  </section>
</template>

<style scoped>
.home-search {
  grid-template-columns: auto minmax(0, 1fr) auto;
}

.home-search input {
  background: var(--app-surface);
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  color: var(--app-text);
  min-height: 34px;
  min-width: 0;
  padding: 6px 10px;
}

.home-search button {
  background: var(--app-accent);
  border: 1px solid var(--app-accent);
  border-radius: var(--app-radius);
  color: #ffffff;
  min-height: 34px;
  padding: 6px 12px;
}

.admin-account {
  align-items: flex-end;
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  display: grid;
  gap: 3px;
  justify-items: end;
  padding: 8px 10px;
}

.admin-account span {
  color: var(--app-text-muted);
  font-size: 13px;
  font-weight: 600;
  line-height: 1.2;
}

.admin-account strong {
  color: var(--app-heading);
  font-size: 16px;
  font-weight: 650;
  line-height: 1.25;
  max-width: 260px;
  overflow-wrap: anywhere;
}

.dashboard-grid {
  display: grid;
  gap: 16px;
  grid-template-columns: 1fr 1fr;
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

.resource-types {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.resource-types span {
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  display: inline-flex;
  gap: 8px;
  padding: 6px 8px;
}

.task-errors {
  display: grid;
  gap: 8px;
  list-style: none;
  margin: 0;
  padding: 0;
}

.task-errors li {
  border: 1px solid var(--app-border-subtle);
  border-radius: var(--app-radius);
  padding: 8px;
}

.task-errors span {
  color: var(--app-text-muted);
  display: block;
  font-size: 14px;
}

.task-errors strong {
  display: block;
  margin-top: 3px;
  overflow-wrap: anywhere;
}

@media (max-width: 840px) {
  .home-search,
  .dashboard-grid {
    grid-template-columns: 1fr;
  }

  .admin-account {
    align-items: flex-start;
    justify-items: start;
    width: 100%;
  }
}
</style>
