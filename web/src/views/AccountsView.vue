<script setup lang="ts">
import QRCode from 'qrcode'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import type { TelegramAccount } from '@/api/types'
import AppPagination from '@/components/common/AppPagination.vue'
import Avatar from '@/components/common/Avatar.vue'
import { useTelegramStore } from '@/stores/telegram'

const telegram = useTelegramStore()
const dialog = useDialog()
const message = useMessage()

const loginDialogVisible = ref(false)
const loginPhone = ref('')
const loginCode = ref('')
const loginPassword = ref('')
const loginSessionString = ref('')
const loginCodeSent = ref(false)
const loginMode = ref<'qr' | 'code' | 'session'>('qr')
const qrCanvas = ref<HTMLCanvasElement | null>(null)
const qrLoginID = ref('')
const qrStatus = ref('')
const page = ref(1)
const pageSize = ref(20)
const pageSizeOptions = [10, 20, 50]
const syncingAccountIds = ref(new Set<number>())
const syncingAvatarAccountIds = ref(new Set<number>())
let qrPolling: number | undefined

const totalPages = computed(() => Math.max(1, Math.ceil(telegram.accounts.length / pageSize.value)))
const pagedAccounts = computed(() => {
  const start = (page.value - 1) * pageSize.value
  return telegram.accounts.slice(start, start + pageSize.value)
})

const metadataText = computed(() => {
  const sync = telegram.loginResult?.metadata_sync
  if (!sync) return ''
  if (sync.status === 'succeeded') return `元数据同步成功：${sync.channel_count} 个频道`
  if (sync.status === 'failed') return `元数据同步失败：${sync.error ?? '未知错误'}`
  return `元数据同步状态：${sync.status}`
})

onMounted(() => {
  void telegram.loadAccounts()
})

watch(
  () => telegram.accounts.length,
  () => {
    if (page.value > totalPages.value) {
      page.value = totalPages.value
    }
  }
)

function displayName(firstName: string, lastName: string, username: string) {
  const name = [firstName, lastName].filter(Boolean).join(' ')
  if (name) return name
  return username ? `@${username}` : '-'
}

function formatDate(value?: string) {
  if (!value) return '-'
  return new Intl.DateTimeFormat('zh-CN', {
    dateStyle: 'medium',
    timeStyle: 'short'
  }).format(new Date(value))
}

function statusLabel(status: string) {
  const labels: Record<string, string> = {
    ONLINE: '在线',
    LOGIN_REQUIRED: '需要登录',
    PASSWORD_REQUIRED: '需要密码',
    RECONNECTING: '重连中',
    FLOOD_WAIT: '等待限流解除',
    DISCONNECTED: '已断开',
    OFFLINE: '离线',
    ERROR: '异常'
  }
  return labels[status] ?? status
}

function statusClass(status: string) {
  if (status === 'ONLINE') return 'status-success'
  if (status === 'LOGIN_REQUIRED' || status === 'PASSWORD_REQUIRED' || status === 'FLOOD_WAIT') return 'status-warning'
  if (status === 'RECONNECTING') return 'status-info'
  if (status === 'ERROR') return 'status-danger'
  return 'status-muted'
}

function needsLogin(account: TelegramAccount) {
  return account.status === 'LOGIN_REQUIRED'
}

function openTelegramLogin(account?: TelegramAccount) {
  loginPhone.value = account?.phone ?? ''
  loginCode.value = ''
  loginPassword.value = ''
  loginSessionString.value = ''
  loginCodeSent.value = false
  loginMode.value = account ? 'code' : 'qr'
  qrLoginID.value = ''
  qrStatus.value = ''
  telegram.phone = loginPhone.value
  telegram.passwordRequired = false
  telegram.loginResult = null
  telegram.qrLogin = null
  loginDialogVisible.value = true
}

function closeTelegramLogin() {
  void cancelQRLogin()
  loginDialogVisible.value = false
}

function setLoginMode(mode: 'qr' | 'code' | 'session') {
  loginMode.value = mode
  if (mode === 'code' || mode === 'session') {
    void cancelQRLogin()
  }
}

async function startQRLogin() {
  try {
    stopQRPolling()
    const response = await telegram.startQRLogin()
    qrLoginID.value = response.login_id
    qrStatus.value = response.status
    await renderQRCode(response.qr_url)
    await pollQRLogin()
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法生成二维码')
  }
}

async function renderQRCode(value?: string) {
  if (!value) return
  await nextTick()
  if (qrCanvas.value) {
    await QRCode.toCanvas(qrCanvas.value, value, { width: 220, margin: 1 })
  }
}

async function pollQRLogin() {
  if (!qrLoginID.value) return
  try {
    const response = await telegram.pollQRLogin(qrLoginID.value)
    qrStatus.value = response.status
    if (response.qr_url) {
      await renderQRCode(response.qr_url)
    }
    if (response.account) {
      finishLogin()
      return
    }
    stopQRPolling()
    qrPolling = window.setTimeout(() => {
      void pollQRLogin()
    }, 2000)
  } catch (error) {
    stopQRPolling()
    message.error(error instanceof Error ? error.message : '无法确认扫码状态')
  }
}

async function cancelQRLogin() {
  stopQRPolling()
  if (!qrLoginID.value) return
  try {
    await telegram.cancelQRLogin(qrLoginID.value)
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法取消扫码登录')
  } finally {
    qrLoginID.value = ''
    qrStatus.value = ''
  }
}

function stopQRPolling() {
  if (qrPolling !== undefined) {
    window.clearTimeout(qrPolling)
    qrPolling = undefined
  }
}

async function sendCode() {
  try {
    await telegram.sendCode(loginPhone.value)
    loginCodeSent.value = true
    message.success('验证码已发送')
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法发送验证码')
  }
}

async function signIn() {
  try {
    telegram.phone = loginPhone.value
    const response = await telegram.signIn(loginCode.value)
    if (response.account) {
      finishLogin()
    }
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法登录')
  }
}

async function submitPassword() {
  try {
    telegram.phone = loginPhone.value
    const response = await telegram.submitPassword(loginPassword.value)
    if (response.account) {
      finishLogin()
    }
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法提交密码')
  }
}

async function loginWithSession() {
  try {
    const response = await telegram.loginWithTelethonSession(loginSessionString.value)
    if (response.account) {
      finishLogin()
    }
  } catch (error) {
    message.error(error instanceof Error ? error.message : '无法登录')
  }
}

function finishLogin() {
  stopQRPolling()
  qrLoginID.value = ''
  qrStatus.value = ''
  loginDialogVisible.value = false
  message.success('Telegram 账号已连接')
}

function changePage(pageNumber: number) {
  page.value = Math.min(Math.max(1, pageNumber), totalPages.value)
}

function changePageSize(value: number) {
  pageSize.value = value
  page.value = 1
}

async function logoutAccount(account: TelegramAccount) {
  await telegram.logoutAccount(account.id)
}

function confirmDeleteAccount(account: TelegramAccount) {
  dialog.warning({
    title: '删除账号',
    content: `确定删除 ${account.phone}？这会删除该账号及其索引数据。`,
    positiveText: '删除账号',
    positiveButtonProps: { type: 'error' },
    negativeText: '取消',
    onPositiveClick: () => telegram.deleteAccount(account.id)
  })
}

async function syncAccountChannels(account: TelegramAccount) {
  const next = new Set(syncingAccountIds.value)
  next.add(account.id)
  syncingAccountIds.value = next
  try {
    await telegram.syncAccountChannels(account.id)
    message.success(`${account.phone} 频道同步完成`)
  } catch {
    message.error(`${account.phone} 频道同步失败`)
  } finally {
    const done = new Set(syncingAccountIds.value)
    done.delete(account.id)
    syncingAccountIds.value = done
  }
}

async function syncAccountAvatar(account: TelegramAccount) {
  const next = new Set(syncingAvatarAccountIds.value)
  next.add(account.id)
  syncingAvatarAccountIds.value = next
  try {
    await telegram.syncAccountAvatar(account.id)
    message.success(`${account.phone} 头像同步已提交`)
  } catch {
    message.error(`${account.phone} 头像同步失败`)
  } finally {
    const done = new Set(syncingAvatarAccountIds.value)
    done.delete(account.id)
    syncingAvatarAccountIds.value = done
  }
}

onBeforeUnmount(() => {
  stopQRPolling()
})
</script>

<template>
  <section class="page-section">
    <div class="page-header">
      <div>
        <p class="page-kicker">Telegram</p>
        <h1 class="page-title">账号</h1>
        <p class="page-subtitle">管理 Telegram 会话状态、重新登录和账号删除。</p>
      </div>
      <div class="header-actions">
        <n-button :loading="telegram.loading" @click="telegram.loadAccounts">刷新</n-button>
        <n-button type="primary" @click="openTelegramLogin()">添加账号</n-button>
      </div>
    </div>

    <div class="table-panel">
      <table class="data-table">
        <thead>
          <tr>
            <th style="width: 60px">头像</th>
            <th>手机号</th>
            <th>名称</th>
            <th>状态</th>
            <th>最后在线</th>
            <th>最后错误</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-if="telegram.loading && telegram.accounts.length === 0">
            <td colspan="7">
              <div class="loading-stack" aria-label="正在加载账号">
                <span class="skeleton-line" />
                <span class="skeleton-line short" />
              </div>
            </td>
          </tr>
          <tr v-for="account in pagedAccounts" :key="account.id">
            <td>
              <Avatar
                :id="account.id"
                :photo-id="account.photo_id"
                type="account"
                :name="displayName(account.first_name, account.last_name, account.username)"
                :size="40"
              />
            </td>
            <td>{{ account.phone }}</td>
            <td>{{ displayName(account.first_name, account.last_name, account.username) }}</td>
            <td>
              <span class="status-pill" :class="statusClass(account.status)">{{ statusLabel(account.status) }}</span>
              <p v-if="needsLogin(account)" class="status-help">
                点击登录后发送验证码，并在 Telegram 官方消息中查看验证码。
              </p>
            </td>
            <td>{{ formatDate(account.last_online_at) }}</td>
            <td>{{ account.last_error || '-' }}</td>
            <td>
              <div class="action-buttons">
                <n-button v-if="needsLogin(account)" size="small" type="primary" @click="openTelegramLogin(account)">
                  登录
                </n-button>
                <n-button v-else size="small" :loading="telegram.loading" @click="logoutAccount(account)">
                  登出
                </n-button>
                <n-button
                  v-if="!needsLogin(account)"
                  size="small"
                  :loading="syncingAccountIds.has(account.id)"
                  @click="syncAccountChannels(account)"
                >
                  同步频道
                </n-button>
                <n-button
                  v-if="!needsLogin(account)"
                  size="small"
                  :disabled="account.status !== 'ONLINE'"
                  :loading="syncingAvatarAccountIds.has(account.id)"
                  @click="syncAccountAvatar(account)"
                >
                  同步头像
                </n-button>
                <n-button
                  size="small"
                  type="error"
                  ghost
                  :loading="telegram.loading"
                  @click="confirmDeleteAccount(account)"
                >
                  删除
                </n-button>
              </div>
            </td>
          </tr>
          <tr v-if="!telegram.loading && telegram.accounts.length === 0">
            <td colspan="7">
              <div class="empty-state">
                <strong>暂无账号</strong>
                <span>添加 Telegram 账号后即可同步频道元数据。</span>
              </div>
            </td>
          </tr>
        </tbody>
      </table>

      <div class="mobile-cards">
        <div v-if="telegram.loading && telegram.accounts.length === 0" class="mobile-loading">
          <div class="loading-stack" aria-label="正在加载账号">
            <span class="skeleton-line" />
            <span class="skeleton-line short" />
          </div>
        </div>
        <div v-for="account in pagedAccounts" :key="account.id" class="mobile-card">
          <div class="mobile-card-header">
            <Avatar
              :id="account.id"
              :photo-id="account.photo_id"
              type="account"
              :name="displayName(account.first_name, account.last_name, account.username)"
              :size="48"
              style="margin-right: 12px"
            />
            <div class="mobile-card-title">
              <span class="mobile-card-name">{{ displayName(account.first_name, account.last_name, account.username) }}</span>
              <span class="mobile-card-sub">{{ account.phone }}</span>
            </div>
            <span class="status-pill" :class="statusClass(account.status)">{{ statusLabel(account.status) }}</span>
          </div>
          <div class="mobile-card-meta">
            <span>最后在线：{{ formatDate(account.last_online_at) }}</span>
            <span v-if="account.last_error" class="mobile-card-error">错误：{{ account.last_error }}</span>
          </div>
          <p v-if="needsLogin(account)" class="status-help">
            点击登录后发送验证码，并在 Telegram 官方消息中查看验证码。
          </p>
          <div class="mobile-card-actions">
            <n-button v-if="needsLogin(account)" size="small" type="primary" @click="openTelegramLogin(account)">登录</n-button>
            <n-button v-else size="small" :loading="telegram.loading" @click="logoutAccount(account)">登出</n-button>
            <n-button
              v-if="!needsLogin(account)"
              size="small"
              :loading="syncingAccountIds.has(account.id)"
              @click="syncAccountChannels(account)"
            >同步频道</n-button>
            <n-button
              v-if="!needsLogin(account)"
              size="small"
              :disabled="account.status !== 'ONLINE'"
              :loading="syncingAvatarAccountIds.has(account.id)"
              @click="syncAccountAvatar(account)"
            >同步头像</n-button>
            <n-button size="small" type="error" ghost :loading="telegram.loading" @click="confirmDeleteAccount(account)">删除</n-button>
          </div>
        </div>
        <div v-if="!telegram.loading && telegram.accounts.length === 0" class="empty-state">
          <strong>暂无账号</strong>
          <span>添加 Telegram 账号后即可同步频道元数据。</span>
        </div>
      </div>
    </div>

    <AppPagination
      v-if="telegram.accounts.length > 0"
      :loading="telegram.loading"
      :page="page"
      :page-size="pageSize"
      :page-size-options="pageSizeOptions"
      :total="telegram.accounts.length"
      @update:page="changePage"
      @update:page-size="changePageSize"
    />

    <n-modal v-model:show="loginDialogVisible" :mask-closable="false">
      <n-card
        class="login-dialog"
        :bordered="false"
        closable
        @close="closeTelegramLogin"
      >
        <template #header>
          <div>
            <p class="page-kicker">Telegram</p>
            <h2>Telegram 登录</h2>
          </div>
        </template>
        <n-button-group class="mode-switch">
          <n-button :type="loginMode === 'qr' ? 'primary' : 'default'" @click="setLoginMode('qr')">
            扫码登录
          </n-button>
          <n-button :type="loginMode === 'code' ? 'primary' : 'default'" @click="setLoginMode('code')">
            验证码登录
          </n-button>
          <n-button :type="loginMode === 'session' ? 'primary' : 'default'" @click="setLoginMode('session')">
            Session登录
          </n-button>
        </n-button-group>

        <div v-if="loginMode === 'qr'" class="qr-login">
          <div class="qr-surface">
            <canvas v-show="qrLoginID" ref="qrCanvas" class="qr-canvas" />
            <div v-if="!qrLoginID" class="qr-placeholder">QR</div>
          </div>
          <n-button type="primary" block :loading="telegram.loading" @click="startQRLogin">生成二维码</n-button>
          <n-button v-if="qrLoginID" block @click="cancelQRLogin">取消扫码</n-button>
          <p v-if="qrStatus" class="sync-result">扫码状态：{{ qrStatus }}</p>
        </div>

        <n-form v-else-if="loginMode === 'session'" @submit.prevent>
          <n-form-item label="Telethon Session String">
            <n-input
              v-model:value="loginSessionString"
              type="textarea"
              placeholder="请粘贴 Telethon StringSession"
              :autosize="{ minRows: 3, maxRows: 6 }"
            />
            <template #feedback>从 Telethon 客户端导出的 session.string_session() 字符串</template>
          </n-form-item>
          <n-button type="primary" block :loading="telegram.loading" @click="loginWithSession">
            登录
          </n-button>
        </n-form>

        <n-form v-else @submit.prevent>
          <n-form-item label="手机号">
            <n-input v-model:value="loginPhone" autocomplete="tel" placeholder="+86 13800138000" />
            <template #feedback>请包含国家码，可输入空格、短横线或括号。</template>
          </n-form-item>
          <n-button type="primary" block :loading="telegram.loading" @click="sendCode">发送验证码</n-button>
          <div v-if="loginCodeSent" class="login-code-guidance" role="status">
            <strong>Telegram 已接受验证码请求</strong>
            <span>请先查看已登录 Telegram 客户端里的官方验证码消息。</span>
            <span>不要连续重复发送验证码；如果没有任何已登录客户端，再等待短信或电话验证码。</span>
          </div>

          <div class="form-section">
            <n-form-item label="验证码">
              <n-input
                v-model:value="loginCode"
                inputmode="numeric"
                autocomplete="one-time-code"
                placeholder="请输入验证码"
                :disabled="!loginCodeSent"
              />
            </n-form-item>
            <n-button type="primary" block :disabled="!loginCodeSent" :loading="telegram.loading" @click="signIn">
              登录
            </n-button>
          </div>

          <div v-if="telegram.passwordRequired" class="form-section">
            <n-form-item label="两步验证密码">
              <n-input
                v-model:value="loginPassword"
                type="password"
                autocomplete="current-password"
                placeholder="请输入密码"
              />
            </n-form-item>
            <n-button type="primary" block :loading="telegram.loading" @click="submitPassword">
              提交密码
            </n-button>
          </div>
        </n-form>
        <p v-if="metadataText" class="sync-result">{{ metadataText }}</p>
        <div class="login-dialog-actions">
          <n-button :disabled="telegram.loading" @click="closeTelegramLogin">取消</n-button>
        </div>
      </n-card>
    </n-modal>
  </section>
</template>

<style scoped>
table {
  min-width: 760px;
}

.action-buttons {
  display: flex;
  gap: 8px;
  white-space: nowrap;
}

.status-help {
  color: var(--app-text-muted);
  font-size: 12px;
  line-height: 1.5;
  margin: 6px 0 0;
  max-width: 220px;
}

.login-dialog {
  max-width: 420px;
  width: min(420px, calc(100vw - 32px));
}

.login-dialog h2 {
  font-size: 22px;
  margin: 0;
}

.mode-switch {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  margin-bottom: 18px;
  width: 100%;
}

.qr-login {
  display: grid;
  gap: 14px;
}

.qr-surface {
  align-items: center;
  aspect-ratio: 1;
  background: var(--app-surface-muted);
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  display: flex;
  justify-content: center;
  margin: 0 auto;
  max-width: 260px;
  width: 100%;
}

.qr-canvas {
  height: 220px;
  width: 220px;
}

.qr-placeholder {
  align-items: center;
  color: var(--app-text-subtle);
  display: flex;
  font-size: 22px;
  font-weight: 700;
  height: 220px;
  justify-content: center;
  width: 220px;
}

.login-dialog-actions {
  display: flex;
  justify-content: flex-end;
  margin-top: 16px;
}

.sync-result {
  color: var(--app-text-muted);
  margin: 16px 0 0;
}

.login-code-guidance {
  background: var(--app-surface-muted);
  border: 1px solid var(--app-border);
  border-radius: var(--app-radius);
  color: var(--app-text-muted);
  display: grid;
  gap: 4px;
  line-height: 1.6;
  margin-top: 12px;
  padding: 10px 12px;
}

.login-code-guidance strong {
  color: var(--app-text);
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
  gap: 10px;
}

.mobile-card-title {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.mobile-card-name {
  font-weight: 600;
}

.mobile-card-sub {
  color: var(--app-text-muted);
  font-size: 12px;
}

.mobile-card-meta {
  color: var(--app-text-muted);
  display: flex;
  flex-direction: column;
  font-size: 12px;
  gap: 2px;
}

.mobile-card-error {
  color: var(--app-danger);
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
