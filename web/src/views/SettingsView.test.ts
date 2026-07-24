import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiDelete, apiGet, apiPost, apiPut } from '@/api/client'
import SettingsView from './SettingsView.vue'

const messageMocks = vi.hoisted(() => ({
  error: vi.fn(),
  success: vi.fn()
}))

const dialogMocks = vi.hoisted(() => ({
  warning: vi.fn()
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useMessage: () => messageMocks,
    useDialog: () => dialogMocks
  }
})

vi.mock('@/api/client', () => ({
  apiGet: vi.fn((path: string) => {
    if (path === '/api/auth/me') {
      return Promise.resolve({ id: 1, username: 'admin', role: 'admin' })
    }
    if (path === '/api/storage/usage') {
      return Promise.resolve({
        db_bytes: 100,
        index_bytes: 200,
        media_cache_bytes: 300,
        total_bytes: 600,
        max_db_bytes: 15 * 1024 * 1024 * 1024,
        max_media_bytes: 25 * 1024 * 1024 * 1024,
        db_over_quota: false,
        media_over_quota: false
      })
    }
    if (path === '/api/settings/telegram-api') {
      return Promise.resolve({ configured: true, app_id: 123456, app_hash_set: true })
    }
    if (path === '/api/settings/telegram-bot') {
      return Promise.resolve({ enabled: false, configured: false, token_set: false, poll_interval: '3s' })
    }
    if (path === '/api/settings/ai/providers') {
      return Promise.resolve({
        items: [
          {
            id: 'openai_compatible',
            name: 'OpenAI Compatible',
            base_url: '',
            default_model: 'qwen2.5-7b-instruct',
            website: 'https://platform.openai.com/docs/api-reference/chat',
            free: false,
            local: false,
            requires_api_key: true
          },
          {
            id: 'groq',
            name: 'Groq',
            base_url: 'https://api.groq.com/openai/v1',
            default_model: 'llama-3.3-70b-versatile',
            api_key_env: 'GROQ_API_KEY',
            website: 'https://console.groq.com/keys',
            free: true,
            local: false,
            requires_api_key: true
          },
          {
            id: 'ollama',
            name: 'Ollama',
            base_url: 'http://localhost:11434/v1',
            default_model: 'qwen2.5:7b',
            website: 'https://ollama.com/download',
            free: true,
            local: true,
            requires_api_key: false
          }
        ]
      })
    }
    if (path === '/api/saved-searches') {
      return Promise.resolve({
        items: [
          {
            id: 10,
            name: '哪吒3',
            keyword: '哪吒3',
            filters: { type: 'movie', cloud_types: ['quark'] },
            notify_rss: true,
            notify_webhook: false,
            notify_telegram: true,
            telegram_chat_ids: [42],
            enabled: true
          }
        ]
      })
    }
    if (path === '/api/telegram-bot/chats') {
      return Promise.resolve({
        items: [
          { chat_id: 42, title: '', username: 'harold', first_name: 'Harold', last_name: 'Finch', type: 'private', last_seen_at: '2026-06-10T00:00:00Z' },
          { chat_id: 43, title: '资源群', username: '', first_name: '', last_name: '', type: 'group', last_seen_at: '2026-06-10T00:00:00Z' }
        ]
      })
    }
    if (path === '/api/accounts') {
      return Promise.resolve({
        items: [
          { id: 1, phone: '+10000000000', username: 'primary', status: 'ONLINE', last_error: '' },
          { id: 2, phone: '+10000000001', username: '', status: 'ONLINE', last_error: '' }
        ]
      })
    }
    if (path === '/api/channels') {
      return Promise.resolve({
        items: [
          { id: 101, account_id: 1, title: '电影频道', username: 'movies', type: 'channel', member_count: 100, description: '', avatar_state: 'ok', sync_state: 'metadata_only', listen_state: 'enabled', history_sync_enabled: false, sync_profile: 'Normal', listen_enabled: true, remote_search_allowed: false, last_message_id: 0, indexed_message_count: 10, web_access_error: '' },
          { id: 102, account_id: 2, title: '软件频道', username: 'software', type: 'channel', member_count: 80, description: '', avatar_state: 'ok', sync_state: 'metadata_only', listen_state: 'enabled', history_sync_enabled: false, sync_profile: 'Normal', listen_enabled: true, remote_search_allowed: false, last_message_id: 0, indexed_message_count: 8, web_access_error: '' }
        ]
      })
    }
    if (path === '/api/webhooks') {
      return Promise.resolve({
        items: [
          {
            id: 20,
            name: 'n8n',
            url: 'https://example.com/hook',
            events: ['resource.created'],
            enabled: true
          }
        ]
      })
    }
    if (path === '/api/notification-deliveries?limit=10&offset=0') {
      return Promise.resolve({
        items: [
          {
            id: 30,
            event_type: 'resource.created',
            target_type: 'webhook',
            target_id: 20,
            status: 'succeeded',
            retry_count: 0,
            last_error: '',
            created_at: '2026-06-10T00:00:00Z'
          }
        ]
      })
    }
    if (path === '/api/settings/runtime') {
      return Promise.resolve({
        sync: {
          workers: 5,
          history_batch_size: 100,
          telegram_request_interval: '2s'
        },
        storage: {
          max_db_size: 15 * 1024 * 1024 * 1024,
          max_media_cache: 25 * 1024 * 1024 * 1024
        },
        telegram: {
          proxy: '',
          reconnect_timeout: '5m0s',
          dial_timeout: '10s',
          rate_limit: {
            enabled: true,
            rate_per_second: 10,
            burst: 5
          },
          stream: {
            concurrency: 2,
            buffers: 4,
            chunk_timeout: '20s'
          },
          media: {
            concurrency: 2
          }
        },
        ai: {
          media_metadata: {
            enabled: true,
            provider: 'openai_compatible',
            base_url: 'https://api.example.com/v1',
            api_key_set: true,
            model: 'media-model',
            fallback_enabled: true,
            providers: [
              {
                id: 'compatible-main',
                provider: 'openai_compatible',
                base_url: 'https://api.example.com/v1',
                api_key_set: true,
                model: 'media-model',
                enabled: true
              }
            ]
          }
        }
      })
    }
    if (path === '/api/settings/version') {
      return Promise.resolve({
        current_version: 'v1.2.3',
        update_available: false
      })
    }
    if (path === '/api/settings/system-info') {
      return Promise.resolve({
        name: 'Linux',
        version: '6.8.0-124-generic',
        architecture: 'amd64',
        go_version: 'go1.25.0',
        cpu_count: 8,
        hostname: 'tg-search-host'
      })
    }
    if (path === '/api/settings/version?check_update=true') {
      return Promise.resolve({
        current_version: 'v1.2.3',
        latest_version: 'v1.2.4',
        latest_url: 'https://github.com/power721/tg-search/releases/tag/v1.2.4',
        update_available: true
      })
    }
    return Promise.resolve({
      id: 1,
      name: 'default',
      prefix: '12345678',
      key: '12345678123456781234567812345678',
      usage_count: 7,
      created_at: '2026-06-08T00:00:00Z'
    })
  }),
  apiPost: vi.fn((path: string) => {
    if (path === '/api/settings/ai/models') {
      return Promise.resolve({ items: ['gpt-4.1-mini', 'qwen-plus'] })
    }
    if (path === '/api/settings/ai/test') {
      return Promise.resolve({ ok: true, model: 'media-model', latency_ms: 12 })
    }
    if (path === '/api/saved-searches') {
      return Promise.resolve({
        id: 11,
        name: '流浪地球',
        keyword: '流浪地球',
        filters: {},
        notify_rss: true,
        notify_webhook: true,
        notify_telegram: false,
        telegram_chat_ids: [],
        enabled: true
      })
    }
    if (path === '/api/webhooks') {
      return Promise.resolve({
        id: 21,
        name: 'Dify',
        url: 'https://example.com/dify',
        events: ['resource.created'],
        enabled: true
      })
    }
    if (path === '/api/saved-searches/10/test') {
      return Promise.resolve({ items: [], total: 3 })
    }
    return Promise.resolve({
      id: 2,
      name: 'default',
      prefix: '87654321',
      key: '87654321876543218765432187654321',
      usage_count: 0,
      created_at: '2026-06-08T01:00:00Z'
    })
  }),
  apiPut: vi.fn((path: string) => {
    if (path === '/api/settings/telegram-api') {
      return Promise.resolve({ configured: true, app_id: 654321, app_hash_set: true })
    }
    if (path === '/api/settings/runtime') {
      return Promise.resolve({
        sync: {
          workers: 8,
          history_batch_size: 250,
          telegram_request_interval: '1500ms'
        },
        storage: {
          max_db_size: 30 * 1024 * 1024 * 1024,
          max_media_cache: 40 * 1024 * 1024 * 1024
        },
        telegram: {
          proxy: 'socks5://127.0.0.1:1080',
          reconnect_timeout: '6m0s',
          dial_timeout: '15s',
          rate_limit: {
            enabled: false,
            rate_per_second: 12,
            burst: 6
          },
          stream: {
            concurrency: 4,
            buffers: 8,
            chunk_timeout: '30s'
          },
          media: {
            concurrency: 3
          }
        },
      ai: {
        media_metadata: {
          enabled: true,
          provider: 'openai_compatible',
          base_url: 'https://api.example.com/v1',
          api_key_set: true,
          model: 'media-model',
          fallback_enabled: true,
          providers: [
            {
              id: 'compatible-main',
              provider: 'openai_compatible',
              base_url: 'https://api.example.com/v1',
              api_key_set: true,
              model: 'media-model',
              enabled: true
            }
          ]
        }
      }
      })
    }
    if (path === '/api/settings/telegram-bot') {
      return Promise.resolve({ enabled: true, configured: true, token_set: true, poll_interval: '5s' })
    }
    if (path.startsWith('/api/saved-searches/')) {
      return Promise.resolve({
        id: 10,
        name: '哪吒3',
        keyword: '哪吒3',
        filters: { type: 'movie', cloud_types: ['quark'] },
        notify_rss: true,
        notify_webhook: false,
        notify_telegram: true,
        telegram_chat_ids: [42],
        enabled: false
      })
    }
    if (path.startsWith('/api/webhooks/')) {
      return Promise.resolve({
        id: 20,
        name: 'n8n',
        url: 'https://example.com/hook',
        events: ['resource.created'],
        enabled: false
      })
    }
    return Promise.resolve({ id: 1, username: 'root', role: 'admin' })
  }),
  apiDelete: vi.fn().mockResolvedValue({ deleted: true })
}))

const stubs = {
  'n-form': { template: '<form><slot /></form>' },
  'n-form-item': { props: ['label'], template: '<label>{{ label }}<slot /></label>' },
  'n-input': {
    props: ['value', 'type', 'autocomplete', 'placeholder'],
    emits: ['update:value'],
    template: `
      <input
        :data-testid="$attrs['data-testid']"
        :type="type || 'text'"
        :value="value"
        :autocomplete="autocomplete"
        :placeholder="placeholder"
        @input="$emit('update:value', $event.target.value)"
      />
    `
  },
  'n-select': {
    props: ['value', 'options', 'multiple'],
    emits: ['update:value'],
    template: `
      <select
        :data-testid="$attrs['data-testid']"
        :multiple="multiple"
        :value="selectValue(value)"
        @change="$emit('update:value', selectedValue($event.target))"
      >
        <option v-for="option in options" :key="option.value" :value="option.value">
          {{ option.label }}
        </option>
      </select>
    `,
    methods: {
      selectValue(value: unknown): string | string[] {
        return Array.isArray(value) ? value.map((item) => String(item)) : String(value ?? '')
      },
      selectedValue(this: { options: Array<{ value: unknown }> }, target: HTMLSelectElement): unknown {
        const optionValue = (value: string) => {
          const option = this.options.find((item: { value: unknown }) => String(item.value) === value)
          return option ? option.value : value
        }
        if (target.multiple) {
          return Array.from(target.options)
            .filter((option) => option.selected)
            .map((option) => optionValue(option.value))
        }
        return optionValue(target.value)
      }
    }
  },
  'n-button': {
    emits: ['click'],
    template: `<button :data-testid="$attrs['data-testid']" @click="$emit('click')"><slot /></button>`
  },
  'n-tabs': {
    props: ['value'],
    emits: ['update:value'],
    template: `
      <div class="n-tabs" :data-active-tab="value">
        <slot />
      </div>
    `
  },
  'n-tab-pane': {
    props: ['name', 'tab'],
    template: `<section class="n-tab-pane" :data-tab-name="name"><h2>{{ tab }}</h2><slot /></section>`
  }
}

describe('SettingsView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
  })

  it('loads and masks the full api key without rendering the prefix field', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/settings/api-key')
    expect(wrapper.text()).not.toContain('前缀')
    expect(wrapper.text()).not.toContain('12345678123456781234567812345678')
    expect(wrapper.get('[data-testid="api-key-usage-count"]').text()).toBe('7')

    const input = wrapper.get<HTMLInputElement>('[data-testid="api-key-input"]')
    expect(input.element.type).toBe('password')
    expect(input.element.value).toBe('12345678123456781234567812345678')

    await wrapper.get('[data-testid="toggle-api-key-visibility"]').trigger('click')
    expect(input.element.type).toBe('text')
  })

  it('regenerates and keeps the replacement key masked', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()
    await wrapper.get('[data-testid="regenerate-api-key"]').trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/settings/api-key/regenerate')
    expect(wrapper.text()).not.toContain('87654321876543218765432187654321')

    const input = wrapper.get<HTMLInputElement>('[data-testid="api-key-input"]')
    expect(input.element.type).toBe('password')
    expect(input.element.value).toBe('87654321876543218765432187654321')
  })

  it('updates admin credentials from the settings page', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    await wrapper.get('[data-testid="admin-username-input"]').setValue('root')
    await wrapper.get('[data-testid="current-password-input"]').setValue('secret123')
    await wrapper.get('[data-testid="new-password-input"]').setValue('newsecret123')
    await wrapper.get('[data-testid="confirm-password-input"]').setValue('newsecret123')
    await wrapper.get('[data-testid="save-admin-credentials"]').trigger('click')
    await flushPromises()

    expect(apiPut).toHaveBeenCalledWith('/api/settings/admin', {
      username: 'root',
      current_password: 'secret123',
      new_password: 'newsecret123'
    })
    expect(wrapper.get<HTMLInputElement>('[data-testid="current-password-input"]').element.value).toBe('')
    expect(wrapper.get<HTMLInputElement>('[data-testid="new-password-input"]').element.value).toBe('')
  })

  it('renders Chinese placeholders for admin credential inputs', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(wrapper.get<HTMLInputElement>('[data-testid="admin-username-input"]').element.placeholder).toBe('请输入用户名')
    expect(wrapper.get<HTMLInputElement>('[data-testid="current-password-input"]').element.placeholder).toBe('请输入密码')
  })

  it('renders storage limits from the storage usage API', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/storage/usage')
    expect(wrapper.text()).toContain('15.0 GB')
    expect(wrapper.text()).toContain('25.0 GB')
  })

  it('shows current version and checks GitHub release updates', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/settings/version')
    expect(wrapper.get('[data-testid="current-version"]').text()).toBe('v1.2.3')
    expect(wrapper.text()).toContain('尚未检查')

    await wrapper.get('[data-testid="check-version"]').trigger('click')
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/settings/version?check_update=true')
    expect(wrapper.text()).toContain('发现新版本 v1.2.4')
  })

  it('renders system information from the settings API', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/settings/system-info')
    expect(wrapper.get('[data-testid="system-name"]').text()).toBe('Linux')
    expect(wrapper.text()).toContain('6.8.0-124-generic')
    expect(wrapper.text()).toContain('amd64')
    expect(wrapper.text()).toContain('tg-search-host')
    expect(wrapper.text()).toContain('8')
    expect(wrapper.text()).not.toContain('Go 版本')
    expect(wrapper.text()).not.toContain('go1.25.0')
  })

  it('groups settings into six tabs including AI settings', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    const panes = wrapper.findAll('.n-tab-pane')
    expect(panes.map((pane) => pane.attributes('data-tab-name'))).toEqual(['security', 'storage', 'runtime', 'ai', 'notifications', 'system'])
    expect(panes.map((pane) => pane.find('h2').text())).toEqual(['账号与安全', '存储', '运行参数', 'AI增强', '通知集成', '系统'])
    expect(panes[3].text()).toContain('AI 媒体元数据')
  })

  it('loads and saves telegram bot settings', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/settings/telegram-bot')
    expect(wrapper.get<HTMLInputElement>('[data-testid="telegram-bot-poll-interval-input"]').element.value).toBe('3s')

    await wrapper.get<HTMLInputElement>('[data-testid="telegram-bot-enabled-input"]').setValue(true)
    await wrapper.get('[data-testid="telegram-bot-token-input"]').setValue('bot-secret')
    await wrapper.get('[data-testid="telegram-bot-poll-interval-input"]').setValue('5s')
    await wrapper.get('[data-testid="save-telegram-bot"]').trigger('click')
    await flushPromises()

    expect(apiPut).toHaveBeenCalledWith('/api/settings/telegram-bot', {
      enabled: true,
      token: 'bot-secret',
      poll_interval: '5s'
    })
    expect(wrapper.get<HTMLInputElement>('[data-testid="telegram-bot-token-input"]').element.value).toBe('')
  })

  it('creates and manages saved searches from settings', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/saved-searches')
    expect(apiGet).toHaveBeenCalledWith('/api/accounts')
    expect(apiGet).toHaveBeenCalledWith('/api/channels')
    expect(apiGet).toHaveBeenCalledWith('/api/telegram-bot/chats')
    expect(wrapper.text()).toContain('哪吒3')
    expect(wrapper.get('[data-testid="saved-search-account-select"]').text()).toContain('+10000000000')
    expect(wrapper.get('[data-testid="saved-search-channel-select"]').text()).toContain('电影频道')

    await wrapper.get('[data-testid="saved-search-name-input"]').setValue('流浪地球')
    await wrapper.get('[data-testid="saved-search-keyword-input"]').setValue('流浪地球')
    await wrapper.get('[data-testid="saved-search-category-select"]').setValue('cloud_drive')
    const resourceTypeSelect = wrapper.get<HTMLSelectElement>('[data-testid="saved-search-resource-types-select"]')
    for (const option of Array.from(resourceTypeSelect.element.options)) {
      option.selected = ['quark', 'aliyun'].includes(option.value)
    }
    await resourceTypeSelect.trigger('change')
    await wrapper.get('[data-testid="saved-search-account-select"]').setValue('1')
    await wrapper.get('[data-testid="saved-search-channel-select"]').setValue('101')
    await wrapper.get<HTMLInputElement>('[data-testid="saved-search-notify-telegram-input"]').setValue(true)
    await flushPromises()
    const telegramChatSelect = wrapper.get<HTMLSelectElement>('[data-testid="saved-search-telegram-chats-select"]')
    for (const option of Array.from(telegramChatSelect.element.options)) {
      option.selected = option.value === '42'
    }
    await telegramChatSelect.trigger('change')
    await wrapper.get('[data-testid="save-saved-search"]').trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/saved-searches', {
      name: '流浪地球',
      keyword: '流浪地球',
      filters: {
        category: 'cloud_drive',
        cloud_types: ['quark', 'aliyun'],
        account_id: 1,
        channel_id: 101
      },
      notify_rss: true,
      notify_webhook: false,
      notify_telegram: true,
      telegram_chat_ids: [42],
      enabled: true
    })

    const testButton = wrapper.findAll('button').find((button) => button.text() === '测试')
    expect(testButton).toBeTruthy()
    await testButton!.trigger('click')
    await flushPromises()
    expect(apiPost).toHaveBeenCalledWith('/api/saved-searches/10/test')

    const deleteButton = wrapper.findAll('button').find((button) => button.text() === '删除')
    expect(deleteButton).toBeTruthy()
    await deleteButton!.trigger('click')
    await flushPromises()
    expect(apiDelete).toHaveBeenCalledWith('/api/saved-searches/10')
  })

  it('creates and manages webhooks from settings', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/webhooks')
    expect(wrapper.text()).toContain('https://example.com/hook')

    await wrapper.get('[data-testid="webhook-name-input"]').setValue('Dify')
    await wrapper.get('[data-testid="webhook-url-input"]').setValue('https://example.com/dify')
    await wrapper.get('[data-testid="webhook-secret-input"]').setValue('secret')
    await wrapper.get('[data-testid="save-webhook"]').trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/webhooks', {
      name: 'Dify',
      url: 'https://example.com/dify',
      events: ['resource.created'],
      enabled: true,
      secret: 'secret'
    })

    const deleteButtons = wrapper.findAll('button').filter((button) => button.text() === '删除')
    expect(deleteButtons.length).toBeGreaterThan(1)
    await deleteButtons[1].trigger('click')
    await flushPromises()
    expect(apiDelete).toHaveBeenCalledWith('/api/webhooks/20')
  })

  it('renders notification integration enum values in Chinese', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()
    await wrapper.get<HTMLInputElement>('[data-testid="saved-search-notify-telegram-input"]').setValue(true)
    await flushPromises()

    expect(wrapper.text()).toContain('Telegram 机器人')
    expect(wrapper.text()).toContain('@harold (私聊)')
    expect(wrapper.text()).toContain('资源创建')
    expect(wrapper.text()).toContain('发送成功')
    expect(wrapper.text()).toContain('Telegram 消息')
    expect(wrapper.text()).toContain('Webhook')
    expect(wrapper.text()).not.toContain('resource.created')
    expect(wrapper.text()).not.toContain('succeeded')
    expect(wrapper.text()).not.toContain('(private)')
  })

  it('keeps API key and Telegram API panels in the right security column', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    const leftPanels = wrapper.findAll('.security-column-left > .panel')
    const rightPanels = wrapper.findAll('.security-column-right > .panel')

    expect(leftPanels.map((panel) => panel.find('h2').text())).toEqual(['管理员账号'])
    expect(rightPanels.map((panel) => panel.find('h2').text())).toEqual(['API 密钥', 'Telegram API'])
  })

  it('loads and saves runtime settings from the runtime tab', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/settings/runtime')
    expect(wrapper.get<HTMLInputElement>('[data-testid="runtime-workers-input"]').element.value).toBe('5')
    expect(wrapper.get<HTMLInputElement>('[data-testid="runtime-max-db-size-input"]').element.value).toBe('15')
    expect(wrapper.get<HTMLSelectElement>('[data-testid="runtime-max-db-size-unit"]').element.value).toBe('GB')
    expect(wrapper.get<HTMLInputElement>('[data-testid="runtime-max-media-cache-input"]').element.value).toBe('25')
    expect(wrapper.get<HTMLSelectElement>('[data-testid="runtime-max-media-cache-unit"]').element.value).toBe('GB')
    expect(wrapper.find('[data-testid="runtime-max-db-size-unit"] option[value="B"]').exists()).toBe(false)
    expect(wrapper.get<HTMLInputElement>('[data-testid="runtime-rate-enabled-input"]').element.checked).toBe(true)

    await wrapper.get('[data-testid="runtime-workers-input"]').setValue('8')
    await wrapper.get('[data-testid="runtime-history-batch-size-input"]').setValue('250')
    await wrapper.get('[data-testid="runtime-request-interval-input"]').setValue('1500ms')
    await wrapper.get('[data-testid="runtime-max-db-size-input"]').setValue('512')
    await wrapper.get('[data-testid="runtime-max-db-size-unit"]').setValue('MB')
    await wrapper.get('[data-testid="runtime-max-media-cache-input"]').setValue('2')
    await wrapper.get('[data-testid="runtime-max-media-cache-unit"]').setValue('GB')
    await wrapper.get('[data-testid="runtime-proxy-input"]').setValue('socks5://127.0.0.1:1080')
    await wrapper.get('[data-testid="runtime-reconnect-timeout-input"]').setValue('6m')
    await wrapper.get('[data-testid="runtime-dial-timeout-input"]').setValue('15s')
    await wrapper.get('[data-testid="runtime-rate-enabled-input"]').setValue(false)
    await wrapper.get('[data-testid="runtime-rate-per-second-input"]').setValue('12')
    await wrapper.get('[data-testid="runtime-rate-burst-input"]').setValue('6')
    await wrapper.get('[data-testid="runtime-stream-concurrency-input"]').setValue('4')
    await wrapper.get('[data-testid="runtime-stream-buffers-input"]').setValue('8')
    await wrapper.get('[data-testid="runtime-stream-timeout-input"]').setValue('30s')
    await wrapper.get('[data-testid="runtime-media-concurrency-input"]').setValue('3')
    await wrapper.get('[data-testid="save-runtime-settings"]').trigger('click')
    await flushPromises()

    expect(apiPut).toHaveBeenCalledWith('/api/settings/runtime', {
      sync: {
        workers: 8,
        history_batch_size: 250,
        telegram_request_interval: '1500ms'
      },
      storage: {
        max_db_size: 512 * 1024 * 1024,
        max_media_cache: 2 * 1024 * 1024 * 1024
      },
      telegram: {
        proxy: 'socks5://127.0.0.1:1080',
        reconnect_timeout: '6m',
        dial_timeout: '15s',
        rate_limit: {
          enabled: false,
          rate_per_second: 12,
          burst: 6
        },
        stream: {
          concurrency: 4,
          buffers: 8,
          chunk_timeout: '30s'
        },
        media: {
          concurrency: 3
        }
      },
      ai: {
        media_metadata: {
          enabled: true,
          provider: 'openai_compatible',
          base_url: 'https://api.example.com/v1',
          api_key: '',
          model: 'media-model',
          fallback_enabled: true,
          providers: [
            {
              id: 'compatible-main',
              name: '',
              provider: 'openai_compatible',
              base_url: 'https://api.example.com/v1',
              api_key: '',
              model: 'media-model',
              enabled: true
            }
          ]
        }
      }
    })
    expect(messageMocks.success).toHaveBeenCalledWith('媒体下载并发已立即生效，其余运行参数重启后生效')
  })

  it('loads model list and saves AI media metadata settings', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(wrapper.get<HTMLInputElement>('[data-testid="ai-media-enabled-input"]').element.checked).toBe(true)
    expect(wrapper.get<HTMLSelectElement>('[data-testid="ai-provider-input-0"]').element.value).toBe('openai_compatible')
    expect(wrapper.get<HTMLInputElement>('[data-testid="ai-base-url-input-0"]').element.value).toBe('https://api.example.com/v1')
    expect(wrapper.get<HTMLInputElement>('[data-testid="ai-api-key-input-0"]').element.value).toBe('')
    expect(wrapper.get<HTMLSelectElement>('[data-testid="ai-model-input-0"]').element.value).toBe('media-model')
    expect(wrapper.get<HTMLInputElement>('[data-testid="ai-fallback-enabled-input"]').element.checked).toBe(true)

    await wrapper.get('[data-testid="ai-provider-input-0"]').setValue('groq')
    expect(wrapper.get<HTMLInputElement>('[data-testid="ai-base-url-input-0"]').element.value).toBe('https://api.groq.com/openai/v1')
    expect(wrapper.get<HTMLSelectElement>('[data-testid="ai-model-input-0"]').element.value).toBe('llama-3.3-70b-versatile')
    expect(wrapper.get('[data-testid="ai-provider-website-0"]').attributes('href')).toBe('https://console.groq.com/keys')

    await wrapper.get('[data-testid="ai-api-key-input-0"]').setValue('new-secret')
    await wrapper.get('[data-testid="test-ai-provider-0"]').trigger('click')
    await flushPromises()
    expect(apiPost).toHaveBeenCalledWith('/api/settings/ai/test', {
      id: 'compatible-main',
      provider: 'groq',
      base_url: 'https://api.groq.com/openai/v1',
      api_key: 'new-secret',
      model: 'llama-3.3-70b-versatile'
    })

    await wrapper.get('[data-testid="fetch-ai-models-0"]').trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/settings/ai/models', {
      provider: 'groq',
      base_url: 'https://api.groq.com/openai/v1',
      api_key: 'new-secret'
    })
    await wrapper.get('[data-testid="ai-model-input-0"]').setValue('qwen-plus')
    await wrapper.get('[data-testid="save-runtime-settings"]').trigger('click')
    await flushPromises()

    expect(apiPut).toHaveBeenCalledWith('/api/settings/runtime', expect.objectContaining({
      ai: {
        media_metadata: {
          enabled: true,
          provider: 'groq',
          base_url: 'https://api.groq.com/openai/v1',
          api_key: 'new-secret',
          model: 'qwen-plus',
          fallback_enabled: true,
          providers: [
            expect.objectContaining({
              provider: 'groq',
              base_url: 'https://api.groq.com/openai/v1',
              api_key: 'new-secret',
              model: 'qwen-plus',
              enabled: true
            })
          ]
        }
      }
    }))
  })

  it('keeps restart-only success text when media concurrency is unchanged', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    await wrapper.get('[data-testid="runtime-workers-input"]').setValue('8')
    await wrapper.get('[data-testid="save-runtime-settings"]').trigger('click')
    await flushPromises()

    expect(messageMocks.success).toHaveBeenCalledWith('运行参数已保存，重启后生效')
  })

  it('rejects storage limits below 100 MB before saving runtime settings', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()
    vi.mocked(apiPut).mockClear()

    await wrapper.get('[data-testid="runtime-max-db-size-input"]').setValue('99')
    await wrapper.get('[data-testid="runtime-max-db-size-unit"]').setValue('MB')
    await wrapper.get('[data-testid="save-runtime-storage"]').trigger('click')
    await flushPromises()

    expect(apiPut).not.toHaveBeenCalled()
  })

  it('updates Telegram API credentials from the settings page', async () => {
    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/settings/telegram-api')
    expect(wrapper.get<HTMLInputElement>('[data-testid="telegram-app-id-input"]').element.value).toBe('123456')

    await wrapper.get('[data-testid="telegram-app-id-input"]').setValue('654321')
    await wrapper.get('[data-testid="telegram-app-hash-input"]').setValue('new-hash-secret')
    await wrapper.get('[data-testid="save-telegram-api"]').trigger('click')
    await flushPromises()

    expect(apiPut).toHaveBeenCalledWith('/api/settings/telegram-api', {
      app_id: 654321,
      app_hash: 'new-hash-secret'
    })
  })

  it('does not show default Telegram API credentials as saved settings', async () => {
    vi.mocked(apiGet).mockImplementation((path: string) => {
      if (path === '/api/auth/me') {
        return Promise.resolve({ id: 1, username: 'admin', role: 'admin' })
      }
      if (path === '/api/storage/usage') {
        return Promise.resolve({
          db_bytes: 100,
          index_bytes: 200,
          media_cache_bytes: 300,
          total_bytes: 600,
          max_db_bytes: 15 * 1024 * 1024 * 1024,
          max_media_bytes: 25 * 1024 * 1024 * 1024,
          db_over_quota: false,
          media_over_quota: false
        })
      }
      if (path === '/api/settings/telegram-api') {
        return Promise.resolve({ configured: false, app_id: 0, app_hash_set: false })
      }
      if (path === '/api/settings/telegram-bot') {
        return Promise.resolve({ enabled: false, configured: false, token_set: false, poll_interval: '3s' })
      }
      if (path === '/api/saved-searches') {
        return Promise.resolve({ items: [] })
      }
      if (path === '/api/accounts') {
        return Promise.resolve({ items: [] })
      }
      if (path === '/api/channels') {
        return Promise.resolve({ items: [] })
      }
      if (path === '/api/webhooks') {
        return Promise.resolve({ items: [] })
      }
      if (path === '/api/notification-deliveries?limit=10&offset=0') {
        return Promise.resolve({ items: [] })
      }
      if (path === '/api/settings/runtime') {
        return Promise.resolve({
          sync: {
            workers: 5,
            history_batch_size: 100,
            telegram_request_interval: '2s'
          },
          storage: {
            max_db_size: 15 * 1024 * 1024 * 1024,
            max_media_cache: 25 * 1024 * 1024 * 1024
          },
          telegram: {
            proxy: '',
            reconnect_timeout: '5m0s',
            dial_timeout: '10s',
            rate_limit: {
              enabled: true,
              rate_per_second: 10,
              burst: 5
            },
            stream: {
              concurrency: 2,
              buffers: 4,
              chunk_timeout: '20s'
            },
            media: {
              concurrency: 2
            }
          }
        })
      }
      if (path === '/api/settings/version') {
        return Promise.resolve({
          current_version: 'dev',
          update_available: false
        })
      }
      if (path === '/api/settings/system-info') {
        return Promise.resolve({
          name: 'Linux',
          version: '6.8.0-124-generic',
          architecture: 'amd64',
          go_version: 'go1.25.0',
          cpu_count: 8,
          hostname: 'tg-search-host'
        })
      }
      if (path === '/api/settings/version?check_update=true') {
        return Promise.resolve({
          current_version: 'dev',
          latest_version: 'v1.2.4',
          latest_url: 'https://github.com/power721/tg-search/releases/tag/v1.2.4',
          update_available: false
        })
      }
      return Promise.resolve({
        id: 1,
        name: 'default',
        prefix: '12345678',
        key: '12345678123456781234567812345678',
        usage_count: 7,
        created_at: '2026-06-08T00:00:00Z'
      })
    })

    const wrapper = mount(SettingsView, {
      global: {
        stubs
      }
    })
    await flushPromises()

    expect(wrapper.get<HTMLInputElement>('[data-testid="telegram-app-id-input"]').element.value).toBe('')
    expect(wrapper.get<HTMLInputElement>('[data-testid="telegram-app-hash-input"]').element.placeholder).toBe('请输入 App Hash')
    expect(wrapper.text()).not.toContain('26375241')
  })

  it('enqueues a media-title repair task from the system tab', async () => {
    vi.mocked(apiPost).mockResolvedValueOnce({ task_id: 42, status: 'queued' })
    const wrapper = mount(SettingsView, { global: { stubs } })
    await flushPromises()

    await wrapper.get('[data-testid="repair-media-title"]').trigger('click')
    expect(dialogMocks.warning).toHaveBeenCalledTimes(1)
    const options = dialogMocks.warning.mock.calls[0][0] as { title: string; onPositiveClick: () => Promise<void> }
    expect(options.title).toBe('修复媒体标题')
    await options.onPositiveClick()
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/maintenance/media-title/repair')
    expect(messageMocks.success).toHaveBeenCalledWith(expect.stringContaining('修复任务已开始'))
  })
})
