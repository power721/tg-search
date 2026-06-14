# Channels View Virtual Scrolling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement virtual scrolling for channels list to reduce initial render from 408 to ~25 components, improving route time from 1-2s to 100-200ms.

**Architecture:** Create reusable VirtualScroller component with slot-based rendering. Integrate into ChannelsView for both desktop table and mobile cards. Only render visible items plus small buffer.

**Tech Stack:** Vue 3 Composition API, TypeScript, Vitest, Naive UI

---

## File Structure

**New files:**
- `web/src/components/common/VirtualScroller.vue` - Generic virtual scrolling container (~150 lines)
- `web/src/components/common/VirtualScroller.test.ts` - Unit tests (~120 lines)

**Modified files:**
- `web/src/views/ChannelsView.vue` - Integrate VirtualScroller for desktop and mobile (~40 lines changed)

---

## Task 1: Write Failing Test for VirtualScroller Visible Range Calculation

**Files:**
- Create: `web/src/components/common/VirtualScroller.test.ts`

- [ ] **Step 1: Create test file with visible range test**

```typescript
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

    // Initially at top: should render items 0-14 (10 visible + 5 buffer)
    await wrapper.vm.$nextTick()
    expect(wrapper.findAll('[data-index]').length).toBe(15)
    expect(wrapper.find('[data-index="0"]').exists()).toBe(true)
    expect(wrapper.find('[data-index="14"]').exists()).toBe(true)
  })
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: FAIL with "Cannot find module './VirtualScroller.vue'"

// __CONTINUE_HERE__

---

## Task 2: Create Minimal VirtualScroller Component

**Files:**
- Create: `web/src/components/common/VirtualScroller.vue`

- [ ] **Step 1: Create component scaffold**

```vue
<script setup lang="ts" generic="T">
import { computed, ref } from 'vue'

interface Props<T> {
  items: T[]
  itemHeight: number
  bufferSize?: number
}

const props = withDefaults(defineProps<Props<T>>(), {
  bufferSize: 5
})

const scrollTop = ref(0)
const containerHeight = ref(800) // Default, will measure on mount

const totalHeight = computed(() => props.items.length * props.itemHeight)

const visibleStart = computed(() => 
  Math.max(0, Math.floor(scrollTop.value / props.itemHeight) - props.bufferSize)
)

const visibleEnd = computed(() => 
  Math.min(
    props.items.length,
    Math.ceil((scrollTop.value + containerHeight.value) / props.itemHeight) + props.bufferSize
  )
)

const visibleItems = computed(() => 
  props.items.slice(visibleStart.value, visibleEnd.value)
)

const offsetY = computed(() => visibleStart.value * props.itemHeight)
</script>

<template>
  <div class="virtual-scroller">
    <div 
      class="virtual-content" 
      :style="{
        height: totalHeight + 'px',
        paddingTop: offsetY + 'px'
      }"
    >
      <div 
        v-for="(item, idx) in visibleItems" 
        :key="(item as any).id ?? visibleStart + idx"
        :data-index="visibleStart + idx"
      >
        <slot name="item" :item="item" :index="visibleStart + idx" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.virtual-scroller {
  overflow-y: auto;
  overflow-x: hidden;
  height: 100%;
}

.virtual-content {
  position: relative;
}
</style>
```

- [ ] **Step 2: Run test to verify it passes**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add web/src/components/common/VirtualScroller.vue web/src/components/common/VirtualScroller.test.ts
git commit -m "feat: add VirtualScroller component with visible range calculation"
```

---

## Task 3: Add Scroll Event Handling Test

**Files:**
- Modify: `web/src/components/common/VirtualScroller.test.ts`

- [ ] **Step 1: Add scroll event test**

```typescript
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: FAIL with "scroll event not handled"

---

## Task 4: Implement Scroll Event Handling

**Files:**
- Modify: `web/src/components/common/VirtualScroller.vue`

- [ ] **Step 1: Add scroll handler**

In the `<script setup>` section, add after the computed properties:

```typescript
function handleScroll(event: Event) {
  scrollTop.value = (event.target as HTMLElement).scrollTop
}
```

Update the template's `<div class="virtual-scroller">` line to:

```vue
<div class="virtual-scroller" @scroll="handleScroll">
```

- [ ] **Step 2: Run test to verify it passes**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: PASS (2 tests)

- [ ] **Step 3: Commit**

```bash
git add web/src/components/common/VirtualScroller.vue web/src/components/common/VirtualScroller.test.ts
git commit -m "feat: add scroll event handling to VirtualScroller"
```

---

## Task 5: Add Scroll Throttling Test

**Files:**
- Modify: `web/src/components/common/VirtualScroller.test.ts`

- [ ] **Step 1: Add throttling test**

```typescript
test('throttles scroll events', async () => {
  vi.useFakeTimers()
  
  const items = Array.from({ length: 100 }, (_, i) => ({ id: i }))
  const wrapper = mount(VirtualScroller, {
    props: { items, itemHeight: 50, bufferSize: 5 },
    slots: { item: '<div>Item</div>' }
  })

  const scroller = wrapper.find('.virtual-scroller')
  
  // Trigger 10 scroll events rapidly
  for (let i = 0; i < 10; i++) {
    Object.defineProperty(scroller.element, 'scrollTop', { value: i * 100, writable: true })
    await scroller.trigger('scroll')
  }
  
  // Should only update once after throttle delay
  await vi.advanceTimersByTime(20)
  await wrapper.vm.$nextTick()
  
  // Verify last scroll position took effect
  expect(wrapper.vm.scrollTop).toBe(900)
  
  vi.useRealTimers()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: FAIL with "scrollTop updates on every event (no throttling)"

---

## Task 6: Implement Scroll Throttling

**Files:**
- Modify: `web/src/components/common/VirtualScroller.vue`

- [ ] **Step 1: Add throttled scroll handler**

Replace the `handleScroll` function with:

```typescript
import { computed, ref, onUnmounted } from 'vue'

// ... existing code ...

let scrollTimer: number | null = null

function handleScroll(event: Event) {
  if (scrollTimer !== null) return
  
  scrollTimer = window.setTimeout(() => {
    scrollTop.value = (event.target as HTMLElement).scrollTop
    scrollTimer = null
  }, 16) // ~60fps
}

onUnmounted(() => {
  if (scrollTimer !== null) {
    clearTimeout(scrollTimer)
  }
})
```

- [ ] **Step 2: Run test to verify it passes**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: PASS (3 tests)

- [ ] **Step 3: Commit**

```bash
git add web/src/components/common/VirtualScroller.vue web/src/components/common/VirtualScroller.test.ts
git commit -m "feat: add scroll throttling to VirtualScroller"
```

---

## Task 7: Add Container Height Measurement Test

**Files:**
- Modify: `web/src/components/common/VirtualScroller.test.ts`

- [ ] **Step 1: Add height measurement test**

```typescript
test('measures container height on mount', async () => {
  const items = Array.from({ length: 100 }, (_, i) => ({ id: i }))
  
  const wrapper = mount(VirtualScroller, {
    props: { items, itemHeight: 50, bufferSize: 5 },
    slots: { item: '<div>Item</div>' },
    attachTo: document.body
  })

  // Mock container height
  Object.defineProperty(wrapper.element, 'clientHeight', { value: 600, configurable: true })
  
  await wrapper.vm.$nextTick()
  
  // With 600px container and 50px items: ~12 visible + 5 buffer = 17 items
  expect(wrapper.findAll('[data-index]').length).toBeGreaterThanOrEqual(17)
  
  wrapper.unmount()
})
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: FAIL with "container height not measured"

---

## Task 8: Implement Container Height Measurement

**Files:**
- Modify: `web/src/components/common/VirtualScroller.vue`

- [ ] **Step 1: Add ref and onMounted hook**

Add at the top of `<script setup>`:

```typescript
import { computed, ref, onMounted, onUnmounted } from 'vue'

// ... existing Props interface ...

const scrollerRef = ref<HTMLElement>()
```

Add after the `onUnmounted` hook:

```typescript
onMounted(() => {
  if (scrollerRef.value) {
    containerHeight.value = scrollerRef.value.clientHeight
  }
})
```

Update the template's first `<div>` to:

```vue
<div ref="scrollerRef" class="virtual-scroller" @scroll="handleScroll">
```

- [ ] **Step 2: Run test to verify it passes**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: PASS (4 tests)

- [ ] **Step 3: Commit**

```bash
git add web/src/components/common/VirtualScroller.vue web/src/components/common/VirtualScroller.test.ts
git commit -m "feat: measure container height on mount in VirtualScroller"
```

---

## Task 9: Add Edge Case Tests

**Files:**
- Modify: `web/src/components/common/VirtualScroller.test.ts`

- [ ] **Step 1: Add edge case tests**

```typescript
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
    slots: { item: '<div>Item</div>' }
  })

  // All 3 items should render
  expect(wrapper.findAll('[data-index]').length).toBe(3)
})
```

- [ ] **Step 2: Run tests to verify they pass**

Run: `npm run web:test VirtualScroller.test.ts`
Expected: PASS (6 tests)

- [ ] **Step 3: Commit**

```bash
git add web/src/components/common/VirtualScroller.test.ts
git commit -m "test: add edge case tests for VirtualScroller"
```

---

## Task 10: Integrate VirtualScroller into Desktop Table

**Files:**
- Modify: `web/src/views/ChannelsView.vue`

- [ ] **Step 1: Import VirtualScroller**

Add to the imports section (after Avatar import):

```typescript
import VirtualScroller from '@/components/common/VirtualScroller.vue'
```

- [ ] **Step 2: Wrap desktop table rows with VirtualScroller**

Find the desktop table `<tbody>` section (around line 530-632). It currently looks like:

```vue
<tbody>
  <tr v-if="channels.loading && channels.items.length === 0">
    <td colspan="10"><!-- Loading spinner --></td>
  </tr>
  <tr v-for="channel in displayChannels" :key="channel.id">
    <!-- 10 columns -->
  </tr>
  <tr v-if="!channels.loading && displayChannels.length === 0">
    <td colspan="10"><!-- Empty state --></td>
  </tr>
</tbody>
```

Replace with:

```vue
<tbody>
  <tr v-if="channels.loading && channels.items.length === 0">
    <td colspan="10"><!-- Loading spinner (unchanged) --></td>
  </tr>
  
  <VirtualScroller
    v-else-if="displayChannels.length > 0"
    :items="displayChannels"
    :item-height="60"
    :buffer-size="5"
    class="desktop-virtual-scroller"
  >
    <template #item="{ item: channel }">
      <tr :key="channel.id">
        <!-- Move all 10 columns here (unchanged content) -->
      </tr>
    </template>
  </VirtualScroller>
  
  <tr v-else>
    <td colspan="10"><!-- Empty state (unchanged) --></td>
  </tr>
</tbody>
```

- [ ] **Step 3: Add desktop virtual scroller styles**

Add to the `<style scoped>` section:

```css
.desktop-virtual-scroller {
  display: block;
  height: calc(100vh - 280px);
  overflow-y: auto;
}

.desktop-virtual-scroller :deep(tr) {
  display: table;
  width: 100%;
  table-layout: fixed;
}
```

- [ ] **Step 4: Run TypeScript check**

Run: `npm run web:typecheck`
Expected: No errors

- [ ] **Step 5: Run tests**

Run: `npm run web:test`
Expected: All tests pass

- [ ] **Step 6: Commit**

```bash
git add web/src/views/ChannelsView.vue
git commit -m "feat: integrate VirtualScroller into desktop channels table"
```

---

## Task 11: Integrate VirtualScroller into Mobile Cards

**Files:**
- Modify: `web/src/views/ChannelsView.vue`

- [ ] **Step 1: Wrap mobile cards with VirtualScroller**

Find the mobile view section (around line 643-689). It currently looks like:

```vue
<div v-if="channels.loading && channels.items.length === 0" class="loading">
  <!-- Loading spinner -->
</div>

<div v-for="channel in displayChannels" :key="channel.id" class="mobile-card">
  <!-- Card content -->
</div>

<div v-if="!channels.loading && displayChannels.length === 0" class="empty-state">
  <!-- Empty state -->
</div>
```

Replace with:

```vue
<div v-if="channels.loading && channels.items.length === 0" class="loading">
  <!-- Loading spinner (unchanged) -->
</div>

<VirtualScroller
  v-else-if="displayChannels.length > 0"
  :items="displayChannels"
  :item-height="120"
  :buffer-size="3"
  class="mobile-virtual-scroller"
>
  <template #item="{ item: channel }">
    <div :key="channel.id" class="mobile-card">
      <!-- Move card content here (unchanged) -->
    </div>
  </template>
</VirtualScroller>

<div v-else class="empty-state">
  <!-- Empty state (unchanged) -->
</div>
```

- [ ] **Step 2: Add mobile virtual scroller styles**

Add to the `<style scoped>` section:

```css
.mobile-virtual-scroller {
  height: calc(100vh - 200px);
}
```

- [ ] **Step 3: Run TypeScript check**

Run: `npm run web:typecheck`
Expected: No errors

- [ ] **Step 4: Run tests**

Run: `npm run web:test`
Expected: All tests pass

- [ ] **Step 5: Commit**

```bash
git add web/src/views/ChannelsView.vue
git commit -m "feat: integrate VirtualScroller into mobile channels cards"
```

---

## Task 12: Manual Testing and Verification

**Files:**
- None (manual browser testing)

- [ ] **Step 1: Start dev server**

Run: `npm run web:dev`

- [ ] **Step 2: Test desktop initial load**

1. Navigate to `http://localhost:5173/channels`
2. Open Chrome DevTools → Performance tab
3. Record while navigating to channels
4. Verify: Time to Interactive < 500ms
5. Open Elements tab
6. Verify: ~20-30 `<tr>` elements rendered (not 408)

- [ ] **Step 3: Test desktop scrolling**

1. Scroll slowly down the channels list
2. Verify: Smooth 60fps, no jank
3. Scroll quickly to bottom
4. Verify: No white screen flicker
5. Verify: Content renders correctly

- [ ] **Step 4: Test desktop filtering**

1. Change type filter to "频道"
2. Verify: Instant update, list filtered
3. Type in search box
4. Verify: Real-time filtering, no lag
5. Clear filters
6. Verify: Full list restored

- [ ] **Step 5: Test desktop sorting**

1. Click "标题" column header
2. Verify: List sorts ascending
3. Click again
4. Verify: List sorts descending
5. Click third time
6. Verify: Returns to default order

- [ ] **Step 6: Test desktop operations**

1. Click "同步" button on a channel
2. Verify: Operation works correctly
3. Scroll to middle of list
4. Click "监听" button
5. Verify: Operation works on scrolled item

- [ ] **Step 7: Test mobile layout**

1. Resize browser to mobile width (375px)
2. Verify: Mobile cards render with virtual scrolling
3. Test scrolling, filtering, operations
4. Verify: All functionality works

- [ ] **Step 8: Performance benchmark**

1. Open Chrome DevTools → Performance
2. Record page load
3. Verify: Main thread not blocked during scroll
4. Open Memory tab
5. Take heap snapshot
6. Scroll to bottom
7. Take another snapshot
8. Verify: Memory stable (no leaks)

Notes on manual testing results can be added here after completion.

---

## Task 13: Final Verification and Documentation

**Files:**
- None

- [ ] **Step 1: Run full test suite**

Run: `GOCACHE=/tmp/go-build-cache go test ./... && npm run web:typecheck && npm run web:test`
Expected: All tests pass

- [ ] **Step 2: Verify performance improvement**

Compare before/after metrics:
- Initial render: 408 components → ~25 components ✓
- DOM nodes: ~6000 → ~500 ✓
- Route time: 1-2s → <500ms ✓

- [ ] **Step 3: Clean up and final commit**

```bash
git status
# Verify no untracked files or uncommitted changes
git log --oneline -10
# Verify clean commit history
```

---

## Summary

**Files created:**
- `web/src/components/common/VirtualScroller.vue` (~150 lines)
- `web/src/components/common/VirtualScroller.test.ts` (~120 lines)

**Files modified:**
- `web/src/views/ChannelsView.vue` (~40 lines changed, +60 lines added for styles)

**Tests added:**
- 6 unit tests for VirtualScroller
- Existing ChannelsView tests continue to pass

**Performance improvements:**
- Initial render: 408 components → ~25 components (94% reduction)
- DOM nodes: ~6,000 → ~500 (92% reduction)
- Route time: 1-2 seconds → 100-200ms (80-90% improvement)
- Memory: Significantly reduced (only visible items in memory)

**Functionality preserved:**
- All filtering, sorting, searching works
- All operation buttons (sync, listen, check web access) work
- Both desktop and mobile layouts work
- Empty and loading states work

