import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiGet, apiPost } from '@/api/client'
import { useResourcesStore } from './resources'

vi.mock('@/api/client', () => ({
  apiGet: vi.fn(),
  apiPost: vi.fn()
}))

describe('useResourcesStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(apiGet).mockReset()
    vi.mocked(apiPost).mockReset()
  })

  it('loads resources and grouped counts', async () => {
    vi.mocked(apiGet)
      .mockResolvedValueOnce({
        items: [{ id: 'link:1', kind: 'link', category: 'cloud_drive', title: 'Course' }],
        total: 1,
        grouped: { cloud_drive: 1, magnet: 0, ed2k: 0, http: 0, files: 0 }
      })
      .mockResolvedValueOnce({
        grouped: { cloud_drive: 1, magnet: 2, ed2k: 0, http: 3, files: 4 }
      })
      .mockResolvedValueOnce({
        grouped: { aliyun: 1, quark: 3, magnet: 2 }
      })
    const store = useResourcesStore()

    await store.load({ keyword: 'course', category: 'cloud_drive', channelId: 7 })
    await store.loadGrouped()
    await store.loadLinkTypesGrouped()

    expect(apiGet).toHaveBeenNthCalledWith(
      1,
      '/api/resources?q=course&category=cloud_drive&channel_id=7&limit=50'
    )
    expect(apiGet).toHaveBeenNthCalledWith(3, '/api/links/grouped')
    expect(store.items[0].title).toBe('Course')
    expect(store.grouped.files).toBe(4)
    expect(store.linkTypesGrouped.quark).toBe(3)
  })

  it('loads dashboard resource type stats from the dashboard endpoint', async () => {
    vi.mocked(apiGet).mockResolvedValue({
      grouped: { cloud_drive: 2, magnet: 1, ed2k: 0, http: 3, files: 4 }
    })
    const store = useResourcesStore()

    await store.loadDashboardGrouped()

    expect(apiGet).toHaveBeenCalledWith('/api/dashboard/resource-stats')
    expect(store.dashboardGrouped.files).toBe(4)
  })

  it('passes page offsets when loading resources', async () => {
    vi.mocked(apiGet).mockResolvedValue({
      items: [],
      total: 75,
      grouped: {}
    })
    const store = useResourcesStore()

    await store.load({ limit: 50, offset: 50 })

    expect(apiGet).toHaveBeenCalledWith('/api/resources?limit=50&offset=50')
    expect(store.total).toBe(75)
  })

  it('deletes selected resources through the bulk endpoint', async () => {
    vi.mocked(apiPost).mockResolvedValue({
      deleted: 1,
      missing_ids: ['link:missing']
    })
    const store = useResourcesStore()
    store.items = [
      { id: 'link:https://example.com/course', kind: 'link', category: 'cloud_drive', title: 'Course' },
      { id: 'file:7', kind: 'file', category: 'files', file_name: 'ubuntu.iso' }
    ]
    store.total = 2

    const result = await store.deleteResources(['link:https://example.com/course', 'link:missing'])

    expect(apiPost).toHaveBeenCalledWith('/api/resources/bulk-delete', {
      ids: ['link:https://example.com/course', 'link:missing']
    })
    expect(result.deleted).toBe(1)
    expect(store.items.map((item) => item.id)).toEqual(['file:7'])
    expect(store.total).toBe(1)
  })
})
