import { defineStore } from 'pinia'
import { apiDelete, apiGet, apiPost } from '@/api/client'
import type {
  TelegramAccountsResponse,
  TelegramAccount,
  TelegramAPISettingsResponse,
  TelegramLoginResponse,
  TelegramQRLoginStartResponse,
  TelegramQRLoginStatusResponse
} from '@/api/types'

export const useTelegramStore = defineStore('telegram', {
  state: () => ({
    settings: null as TelegramAPISettingsResponse | null,
    accounts: [] as TelegramAccount[],
    phone: '',
    passwordRequired: false,
    loading: false,
    error: '',
    loginResult: null as TelegramLoginResponse | null,
    qrLogin: null as TelegramQRLoginStartResponse | TelegramQRLoginStatusResponse | null
  }),
  actions: {
    async loadSettings() {
      return this.withLoading(async () => {
        this.settings = await apiGet<TelegramAPISettingsResponse>('/api/settings/telegram-api')
        return this.settings
      })
    },
    async saveTelegramAPI(appID: number, appHash: string) {
      return this.withLoading(async () => {
        this.settings = await apiPost<TelegramAPISettingsResponse>('/api/setup/telegram-api', {
          app_id: appID,
          app_hash: appHash
        })
        return this.settings
      })
    },
    async sendCode(phone: string) {
      return this.withLoading(async () => {
        this.phone = phone
        this.passwordRequired = false
        this.loginResult = await apiPost<TelegramLoginResponse>('/api/telegram/login/send-code', {
          phone
        })
        this.phone = this.loginResult.phone ?? phone
        return this.loginResult
      })
    },
    async signIn(code: string) {
      return this.withLoading(async () => {
        this.loginResult = await apiPost<TelegramLoginResponse>('/api/telegram/login/sign-in', {
          phone: this.phone,
          code
        })
        this.passwordRequired = this.loginResult.password_required === true
        if (this.loginResult.account) {
          await this.loadAccounts()
        }
        return this.loginResult
      })
    },
    async submitPassword(password: string) {
      return this.withLoading(async () => {
        this.loginResult = await apiPost<TelegramLoginResponse>('/api/telegram/login/password', {
          phone: this.phone,
          password
        })
        this.passwordRequired = false
        if (this.loginResult.account) {
          await this.loadAccounts()
        }
        return this.loginResult
      })
    },
    async loginWithTelethonSession(sessionString: string) {
      return this.withLoading(async () => {
        this.passwordRequired = false
        this.loginResult = await apiPost<TelegramLoginResponse>('/api/telegram/login/telethon-session', {
          session_string: sessionString
        })
        if (this.loginResult.account) {
          await this.loadAccounts()
        }
        return this.loginResult
      })
    },
    async startQRLogin() {
      return this.withLoading(async () => {
        this.passwordRequired = false
        this.qrLogin = await apiPost<TelegramQRLoginStartResponse>('/api/telegram/login/qr/start', {})
        return this.qrLogin
      })
    },
    async pollQRLogin(loginID: string) {
      const response = await apiGet<TelegramQRLoginStatusResponse>(`/api/telegram/login/qr/${loginID}`)
      this.qrLogin = response
      this.loginResult = response
      if (response.account) {
        await this.loadAccounts()
      }
      return response
    },
    async cancelQRLogin(loginID: string) {
      await apiDelete<{ canceled: boolean }>(`/api/telegram/login/qr/${loginID}`)
      this.qrLogin = null
    },
    async loadAccounts() {
      return this.withLoading(async () => {
        const response = await apiGet<TelegramAccountsResponse>('/api/accounts')
        this.accounts = response.items
        return this.accounts
      })
    },
    async logoutAccount(id: number) {
      return this.withLoading(async () => {
        const account = await apiPost<TelegramAccount>(`/api/accounts/${id}/logout`)
        await this.loadAccounts()
        return account
      })
    },
    async deleteAccount(id: number) {
      return this.withLoading(async () => {
        await apiDelete<{ deleted: boolean }>(`/api/accounts/${id}`)
        await this.loadAccounts()
      })
    },
    async syncAccountChannels(id: number) {
      return this.withLoading(async () => {
        await apiPost(`/api/accounts/${id}/channels/sync-metadata`)
        await this.loadAccounts()
      })
    },
    async syncAccountAvatar(id: number) {
      return this.withLoading(async () => {
        await apiPost(`/api/accounts/${id}/sync-avatar`)
      })
    },
    async withLoading<T>(fn: () => Promise<T>): Promise<T> {
      this.loading = true
      this.error = ''
      try {
        return await fn()
      } catch (error) {
        this.error = error instanceof Error ? error.message : '请求失败'
        throw error
      } finally {
        this.loading = false
      }
    }
  }
})
