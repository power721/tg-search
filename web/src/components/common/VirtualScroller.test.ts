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

  test('updates visible range on scroll', async () => {
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

    // Simulate scroll to position 1000px
    const scroller = wrapper.find('.virtual-scroller')
    Object.defineProperty(scroller.element, 'scrollTop', { value: 1000, writable: true })
    await scroller.trigger('scroll')

    await wrapper.vm.$nextTick()

    // At 1000px with 50px items: item 20 is at top
    // Should render items 15-34 (20 visible items + 5 buffer each side)
    expect(wrapper.find('[data-index="15"]').exists()).toBe(true)
    expect(wrapper.find('[data-index="34"]').exists()).toBe(true)
    expect(wrapper.find('[data-index="0"]').exists()).toBe(false)
  })
})
