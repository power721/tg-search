import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiDelete, apiGet, apiPost } from '@/api/client'
import AccountsView from './AccountsView.vue'

const push = vi.fn()
const dialogWarning = vi.fn((options: { onPositiveClick?: () => void }) => {
  options.onPositiveClick?.()
})
const messageSuccess = vi.fn()
const messageError = vi.fn()
const defaultAccounts = [
  {
    id: 1,
    phone: '+8613800138000',
    telegram_user_id: 42,
    first_name: 'Ada',
    last_name: 'Lovelace',
    username: 'ada',
    status: 'ONLINE',
    last_online_at: '2026-06-08T02:00:00Z',
    last_error: ''
  },
  {
    id: 2,
    phone: '+8613800138001',
    telegram_user_id: 43,
    first_name: 'Grace',
    last_name: 'Hopper',
    username: 'grace',
    status: 'LOGIN_REQUIRED',
    last_online_at: '',
    last_error: ''
  }
]

vi.mock('vue-router', () => ({
  useRouter: () => ({ push })
}))

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useDialog: () => ({ warning: dialogWarning }),
    useMessage: () => ({ success: messageSuccess, error: messageError })
  }
})

vi.mock('qrcode', () => ({
  default: { toCanvas: vi.fn(() => Promise.resolve()) }
}))

vi.mock('@/api/client', () => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiDelete: vi.fn().mockResolvedValue({ deleted: true })
}))

describe('AccountsView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    vi.mocked(apiGet).mockResolvedValue({ items: defaultAccounts })
    vi.mocked(apiPost).mockImplementation((path: string) => {
      if (path === '/api/telegram/login/qr/start') {
        return Promise.resolve({
          login_id: 'login-1',
          status: 'pending',
          qr_url: 'tg://login?token=one',
          expires_at: new Date(Date.now() + 60_000).toISOString()
        })
      }
      if (path === '/api/telegram/login/sign-in') {
        return Promise.resolve({
          status: 'ONLINE',
          account: { id: 2, phone: '+8613800138001', status: 'ONLINE', last_error: '' },
          metadata_sync: { status: 'succeeded', channel_count: 3 }
        })
      }
      return Promise.resolve({
        id: 1,
        phone: '+8613800138000',
        status: 'LOGIN_REQUIRED',
        last_error: ''
      })
    })
    vi.mocked(apiDelete).mockResolvedValue({ deleted: true })
  })

  it('renders account columns', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    expect(wrapper.text()).toContain('账号')
    expect(wrapper.text()).toContain('头像')
    expect(wrapper.text()).toContain('手机号')
    expect(wrapper.text()).toContain('状态')
    expect(wrapper.text()).toContain('最后在线')
    expect(wrapper.text()).toContain('操作')
    expect(wrapper.text()).toContain('添加账号')
    expect(wrapper.text()).toContain('登出')
    expect(wrapper.text()).toContain('登录')
    expect(wrapper.text()).toContain('删除')
  })

  it('translates reconnecting account status', async () => {
    vi.mocked(apiGet).mockResolvedValueOnce({
      items: [
        {
          id: 3,
          phone: '+8613800138002',
          telegram_user_id: 44,
          first_name: '',
          last_name: '',
          username: 'reconnect',
          status: 'RECONNECTING',
          last_online_at: '',
          last_error: ''
        }
      ]
    })
    const wrapper = mountAccountsView()
    await flushPromises()

    expect(wrapper.text()).toContain('重连中')
    expect(wrapper.text()).not.toContain('RECONNECTING')
  })

  it('shows the next login step for accounts that need login', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    expect(wrapper.text()).toContain('需要登录')
    expect(wrapper.text()).toContain('点击登录后发送验证码，并在 Telegram 官方消息中查看验证码。')
  })

  it('opens telegram login dialog when adding or reconnecting an account', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    const addButton = wrapper.findAll('button').find((button) => button.text() === '添加账号')
    expect(addButton).toBeTruthy()
    await addButton!.trigger('click')

    expect(wrapper.text()).toContain('Telegram 登录')
    expect(wrapper.text()).toContain('扫码登录')
    expect(wrapper.text()).toContain('生成二维码')

    const loginButton = wrapper.findAll('button').find((button) => button.text() === '登录')
    expect(loginButton).toBeTruthy()
    await loginButton!.trigger('click')

    expect(push).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Telegram 登录')
    expect(wrapper.text()).toContain('验证码登录')
    expect((wrapper.find('input[autocomplete="tel"]').element as HTMLInputElement).value).toBe('+8613800138001')
  })

  it('renders Chinese placeholders in the telegram login dialog', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    await wrapper.findAll('button').find((button) => button.text() === '添加账号')!.trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === '验证码登录')!.trigger('click')

    expect(wrapper.get<HTMLInputElement>('input[autocomplete="tel"]').element.placeholder).toBe('+86 13800138000')
    expect(wrapper.get<HTMLInputElement>('input[autocomplete="one-time-code"]').element.placeholder).toBe('请输入验证码')
  })

  it('closes the telegram login dialog from close and cancel controls', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    await wrapper.findAll('button').find((button) => button.text() === '添加账号')!.trigger('click')
    expect(wrapper.text()).toContain('Telegram 登录')
    expect(wrapper.find('[aria-label="关闭 Telegram 登录"]').exists()).toBe(true)

    await wrapper.findAll('button').find((button) => button.text() === '取消')!.trigger('click')
    expect(wrapper.text()).not.toContain('Telegram 登录')
  })

  it('starts qr login from the account dialog and finishes after poll succeeds', async () => {
    vi.mocked(apiGet).mockImplementation((path: string) => {
      if (path === '/api/telegram/login/qr/login-1') {
        return Promise.resolve({
          login_id: 'login-1',
          status: 'online',
          account: { id: 3, phone: '+8613800138002', status: 'ONLINE', last_error: '' },
          metadata_sync: { status: 'succeeded', channel_count: 2 }
        })
      }
      return Promise.resolve({ items: [] })
    })
    const wrapper = mountAccountsView()
    await flushPromises()

    await wrapper.findAll('button').find((button) => button.text() === '添加账号')!.trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === '生成二维码')!.trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/telegram/login/qr/start', {})
    expect(apiGet).toHaveBeenCalledWith('/api/telegram/login/qr/login-1')
    expect(apiGet).toHaveBeenCalledWith('/api/accounts')
    expect(messageSuccess).toHaveBeenCalledWith('Telegram 账号已连接')
  })

  it('paginates accounts on the account page', async () => {
    vi.mocked(apiGet).mockResolvedValueOnce({
      items: Array.from({ length: 21 }, (_, index) => ({
        id: index + 1,
        phone: `+100000000${String(index).padStart(2, '0')}`,
        telegram_user_id: index + 1,
        first_name: `User${index + 1}`,
        last_name: '',
        username: '',
        status: 'ONLINE',
        last_error: ''
      }))
    })

    const wrapper = mountAccountsView()
    await flushPromises()

    expect(wrapper.text()).toContain('+10000000000')
    expect(wrapper.text()).not.toContain('+10000000020')

    await wrapper.find('button[aria-label="下一页"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('+10000000020')
    expect(wrapper.text()).not.toContain('+10000000000')
  })

  it('logs in from the account dialog without navigating to setup', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    const loginButton = wrapper.findAll('button').find((button) => button.text() === '登录')
    expect(loginButton).toBeTruthy()
    await loginButton!.trigger('click')

    await wrapper.findAll('button').find((button) => button.text() === '发送验证码')!.trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('Telegram 已接受验证码请求')
    expect(wrapper.text()).toContain('请先查看已登录 Telegram 客户端里的官方验证码消息')

    const dialogLoginButtons = wrapper.findAll('button').filter((button) => button.text() === '登录')
    await dialogLoginButtons.at(-1)!.trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/telegram/login/send-code', { phone: '+8613800138001' })
    expect(apiPost).toHaveBeenCalledWith('/api/telegram/login/sign-in', {
      phone: '+8613800138001',
      code: ''
    })
    expect(push).not.toHaveBeenCalled()
    expect(apiGet).toHaveBeenCalledWith('/api/accounts')
    expect(messageSuccess).toHaveBeenCalledWith('Telegram 账号已连接')
  })

  it('uses the phone and code typed in the account login dialog', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    const loginButton = wrapper.findAll('button').find((button) => button.text() === '登录')
    expect(loginButton).toBeTruthy()
    await loginButton!.trigger('click')

    const phoneInput = wrapper.find('input[autocomplete="tel"]')
    await phoneInput.setValue('+16502530000')
    await wrapper.findAll('button').find((button) => button.text() === '发送验证码')!.trigger('click')
    await flushPromises()

    await phoneInput.setValue('+442071838750')
    await wrapper.find('input[autocomplete="one-time-code"]').setValue('12345')
    const dialogLoginButtons = wrapper.findAll('button').filter((button) => button.text() === '登录')
    await dialogLoginButtons.at(-1)!.trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/telegram/login/send-code', { phone: '+16502530000' })
    expect(apiPost).toHaveBeenCalledWith('/api/telegram/login/sign-in', {
      phone: '+442071838750',
      code: '12345'
    })
  })

  it('logs out an account from the action column', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    const logoutButton = wrapper.findAll('button').find((button) => button.text() === '登出')
    expect(logoutButton).toBeTruthy()
    await logoutButton!.trigger('click')
    await flushPromises()

    expect(apiPost).toHaveBeenCalledWith('/api/accounts/1/logout')
    expect(apiGet).toHaveBeenCalledWith('/api/accounts')
  })

  it('confirms before deleting an account', async () => {
    const wrapper = mountAccountsView()
    await flushPromises()

    const deleteButton = wrapper.findAll('button').find((button) => button.text() === '删除')
    expect(deleteButton).toBeTruthy()
    await deleteButton!.trigger('click')
    await flushPromises()

    expect(dialogWarning).toHaveBeenCalledWith(
      expect.objectContaining({
        positiveText: '删除账号',
        positiveButtonProps: expect.objectContaining({ type: 'error' })
      })
    )
    expect(apiDelete).toHaveBeenCalledWith('/api/accounts/1')
    expect(apiGet).toHaveBeenCalledWith('/api/accounts')
  })
})

function mountAccountsView() {
  return mount(AccountsView, {
    global: {
      stubs: {
        NButton: {
          emits: ['click'],
          template: '<button v-bind="$attrs" @click="$emit(\'click\', $event)"><slot /></button>'
        },
        NButtonGroup: {
          template: '<div><slot /></div>'
        },
        NForm: {
          template: '<form><slot /></form>'
        },
        NFormItem: {
          props: ['label'],
          template: '<label>{{ label }}<slot /></label>'
        },
        NInput: {
          props: ['value', 'autocomplete', 'placeholder'],
          emits: ['update:value'],
          template:
            '<input :value="value" :autocomplete="autocomplete" :placeholder="placeholder" @input="$emit(\'update:value\', $event.target.value)" />'
        },
        NModal: {
          props: ['show'],
          template: '<div v-if="show"><slot /></div>'
        },
        NCard: {
          props: { closable: Boolean },
          emits: ['close'],
          template:
            '<section><slot name="header" /><button v-if="closable" type="button" aria-label="关闭 Telegram 登录" @click="$emit(\'close\')" /><slot /></section>'
        },
        NTag: {
          template: '<span><slot /></span>'
        }
      }
    }
  })
}
