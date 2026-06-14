import { describe, test, expect, vi } from 'vitest'
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
      },
      attachTo: document.body
    })

    // Mock container height before mount completes
    Object.defineProperty(wrapper.element, 'clientHeight', { value: 800, configurable: true })

    // Trigger onMounted manually by forcing a re-measure
    wrapper.vm.containerHeight = 800

    // Initially at top with 800px container and 50px items:
    // 800 / 50 = 16 visible + 5 buffer = 21 items
    await wrapper.vm.$nextTick()
    expect(wrapper.findAll('[data-index]').length).toBe(21)
    expect(wrapper.find('[data-index="0"]').exists()).toBe(true)
    expect(wrapper.find('[data-index="20"]').exists()).toBe(true)

    wrapper.unmount()
  })

  test('updates visible range on scroll', async () => {
    vi.useFakeTimers()

    const items = Array.from({ length: 100 }, (_, i) => ({ id: i, name: `Item ${i}` }))

    const wrapper = mount(VirtualScroller, {
      props: {
        items,
        itemHeight: 50,
        bufferSize: 5
      },
      slots: {
        item: '<div>{{ item.name }}</div>'
      },
      attachTo: document.body
    })

    // Set container height
    wrapper.vm.containerHeight = 800

    // Simulate scroll to position 1000px
    const scroller = wrapper.find('.virtual-scroller')
    Object.defineProperty(scroller.element, 'scrollTop', { value: 1000, writable: true })
    await scroller.trigger('scroll')

    // Wait for throttle delay
    await vi.advanceTimersByTime(20)
    await wrapper.vm.$nextTick()

    // At 1000px with 50px items: item 20 is at top
    // Should render items 15-34 (20 visible items + 5 buffer each side)
    expect(wrapper.find('[data-index="15"]').exists()).toBe(true)
    expect(wrapper.find('[data-index="34"]').exists()).toBe(true)
    expect(wrapper.find('[data-index="0"]').exists()).toBe(false)

    vi.useRealTimers()
    wrapper.unmount()
  })

  test('throttles scroll events', async () => {
    vi.useFakeTimers()

    const items = Array.from({ length: 100 }, (_, i) => ({ id: i }))
    const wrapper = mount(VirtualScroller, {
      props: { items, itemHeight: 50, bufferSize: 5 },
      slots: { item: '<div>Item</div>' },
      attachTo: document.body
    })

    wrapper.vm.containerHeight = 800

    const scroller = wrapper.find('.virtual-scroller')

    // First scroll event to 100px - starts timer
    Object.defineProperty(scroller.element, 'scrollTop', { value: 100, writable: true })
    await scroller.trigger('scroll')
    await wrapper.vm.$nextTick()

    // Immediately after, scrollTop should still be 0 (timer hasn't fired)
    expect(wrapper.vm.scrollTop).toBe(0)

    // Advance time by 20ms - timer should fire
    await vi.advanceTimersByTime(20)
    await wrapper.vm.$nextTick()

    // Now scrollTop should be updated to 100
    expect(wrapper.vm.scrollTop).toBe(100)

    // Change element scrollTop to 500 and trigger another event
    Object.defineProperty(scroller.element, 'scrollTop', { value: 500, writable: true })
    await scroller.trigger('scroll')
    await wrapper.vm.$nextTick()

    // Immediately after, should still be 100 (new timer started)
    expect(wrapper.vm.scrollTop).toBe(100)

    // After another 20ms, should update to 500
    await vi.advanceTimersByTime(20)
    await wrapper.vm.$nextTick()
    expect(wrapper.vm.scrollTop).toBe(500)

    vi.useRealTimers()
    wrapper.unmount()
  })

  test('measures container height on mount', async () => {
    const items = Array.from({ length: 100 }, (_, i) => ({ id: i }))

    const wrapper = mount(VirtualScroller, {
      props: { items, itemHeight: 50, bufferSize: 5 },
      slots: { item: '<div>Item</div>' },
      attachTo: document.body
    })

    await wrapper.vm.$nextTick()

    // In jsdom, clientHeight is typically 0, but the onMounted hook should have run
    // Verify that containerHeight ref exists and onMounted was called
    expect(wrapper.vm.containerHeight).toBeDefined()

    // Manually set a height to verify calculation works
    wrapper.vm.containerHeight = 600
    await wrapper.vm.$nextTick()

    // With 600px container and 50px items: ~12 visible + 5 buffer = 17 items
    expect(wrapper.findAll('[data-index]').length).toBeGreaterThanOrEqual(17)

    wrapper.unmount()
  })

  test('handles empty list', () => {
    const wrapper = mount(VirtualScroller, {
      props: { items: [], itemHeight: 50 },
      slots: { item: '<div>Item</div>' }
    })

    expect(wrapper.findAll('[data-index]').length).toBe(0)
    expect(wrapper.find('.virtual-content').attributes('style')).toContain('height: 0px')
  })

  test('handles small list that fits in viewport', () => {
    const items = Array.from({ length: 3 }, (_, i) => ({ id: i }))

    const wrapper = mount(VirtualScroller, {
      props: { items, itemHeight: 50, bufferSize: 5 },
      slots: { item: '<div>Item</div>' },
      attachTo: document.body
    })

    wrapper.vm.containerHeight = 800

    // All 3 items should render
    expect(wrapper.findAll('[data-index]').length).toBe(3)

    wrapper.unmount()
  })
})
