import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiGet } from '@/api/client'
import HomeView from './HomeView.vue'

const { routerPush } = vi.hoisted(() => ({
  routerPush: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiGet: vi.fn((path: string) => {
    if (path === '/api/auth/me') {
      return Promise.resolve({ id: 1, username: 'admin', role: 'admin' })
    }
    if (path === '/api/status') {
      return Promise.resolve({
        service: 'ok',
        accounts: 1,
        channels: 2,
        messages: 100,
        links: 30,
        account_states: { ONLINE: 1 }
      })
    }
    if (path === '/api/storage/usage') {
      return Promise.resolve({
        db_bytes: 3_200_000_000,
        index_bytes: 1_100_000_000,
        media_cache_bytes: 0,
        total_bytes: 4_300_000_000,
        max_db_bytes: 10_000_000_000,
        max_media_bytes: 20_000_000_000,
        db_over_quota: false,
        media_over_quota: false
      })
    }
    if (path === '/api/dashboard/resource-stats') {
      return Promise.resolve({
        grouped: { cloud_drive: 2, magnet: 1, ed2k: 0, http: 3, files: 4 }
      })
    }
    if (path === '/api/links/grouped') {
      return Promise.resolve({
        grouped: { aliyun: 2, quark: 3, magnet: 1 }
      })
    }
    if (path.startsWith('/api/tasks')) {
      return Promise.resolve({
        total: 6,
        items: [{ id: 1, type: 'history_sync', status: 'failed', error_message: 'temporary failure' }]
      })
    }
    return Promise.reject(new Error('unexpected path'))
  })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({
    push: routerPush
  })
}))

describe('HomeView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    routerPush.mockReset()
  })

  it('renders storage usage', async () => {
    const wrapper = mount(HomeView)
    await new Promise((resolve) => setTimeout(resolve, 0))
    expect(wrapper.text()).toContain('存储使用')
    expect(wrapper.text()).toContain('管理员账号')
    expect(wrapper.text()).toContain('admin')
    expect(wrapper.text()).toContain('4.3 GB')
    expect(wrapper.text()).toContain('资源类型统计')
    expect(wrapper.text()).toContain('网盘')
    expect(wrapper.text()).toContain('链接类型统计')
    expect(wrapper.text()).toContain('夸克')
    expect(wrapper.text()).toContain('任务')
    expect(wrapper.text()).toContain('6')
    expect(wrapper.text()).toContain('最近任务错误')
    expect(wrapper.text()).toContain('temporary failure')
    expect(wrapper.find('.home-search').exists()).toBe(true)
    expect(wrapper.get('input[name="q"]').attributes('placeholder')).toBe('搜索消息、链接、文件、频道')
    expect(apiGet).toHaveBeenCalledWith('/api/dashboard/resource-stats')
    expect(apiGet).not.toHaveBeenCalledWith('/api/resources/grouped')
  })

  it('navigates home global search to the search page', async () => {
    const wrapper = mount(HomeView)
    await new Promise((resolve) => setTimeout(resolve, 0))

    await wrapper.get('input[name="q"]').setValue(' ubuntu ')
    await wrapper.get('form.home-search').trigger('submit')
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(routerPush).toHaveBeenCalledWith({ path: '/search', query: { q: 'ubuntu' } })
  })
})
