import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { apiDelete, apiGet, apiPatch, apiPost, apiPut } from '@/api/client'
import ChannelsView from './ChannelsView.vue'

const dialogWarning = vi.fn((options: { onPositiveClick?: () => void | Promise<void> }) => {
  void options.onPositiveClick?.()
})

vi.mock('naive-ui', async () => {
  const actual = await vi.importActual<typeof import('naive-ui')>('naive-ui')
  return {
    ...actual,
    useDialog: () => ({ warning: dialogWarning })
  }
})

vi.mock('@/api/client', () => ({
  apiGet: vi.fn().mockResolvedValue({
    items: [
      {
        id: 1,
        title: 'Movies',
        username: 'movies',
        type: 'channel',
        sync_state: 'metadata_only',
        listen_state: 'disabled',
        sync_profile: 'Normal',
        indexed_message_count: 42,
        member_count: 1200,
        description: 'Public movie releases',
        web_access: true,
        history_sync_enabled: false,
        listen_enabled: false,
        remote_search_allowed: true
      },
      {
        id: 2,
        title: 'Anime',
        username: 'animehub',
        type: 'group',
        sync_state: 'synced',
        listen_state: 'enabled',
        sync_profile: 'Normal',
        indexed_message_count: 8,
        member_count: 200,
        description: 'Private anime discussion',
        web_access: false,
        history_sync_enabled: true,
        listen_enabled: true,
        remote_search_allowed: true
      },
      {
        id: 3,
        title: 'Docs',
        username: 'manuals',
        type: 'supergroup',
        sync_state: 'failed',
        listen_state: 'error',
        sync_profile: 'Normal',
        indexed_message_count: 100,
        member_count: 50,
        description: 'Manuals and docs',
        history_sync_enabled: true,
        listen_enabled: true,
        remote_search_allowed: true
      },
      {
        id: 4,
        title: 'Saved',
        username: '',
        type: 'saved_messages',
        sync_state: 'synced',
        listen_state: 'disabled',
        sync_profile: 'Normal',
        indexed_message_count: 7,
        member_count: 0,
        description: '',
        history_sync_enabled: true,
        listen_enabled: false,
        remote_search_allowed: false
      }
    ]
  }),
  apiDelete: vi.fn().mockResolvedValue({ deleted: true }),
  apiPatch: vi.fn().mockResolvedValue({
    id: 1,
    title: 'Movies',
    username: 'movies',
    type: 'channel',
    sync_state: 'metadata_only',
    listen_state: 'enabled',
    sync_profile: 'Normal',
    web_access: false,
    history_sync_enabled: false,
    listen_enabled: true,
    remote_search_allowed: true
  }),
  apiPost: vi.fn((path: string) => {
    if (path === '/api/channels/1/analyze') {
      return Promise.resolve({
        channel: { id: 1, title: 'Movies' },
        control: {
          history_sync_enabled: false,
          sync_profile: 'Normal',
          listen_enabled: false,
          remote_search_allowed: true
        },
        indexed_counts: { messages: 0, links: 0, files: 0 }
      })
    }
    if (path === '/api/channels/2/analyze') {
      return Promise.resolve({
        channel: { id: 2, title: 'Anime' },
        control: {
          history_sync_enabled: true,
          sync_profile: 'Normal',
          listen_enabled: true,
          remote_search_allowed: true
        },
        watch_rule: {
          id: 9,
          channel_id: 2,
          enabled: true,
          includes: ['动漫'],
          excludes: ['预告'],
          message_types: ['link'],
          link_types: ['cloud_drive']
        },
        indexed_counts: { messages: 0, links: 0, files: 0 }
      })
    }
    if (path === '/api/watch-rules') {
      return Promise.resolve({ id: 10, channel_id: 1, enabled: true })
    }
    if (path === '/api/channels/1/clear') {
      return Promise.resolve({
        channel: {
          id: 1,
          title: 'Movies',
          username: 'movies',
          type: 'channel',
          sync_state: 'metadata_only',
          listen_state: 'disabled',
          sync_profile: 'Normal',
          indexed_message_count: 0,
          member_count: 1200,
          description: 'Public movie releases',
          web_access: true,
          history_sync_enabled: false,
          listen_enabled: false,
          remote_search_allowed: true
        },
        deleted: { messages: 42, links: 3, files: 2 }
      })
    }
    return Promise.resolve({ job_id: 'job-1', status: 'queued' })
  }),
  apiPut: vi.fn((path: string) => {
    if (path === '/api/listen-rules') {
      return Promise.resolve({
        includes: ['全局'],
        excludes: [],
        message_types: ['link', 'text'],
        link_types: ['cloud_drive', 'magnet'],
        ignored_link_patterns: ['t.me', 'toapp.mypikpak.com', 'telegra.ph', 'www.themoviedb.org']
      })
    }
    return Promise.resolve({ id: 9, channel_id: 2, enabled: true })
  })
}))

describe('ChannelsView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.clearAllMocks()
    dialogWarning.mockImplementation((options: { onPositiveClick?: () => void | Promise<void> }) => {
      void options.onPositiveClick?.()
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function channelTitles(wrapper: ReturnType<typeof mount>) {
    return wrapper.findAll('tbody tr').map(rowTitle)
  }

  function channelRow(wrapper: ReturnType<typeof mount>, title: string) {
    const row = wrapper.findAll('tbody tr').find((row) => rowTitle(row) === title)
    if (!row) throw new Error(`missing row ${title}`)
    return row
  }

  function rowTitle(row: ReturnType<ReturnType<typeof mount>['findAll']>[number]) {
    const titleLink = row.find('.channel-title-link')
    if (titleLink.exists()) return titleLink.text()
    const titleWithDescription = row.find('.title-with-description')
    if (titleWithDescription.exists()) return titleWithDescription.text()
    const titleText = row.find('.channel-title-text')
    if (titleText.exists()) return titleText.text()
    return row.findAll('td').at(0)?.text() ?? ''
  }

  function mountChannelsView() {
    return mount(ChannelsView, {
      global: {
        stubs: {
          'n-button': {
            emits: ['click'],
            props: ['disabled', 'loading', 'type'],
            template:
              '<button :disabled="disabled" :data-loading="loading ? \'true\' : \'false\'" @click="$emit(\'click\', $event)"><slot /></button>'
          },
          'n-tag': { template: '<span><slot /></span>' },
          'n-modal': { props: ['show'], template: '<div v-if="show"><slot /></div>' },
          'n-card': { template: '<section class="sync-modal"><slot /></section>' },
          'n-input-number': {
            props: ['value'],
            template: '<input :value="value" @input="$emit(\'update:value\', Number($event.target.value))" />'
          },
          'n-input': {
            props: ['value'],
            template: '<input v-bind="$attrs" :value="value" @input="$emit(\'update:value\', $event.target.value)" />'
          },
          'n-checkbox-group': { template: '<div><slot /></div>' },
          'n-checkbox': {
            props: ['value'],
            template:
              '<label><input type="checkbox" :value="value" @change="$emit(\'update:checked\', $event.target.checked)" /><slot /></label>'
          },
          'n-form': { template: '<form><slot /></form>' },
          'n-form-item': { props: ['label'], template: '<label>{{ label }}<slot /></label>' },
          'n-select': {
            props: ['value', 'options'],
            template:
              '<select v-bind="$attrs" :value="value" @change="$emit(\'update:value\', $event.target.value)"><option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option></select>'
          },
          'n-tooltip': {
            props: ['contentStyle'],
            template:
              '<span class="tooltip"><slot name="trigger" /><span class="tooltip-content" :style="contentStyle"><slot /></span></span>'
          },
          RouterLink: {
            props: ['to'],
            template:
              '<a v-bind="$attrs" :data-route-name="to.name" :data-channel-id="to.query.channel_id"><slot /></a>'
          },
          'n-drawer': true,
          'n-drawer-content': true,
          'n-switch': true
        }
      }
    })
  }

  it('renders channel list in Chinese without remote search controls', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()

    expect(wrapper.text()).toContain('频道')
    expect(wrapper.text()).toContain('Movies')
    expect(wrapper.text()).toContain('@movies')
    expect(wrapper.text()).toContain('频道')
    expect(wrapper.text()).toContain('保存的消息')
    expect(wrapper.text()).toContain('无')
    expect(wrapper.text()).toContain('未监听')
    expect(wrapper.text()).toContain('监听中')
    for (const label of ['头像', '标题', '用户名', '类型', '成员数', '同步状态', '监听状态', '已索引消息', '网页访问', '操作']) {
      expect(wrapper.text()).toContain(label)
    }
    expect(wrapper.findAll('thead th')).toHaveLength(10)
    expect(wrapper.findAll('thead th').some((header) => header.text() === '描述')).toBe(false)
    expect(wrapper.find('.profile-legend').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('同步档位')
    expect(wrapper.text()).not.toContain('普通')
    expect(wrapper.find('.web-access-text').exists()).toBe(false)
    expect(wrapper.text()).toContain('42')
    expect(wrapper.text()).toContain('1200')
    expect(wrapper.text()).toContain('Public movie releases')
    const indexedLink = channelRow(wrapper, 'Movies').find('.indexed-resource-link')
    expect(indexedLink.text()).toBe('42')
    expect(indexedLink.attributes('data-route-name')).toBe('resources')
    expect(indexedLink.attributes('data-channel-id')).toBe('1')
    const titleCell = channelRow(wrapper, 'Movies').findAll('td').at(1)
    expect(titleCell?.find('.tooltip-content').text()).toBe('Public movie releases')
    expect(titleCell?.find('.tooltip-content').attributes('style')).toContain('overflow-wrap: anywhere')
    const usernameCell = channelRow(wrapper, 'Movies').findAll('td').at(2)
    expect(usernameCell?.text()).toBe('@movies')
    const usernameLink = usernameCell?.find('a')
    expect(usernameLink?.attributes('href')).toBe('https://t.me/s/movies')
    expect(usernameLink?.attributes('target')).toBe('_blank')
    const inaccessibleUsernameCell = channelRow(wrapper, 'Anime').findAll('td').at(2)
    expect(inaccessibleUsernameCell?.text()).toBe('@animehub')
    expect(inaccessibleUsernameCell?.find('a').exists()).toBe(false)
    const webAccessCell = channelRow(wrapper, 'Movies').findAll('td').at(8)
    expect(webAccessCell?.text()).toBe('可访问')
    const webAccessLink = webAccessCell?.find('a')
    expect(webAccessLink?.attributes('href')).toBe('https://t.me/s/movies')
    expect(webAccessLink?.attributes('target')).toBe('_blank')
    expect(wrapper.text()).toContain('同步')
    expect(wrapper.text()).toContain('监听')
    expect(wrapper.text()).toContain('规则')
    expect(wrapper.text()).toContain('全局规则')
    expect(wrapper.find('.channel-search').exists()).toBe(true)
    expect(wrapper.find('.type-filter').exists()).toBe(true)
    expect(wrapper.find('.sync-state-filter').exists()).toBe(true)
    expect(wrapper.find('.listen-state-filter').exists()).toBe(true)
    expect(wrapper.find('.web-access-filter').exists()).toBe(true)
    expect(wrapper.find('.sort-select').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('频道类')
    expect(wrapper.text()).not.toContain('Channels')
    expect(wrapper.text()).not.toContain('Refresh')
    expect(wrapper.text()).not.toContain('Edit Controls')
    expect(wrapper.text()).not.toContain('Analyze')
    expect(wrapper.text()).not.toContain('Check Web Access')
    expect(wrapper.text()).not.toContain('No Web Access')
    expect(wrapper.text()).not.toContain('Remote Search')
    expect(wrapper.text()).not.toContain('channel')
    expect(wrapper.text()).not.toContain('metadata_only')
    expect(wrapper.text()).not.toContain('disabled')
    expect(wrapper.find('.remote-input').exists()).toBe(false)
  })

  it('refreshes without passing the click event as account_id', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()
    vi.mocked(apiGet).mockClear()

    await wrapper.find('.page-header button').trigger('click')
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/channels')
  })

  it('searches filters and sorts channel rows from table headers', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()

    await wrapper.find('[data-sort-key="indexed"]').trigger('click')
    expect(channelTitles(wrapper)).toEqual(['Saved', 'Anime', 'Movies', 'Docs'])
    await wrapper.find('[data-sort-key="indexed"]').trigger('click')
    expect(channelTitles(wrapper)).toEqual(['Docs', 'Movies', 'Anime', 'Saved'])
    await wrapper.find('[data-sort-key="username"]').trigger('click')
    expect(channelTitles(wrapper)).toEqual(['Saved', 'Anime', 'Docs', 'Movies'])

    await wrapper.find('.channel-search').setValue('anime')
    expect(channelTitles(wrapper)).toEqual(['Anime'])
    await wrapper.find('.channel-search').setValue('')

    await wrapper.find('.type-filter').setValue('channel')
    expect(channelTitles(wrapper)).toEqual(['Movies'])
    await wrapper.find('.type-filter').setValue('saved_messages')
    expect(channelTitles(wrapper)).toEqual(['Saved'])
    await wrapper.find('.type-filter').setValue('')

    await wrapper.find('.sync-state-filter').setValue('failed')
    expect(channelTitles(wrapper)).toEqual(['Docs'])
    await wrapper.find('.sync-state-filter').setValue('')

    await wrapper.find('.listen-state-filter').setValue('enabled')
    expect(channelTitles(wrapper)).toEqual(['Anime'])
    await wrapper.find('.listen-state-filter').setValue('')

    await wrapper.find('.web-access-filter').setValue('accessible')
    expect(channelTitles(wrapper)).toEqual(['Movies'])
    await wrapper.find('.web-access-filter').setValue('inaccessible')
    expect(channelTitles(wrapper)).toEqual(['Anime'])
    await wrapper.find('.web-access-filter').setValue('unknown')
    expect(channelTitles(wrapper)).toEqual(['Saved', 'Docs'])
  })

  it('checks public channel web access from row and batch actions', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()

    await channelRow(wrapper, 'Movies')
      .findAll('button')
      .find((button) => button.text() === '检测')
      ?.trigger('click')
    expect(apiPost).toHaveBeenCalledWith('/api/channels/web-access/check', { channel_ids: [1] })

    const savedCheckButton = channelRow(wrapper, 'Saved')
      .findAll('button')
      .find((button) => button.text() === '检测')
    expect(savedCheckButton?.attributes('disabled')).toBeDefined()

    await wrapper.find('.batch-web-access-check').trigger('click')
    expect(apiPost).toHaveBeenCalledWith('/api/channels/web-access/check', { channel_ids: [1, 3] })

    await wrapper.find('.type-filter').setValue('saved_messages')
    expect(wrapper.find('.batch-web-access-check').attributes('disabled')).toBeDefined()
  })

  it('does not show row action loading while only the list is loading', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()

    const moviesButtons = channelRow(wrapper, 'Movies').findAll('button')
    expect(moviesButtons.find((button) => button.text() === '同步')?.attributes('data-loading')).toBe('false')
    expect(moviesButtons.find((button) => button.text() === '检测')?.attributes('data-loading')).toBe('false')
    expect(moviesButtons.find((button) => button.text() === '开启监听')?.attributes('data-loading')).toBe('false')
  })

  it('syncs history and toggles listening from row actions', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()

    await channelRow(wrapper, 'Movies')
      .findAll('button')
      .find((button) => button.text() === '同步')
      ?.trigger('click')
    expect(wrapper.find('.sync-modal').text()).toContain('同步记录最大条数')
    await wrapper.find('.sync-modal input').setValue('250')
    await wrapper.findAll('button').find((button) => button.text() === '开始同步')?.trigger('click')
    await channelRow(wrapper, 'Movies')
      .findAll('button')
      .find((button) => button.text() === '开启监听')
      ?.trigger('click')
    await channelRow(wrapper, 'Anime')
      .findAll('button')
      .find((button) => button.text() === '取消监听')
      ?.trigger('click')

    expect(apiPost).toHaveBeenCalledWith('/api/channels/sync', { channel_ids: [1], max_messages: 250 })
    expect(apiPatch).toHaveBeenCalledWith('/api/channels/1/control', {
      history_sync_enabled: false,
      sync_profile: 'Normal',
      listen_enabled: true,
      remote_search_allowed: true
    })
    expect(apiPatch).toHaveBeenCalledWith('/api/channels/2/control', {
      history_sync_enabled: true,
      sync_profile: 'Normal',
      listen_enabled: false,
      remote_search_allowed: true
    })
  })

  it('clears a channel after confirmation', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()

    await channelRow(wrapper, 'Movies')
      .findAll('button')
      .find((button) => button.text() === '清空')
      ?.trigger('click')
    await flushPromises()

    expect(dialogWarning).toHaveBeenCalledWith(
      expect.objectContaining({
        title: '清空频道',
        content: '清空「Movies」？这会取消监听，并删除这个频道的所有消息和资源。',
        positiveText: '清空',
        positiveButtonProps: expect.objectContaining({ type: 'error' }),
        negativeText: '取消'
      })
    )
    expect(apiPost).toHaveBeenCalledWith('/api/channels/1/clear')
    expect(channelRow(wrapper, 'Movies').text()).toContain('0')
  })

  it('does not clear a channel when confirmation is cancelled', async () => {
    dialogWarning.mockImplementationOnce(() => undefined)
    const wrapper = mountChannelsView()
    await flushPromises()

    await channelRow(wrapper, 'Movies')
      .findAll('button')
      .find((button) => button.text() === '清空')
      ?.trigger('click')
    await flushPromises()

    expect(dialogWarning).toHaveBeenCalledWith(
      expect.objectContaining({
        title: '清空频道',
        positiveText: '清空'
      })
    )
    expect(apiPost).not.toHaveBeenCalledWith('/api/channels/1/clear')
  })

  it('edits global listen rules from the channel toolbar', async () => {
    vi.mocked(apiGet).mockImplementation((path: string) => {
      if (path === '/api/listen-rules') {
        return Promise.resolve({
          includes: ['电影'],
          excludes: ['预告'],
          message_types: ['link'],
          link_types: ['cloud_drive'],
          ignored_link_patterns: ['t.me', '*.t.me']
        })
      }
      return Promise.resolve({
        items: [
          {
            id: 1,
            title: 'Movies',
            username: 'movies',
            type: 'channel',
            sync_state: 'metadata_only',
            listen_state: 'disabled',
            sync_profile: 'Normal',
            indexed_message_count: 42,
            member_count: 1200,
            description: 'Public movie releases',
            web_access: true,
            history_sync_enabled: false,
            listen_enabled: false,
            remote_search_allowed: true
          },
          {
            id: 2,
            title: 'Anime',
            username: 'animehub',
            type: 'group',
            sync_state: 'synced',
            listen_state: 'enabled',
            sync_profile: 'Normal',
            indexed_message_count: 8,
            member_count: 200,
            description: 'Private anime discussion',
            web_access: false,
            history_sync_enabled: true,
            listen_enabled: true,
            remote_search_allowed: true
          }
        ]
      })
    })
    const wrapper = mountChannelsView()
    await flushPromises()

    await wrapper.findAll('button').find((button) => button.text() === '全局规则')?.trigger('click')
    await flushPromises()
    expect(wrapper.find('.listen-rule-modal').text()).toContain('全局监听规则')
    expect(wrapper.find('.rule-includes').element).toHaveProperty('value', '电影')
    await wrapper.find('.rule-includes').setValue('全局, 资源')
    await wrapper.find('.rule-excludes').setValue('广告')
    await wrapper.findAll('button').find((button) => button.text() === '保存规则')?.trigger('click')
    await flushPromises()

    expect(apiGet).toHaveBeenCalledWith('/api/listen-rules')
    expect(apiPut).toHaveBeenCalledWith('/api/listen-rules', {
      includes: ['全局', '资源'],
      excludes: ['广告'],
      message_types: ['link'],
      link_types: ['cloud_drive'],
      ignored_link_patterns: ['t.me', '*.t.me']
    })
  })

  it('creates updates and removes channel listen rules', async () => {
    const wrapper = mountChannelsView()
    await flushPromises()

    await channelRow(wrapper, 'Movies')
      .findAll('button')
      .find((button) => button.text() === '规则')
      ?.trigger('click')
    await flushPromises()
    expect(wrapper.find('.listen-rule-modal').text()).toContain('Movies')
    await wrapper.find('.rule-includes').setValue('电影')
    await wrapper.find('.rule-excludes').setValue('预告')
    await wrapper.findAll('button').find((button) => button.text() === '保存规则')?.trigger('click')
    await flushPromises()
    expect(apiPost).toHaveBeenCalledWith('/api/watch-rules', {
      channel_id: 1,
      enabled: true,
      includes: ['电影'],
      excludes: ['预告'],
      message_types: ['link', 'text', 'image', 'video', 'audio'],
      link_types: ['cloud_drive', 'magnet', 'ed2k', 'other']
    })

    await channelRow(wrapper, 'Anime')
      .findAll('button')
      .find((button) => button.text() === '规则')
      ?.trigger('click')
    await flushPromises()
    expect(wrapper.find('.rule-includes').element).toHaveProperty('value', '动漫')
    await wrapper.find('.rule-includes').setValue('动画')
    await wrapper.findAll('button').find((button) => button.text() === '保存规则')?.trigger('click')
    await flushPromises()
    expect(apiPut).toHaveBeenCalledWith('/api/watch-rules/9', {
      channel_id: 2,
      enabled: true,
      includes: ['动画'],
      excludes: ['预告'],
      message_types: ['link'],
      link_types: ['cloud_drive']
    })

    await channelRow(wrapper, 'Anime')
      .findAll('button')
      .find((button) => button.text() === '规则')
      ?.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text() === '使用全局规则')?.trigger('click')
    await flushPromises()
    expect(apiDelete).toHaveBeenCalledWith('/api/watch-rules/9')
  })
})

describe('three-state sorting', () => {
  it('cycles through ascending, descending, and default order', async () => {
    const wrapper = mount(ChannelsView)
    await flushPromises()

    // Initial state: sortKey is null (default order)
    const vm = wrapper.vm as any
    expect(vm.sortKey).toBe(null)
    expect(vm.sortDirection).toBe('asc')

    // First click: ascending
    await wrapper.find('[data-sort-key="title"]').trigger('click')
    expect(vm.sortKey).toBe('title')
    expect(vm.sortDirection).toBe('asc')

    // Second click: descending
    await wrapper.find('[data-sort-key="title"]').trigger('click')
    expect(vm.sortKey).toBe('title')
    expect(vm.sortDirection).toBe('desc')

    // Third click: reset to default
    await wrapper.find('[data-sort-key="title"]').trigger('click')
    expect(vm.sortKey).toBe(null)
    expect(vm.sortDirection).toBe('asc')
  })

  it('resets to ascending when switching columns', async () => {
    const wrapper = mount(ChannelsView)
    await flushPromises()

    // Click title to ascending
    await wrapper.find('[data-sort-key="title"]').trigger('click')
    const vm = wrapper.vm as any
    expect(vm.sortKey).toBe('title')
    expect(vm.sortDirection).toBe('asc')

    // Click username - should start at ascending
    await wrapper.find('[data-sort-key="username"]').trigger('click')
    expect(vm.sortKey).toBe('username')
    expect(vm.sortDirection).toBe('asc')
  })

  it('sorts by ID when sortKey is null', async () => {
    const wrapper = mount(ChannelsView)
    await flushPromises()

    const vm = wrapper.vm as any

    // Verify default state
    expect(vm.sortKey).toBe(null)

    // Get filtered channels (should be sorted by ID)
    const filtered = vm.filteredChannels

    // Verify channels are in ID order
    for (let i = 1; i < filtered.length; i++) {
      expect(filtered[i].id).toBeGreaterThan(filtered[i - 1].id)
    }
  })

  it('shows no indicator when sortKey is null', async () => {
    const wrapper = mount(ChannelsView)
    await flushPromises()

    const vm = wrapper.vm as any

    // Default state: no indicators
    expect(vm.sortIndicator('title')).toBe('')
    expect(vm.sortIndicator('username')).toBe('')
    expect(vm.sortIndicator('indexed')).toBe('')

    // Click title: ascending indicator
    await wrapper.find('[data-sort-key="title"]').trigger('click')
    expect(vm.sortIndicator('title')).toBe(' ↑')
    expect(vm.sortIndicator('username')).toBe('')

    // Click title again: descending indicator
    await wrapper.find('[data-sort-key="title"]').trigger('click')
    expect(vm.sortIndicator('title')).toBe(' ↓')

    // Click title third time: no indicator
    await wrapper.find('[data-sort-key="title"]').trigger('click')
    expect(vm.sortIndicator('title')).toBe('')
  })
})
