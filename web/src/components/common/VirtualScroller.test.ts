import { describe, test, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import VirtualScroller from './VirtualScroller.vue'

describe('VirtualScroller', () => {
  test('calculates visible range correctly', async () => {
    const items = Array.from({ length: 100 }, (_, i) => ({ id: i, name: `Item ${i}` }))

    const wrapper = mount(VirtualScroller, {
      props: {
        items,
        itemHeight: 50,
        bufferSize: 5
      },
      slots: {
        item: '<div>{{ item.name }}</div>'
      }
    })

    // Initially at top with 800px container and 50px items:
    // 800 / 50 = 16 visible + 5 buffer = 21 items
    await wrapper.vm.$nextTick()
    expect(wrapper.findAll('[data-index]').length).toBe(21)
    expect(wrapper.find('[data-index="0"]').exists()).toBe(true)
    expect(wrapper.find('[data-index="20"]').exists()).toBe(true)
  })
})
