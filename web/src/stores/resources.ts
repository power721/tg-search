import { defineStore } from 'pinia'
import { apiGet, apiPost } from '@/api/client'
import type {
  DashboardResourceStatsResponse,
  LinksGroupedResponse,
  ResourceItem,
  ResourcesGroupedResponse,
  ResourcesResponse
} from '@/api/types'

export interface ResourceDeleteManyResult {
  deleted: number
  missing_ids: string[]
}

export interface ResourceFilters {
  keyword?: string
  type?: string
  category?: string
  channelId?: number
  extension?: string
  limit?: number
  offset?: number
}

function buildResourcePath(path: string, filters: ResourceFilters = {}, includeLimit = true) {
  const params = new URLSearchParams()
  const keyword = filters.keyword?.trim()
  if (keyword) params.set('q', keyword)
  if (filters.type) params.set('type', filters.type)
  if (filters.category) params.set('category', filters.category)
  if (filters.channelId) params.set('channel_id', String(filters.channelId))
  if (filters.extension) params.set('extension', filters.extension)
  if (includeLimit) params.set('limit', String(filters.limit ?? 50))
  if (filters.offset) params.set('offset', String(filters.offset))
  const query = params.toString()
  return query ? `${path}?${query}` : path
}

export const useResourcesStore = defineStore('resources', {
  state: () => ({
    items: [] as ResourceItem[],
    total: 0,
    grouped: {} as Record<string, number>,
    dashboardGrouped: {} as Record<string, number>,
    linkTypesGrouped: {} as Record<string, number>,
    loading: false,
    error: ''
  }),
  actions: {
    async load(filters: ResourceFilters = {}) {
      return this.withLoading(async () => {
        const response = await apiGet<ResourcesResponse>(buildResourcePath('/api/resources', filters))
        this.items = response.items
        this.total = response.total
        return response
      })
    },
    async loadGrouped(filters: ResourceFilters = {}) {
      return this.withLoading(async () => {
        const response = await apiGet<ResourcesGroupedResponse>(
          buildResourcePath('/api/resources/grouped', filters, false)
        )
        this.grouped = response.grouped
        return response.grouped
      })
    },
    async loadDashboardGrouped() {
      return this.withLoading(async () => {
        const response = await apiGet<DashboardResourceStatsResponse>('/api/dashboard/resource-stats')
        this.dashboardGrouped = response.grouped
        return response.grouped
      })
    },
    async loadLinkTypesGrouped() {
      return this.withLoading(async () => {
        const response = await apiGet<LinksGroupedResponse>('/api/links/grouped')
        this.linkTypesGrouped = response.grouped
        return response.grouped
      })
    },
    async deleteResource(id: string) {
      return this.deleteResources([id])
    },
    async deleteResources(ids: string[]) {
      return this.withLoading(async () => {
        const result = await apiPost<ResourceDeleteManyResult>('/api/resources/bulk-delete', { ids })
        const missing = new Set(result.missing_ids ?? [])
        this.removeResources(ids.filter((id) => !missing.has(id)))
        return result
      })
    },
    removeResources(ids: string[]) {
      const targets = new Set(ids)
      const removed = this.items.filter((item) => targets.has(item.id)).length
      this.items = this.items.filter((item) => !targets.has(item.id))
      this.total = Math.max(0, this.total - removed)
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
