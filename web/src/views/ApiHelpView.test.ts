import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import ApiHelpView from './ApiHelpView.vue'

describe('ApiHelpView', () => {
  it('documents header-only api key authentication', () => {
    const wrapper = mount(ApiHelpView)
    const text = wrapper.text()

    expect(text).toContain('X-API-Key: YOUR_API_KEY')
    expect(text).toContain('Authorization: YOUR_API_KEY')
    expect(text).not.toContain('Bearer')
    expect(text).not.toContain('api_key=YOUR_API_KEY')
  })

  it('documents the search response example shape', () => {
    const wrapper = mount(ApiHelpView)
    const text = wrapper.text()

    expect(text).toContain('返回示例')
    expect(text).toContain('"merged_by_type"')
    expect(text).toContain('"quark"')
    expect(text).toContain('"url": "https://pan.quark.cn/s/42455f092f5d"')
    expect(text).toContain('"/i/4986016461960711126?exp=1781131335&sig=4bdb7be40232890fbe159fc2cfa9753ff5016bc9e2a35180219eb6760ae8ba7b"')
    expect(text).toContain('"media"')
    expect(text).toContain('"quality": "4K"')
  })

  it('documents the search results response example shape', () => {
    const wrapper = mount(ApiHelpView)
    const text = wrapper.text()

    expect(text).toContain('返回示例（results）')
    expect(text).toContain('"results"')
    expect(text).toContain('"unique_id": "link:https://pan.quark.cn/s/42455f092f5d"')
    expect(text).toContain('"title": "迷墙 更新07集 国语中字 2026 4K【国剧】"')
    expect(text).toContain('"links"')
    expect(text).toContain('"work_title": "迷墙 更新07集 国语中字 2026 4K【国剧】"')
  })

  it('lays out the two search response examples side by side', () => {
    const wrapper = mount(ApiHelpView)
    const responseGrid = wrapper.find('.response-example-grid')

    expect(responseGrid.exists()).toBe(true)
    expect(responseGrid.findAll('.code-card')).toHaveLength(2)
  })

  it('documents the public link check api', () => {
    const wrapper = mount(ApiHelpView)
    const text = wrapper.text()

    expect(text).toContain('/api/check/links')
    expect(text).toContain('"disk_type": "quark"')
    expect(text).toContain('"timeout": 5')
    expect(text).toContain('"timeout_ms": 5000')
    expect(text).toContain('高并发工作池')
    expect(text).toContain('默认并发 128')
    expect(text).toContain('链接检测')
    expect(text).toContain('HTTP/2')
    expect(text).toContain('去重')
    expect(text).toContain('立即生效')
    expect(text).toContain('"state": "ok"')
    expect(text).toContain('bad')
    expect(text).toContain('locked')
    expect(text).toContain('unsupported')
    expect(text).toContain('uncertain')
  })
})
