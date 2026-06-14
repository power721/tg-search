# Channels View Virtual Scrolling Design

**Date:** 2026-06-14  
**Status:** Approved  
**Author:** Claude (Opus 4.8)

## Problem Statement

The channels page experiences 1-2 second loading delay when routing to `/channels`, even after recent optimizations (avatar lazy loading, conditional sorting). With 408 channels, the browser must:

1. Initialize 408 Vue component instances (each channel row)
2. Create 408+ DOM nodes (table rows + cells, or mobile cards)
3. Mount 408 Avatar components (even with lazy loading)
4. Initialize reactive state for 408 items
5. Parse and execute the 962-line ChannelsView.vue component

**Current behavior:**
- Route to `/channels` → 1-2 second delay before page becomes interactive
- User sees loading state or white screen during initialization
- All 408 rows rendered even though only ~15 are visible

**Root cause:** Rendering all 408 items upfront. The browser and Vue don't know that most items are off-screen.

## Solution Overview

Implement virtual scrolling: only render visible items (~20) plus small buffer (~5 above and below). Dynamically create/destroy components as the user scrolls.

**Key principle:** Don't render what the user can't see.

## Design Details

### 1. Core Virtual Scrolling Algorithm

**Concept:**
- Container has fixed height (e.g., `800px`) and `overflow-y: auto`
- Inner content height = `totalItems × itemHeight` (e.g., `408 × 60px = 24,480px`)
- Calculate visible range based on `scrollTop` position
- Only render items in visible range + buffer

**Calculation:**
```javascript
const visibleStart = Math.max(0, Math.floor(scrollTop / itemHeight) - bufferSize)
const visibleEnd = Math.min(items.length, Math.ceil((scrollTop + containerHeight) / itemHeight) + bufferSize)
const visibleItems = items.slice(visibleStart, visibleEnd)
```

**Example:**
- 408 items, 60px each = 24,480px total height
- Container 800px tall = ~13 visible items
- Scroll to 3000px → items 50-75 visible (with buffer)
- Render only 25 items instead of 408 (94% reduction)

**DOM structure:**
```html
<div class="virtual-scroller" style="height: 800px; overflow-y: auto;">
  <div class="virtual-content" style="height: 24480px; padding-top: 3000px;">
    <!-- Only 25 items rendered here -->
    <div>Item 50</div>
    <div>Item 51</div>
    ...
    <div>Item 75</div>
  </div>
</div>
```

**Padding technique:**
- `padding-top` simulates space of unrendered items above
- `padding-bottom` simulates space of unrendered items below
- Total height maintains correct scrollbar size

### 2. VirtualScroller Component

**File:** `web/src/components/common/VirtualScroller.vue`

**Purpose:** Reusable virtual scrolling container for any list/table with fixed-height items.

**Props:**
```typescript
interface Props<T> {
  items: T[]              // Full list (e.g., 408 channels)
  itemHeight: number      // Fixed height per item (60px desktop, 120px mobile)
  bufferSize?: number     // Items to render above/below visible area (default: 5)
}
```

**Template:**
```vue
<template>
  <div ref="scrollerRef" class="virtual-scroller" @scroll="handleScroll">
    <div 
      class="virtual-content" 
      :style="{
        height: totalHeight + 'px',
        paddingTop: offsetY + 'px'
      }"
    >
      <slot 
        name="item" 
        v-for="(item, idx) in visibleItems" 
        :item="item" 
        :index="visibleStart + idx"
        :key="getItemKey(item, visibleStart + idx)"
      />
    </div>
  </div>
</template>
```

**Script (core logic):**
```typescript
<script setup lang="ts" generic="T">
import { computed, ref, onMounted, onUnmounted } from 'vue'

const props = withDefaults(defineProps<Props<T>>(), {
  bufferSize: 5
})

const scrollerRef = ref<HTMLElement>()
const scrollTop = ref(0)
const containerHeight = ref(0)

// Total height of all items
const totalHeight = computed(() => props.items.length * props.itemHeight)

// Calculate visible range
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

// Throttled scroll handler (60fps)
let scrollTimer: number | null = null
function handleScroll(event: Event) {
  if (scrollTimer !== null) return
  
  scrollTimer = window.setTimeout(() => {
    scrollTop.value = (event.target as HTMLElement).scrollTop
    scrollTimer = null
  }, 16) // ~60fps
}

// Measure container height on mount
onMounted(() => {
  if (scrollerRef.value) {
    containerHeight.value = scrollerRef.value.clientHeight
  }
})

onUnmounted(() => {
  if (scrollTimer !== null) {
    clearTimeout(scrollTimer)
  }
})

function getItemKey(item: T, index: number): string | number {
  return (item as any).id ?? index
}
</script>
```

**Styles:**
```vue
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

**Key implementation details:**

1. **Generic type `T`:** Component works with any item type (channels, accounts, resources)

2. **Throttling:** Scroll events throttled to 16ms (60fps) to avoid excessive re-renders

3. **Computed properties:** Vue automatically recalculates visible range when `scrollTop` or `items` change

4. **Slot pattern:** Parent component passes item template via `#item` slot, maintaining full control over rendering

5. **Key management:** Uses `item.id` if available, falls back to index

6. **Container height measurement:** Measures on mount to calculate how many items fit

### 3. ChannelsView Integration

**Desktop Table Integration:**

**Before:**
```vue
<table class="channels-table">
  <thead>
    <!-- Header rows with sort controls -->
  </thead>
  <tbody>
    <tr v-if="channels.loading && channels.items.length === 0">
      <td colspan="10"><!-- Loading spinner --></td>
    </tr>
    <tr v-for="channel in displayChannels" :key="channel.id">
      <!-- 10 columns of content -->
    </tr>
    <tr v-if="!channels.loading && displayChannels.length === 0">
      <td colspan="10"><!-- Empty state --></td>
    </tr>
  </tbody>
</table>
```

**After:**
```vue
<table class="channels-table">
  <thead>
    <!-- Header rows with sort controls (unchanged) -->
  </thead>
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
          <!-- 10 columns of content (moved from v-for, otherwise unchanged) -->
        </tr>
      </template>
    </VirtualScroller>
    
    <tr v-else>
      <td colspan="10"><!-- Empty state (unchanged) --></td>
    </tr>
  </tbody>
</table>
```

**Mobile Card Integration:**

**Before:**
```vue
<div class="mobile-view">
  <!-- Filters (unchanged) -->
  
  <div v-if="channels.loading && channels.items.length === 0" class="loading">
    <!-- Loading spinner -->
  </div>
  
  <div v-for="channel in displayChannels" :key="channel.id" class="mobile-card">
    <!-- Card content -->
  </div>
  
  <div v-if="!channels.loading && displayChannels.length === 0" class="empty-state">
    <!-- Empty state -->
  </div>
</div>
```

**After:**
```vue
<div class="mobile-view">
  <!-- Filters (unchanged) -->
  
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
        <!-- Card content (moved from v-for, otherwise unchanged) -->
      </div>
    </template>
  </VirtualScroller>
  
  <div v-else class="empty-state">
    <!-- Empty state (unchanged) -->
  </div>
</div>
```

**Styling additions:**

```css
/* Desktop: make table body scrollable */
.desktop-virtual-scroller {
  display: block;
  height: calc(100vh - 280px); /* Adjust based on header/filters height */
  overflow-y: auto;
}

.desktop-virtual-scroller tr {
  display: table;
  width: 100%;
  table-layout: fixed;
}

/* Mobile: make list scrollable */
.mobile-virtual-scroller {
  height: calc(100vh - 200px); /* Adjust based on filters height */
}
```

**Key integration points:**

1. **Conditional rendering:** VirtualScroller only used when `displayChannels.length > 0`
2. **Empty/loading states:** Handled outside VirtualScroller (unchanged logic)
3. **Item template:** Moved into `#item` slot, content unchanged
4. **Parameters:** Desktop 60px rows, mobile 120px cards; desktop buffer 5, mobile buffer 3 (smaller screen)
5. **Existing features preserved:** All sorting, filtering, search, buttons continue working

### 4. Performance Characteristics

**Before virtual scrolling:**
- Initial render: 408 component instances
- DOM nodes: 408 rows × ~15 elements = ~6,000 nodes
- Memory: High (all components in memory)
- Route time: 1-2 seconds

**After virtual scrolling:**
- Initial render: ~25 component instances (visible + buffer)
- DOM nodes: ~25 rows × ~15 elements = ~375 nodes (94% reduction)
- Memory: Low (only visible components in memory)
- Route time: 100-200ms (80-90% improvement)

**Scrolling performance:**
- Throttled to 60fps (16ms per frame)
- Only updates visible range when scrolling stops/slows
- Vue reuses DOM nodes via key-based reconciliation
- Avatar lazy loading only triggers for newly visible items

**Memory management:**
- Scrolled-away components destroyed by Vue
- Only ~25 Avatar images loaded at any time
- Garbage collection can clean up off-screen components

**Combined with existing optimizations:**
1. **Conditional sorting** (just merged): No unnecessary sorting when filters change
2. **Avatar lazy loading** (merged): Images only load when visible
3. **Virtual scrolling** (this): Only render visible items

**Net effect:**
- Initial page load: 408 components + 408 avatars → 25 components + 25 avatars
- Filter/search: Instant update (no sorting overhead)
- Scrolling: Smooth 60fps, no blocking
- Memory: 94% reduction in DOM nodes

### 5. Edge Cases and Error Handling

**Empty list:**
- VirtualScroller not rendered when `displayChannels.length === 0`
- Empty state handled outside VirtualScroller
- No scroll container shown

**Small list (< 1 screen):**
- All items fit in viewport
- No scrollbar appears
- VirtualScroller still works (renders all items)
- No performance penalty

**Fast scrolling:**
- Buffer prevents white screen flicker
- Throttling ensures smooth updates
- If scroll faster than render, buffer items provide content

**Container resize:**
- Window resize doesn't break scrolling
- Could add `ResizeObserver` if needed
- Current implementation uses initial height (sufficient for most cases)

**Sorting/filtering while scrolled:**
- `displayChannels` changes trigger recalculation
- Scroll position can be preserved or reset to top
- Reset to top is simpler and expected behavior

**Item height variation:**
- Design assumes fixed height (60px desktop, 120px mobile)
- If content overflow: use CSS `overflow: hidden` or `text-overflow: ellipsis`
- Variable height would require more complex solution (out of scope)

### 6. Testing Strategy

**Unit tests (VirtualScroller.test.ts):**

```typescript
describe('VirtualScroller', () => {
  test('calculates visible range correctly', () => {
    // Given: 100 items, 50px each, container 500px, scroll to 250px
    // Expect: visibleStart = 0, visibleEnd = 15 (with buffer 5)
  })

  test('handles empty list', () => {
    // Given: items = []
    // Expect: renders nothing, no errors
  })

  test('handles small list', () => {
    // Given: 5 items, container fits 10
    // Expect: renders all 5 items, no scroll
  })

  test('updates on scroll', () => {
    // Given: scrolled from 0 to 1000px
    // Expect: visibleStart/End update correctly
  })

  test('throttles scroll events', () => {
    // Given: 10 scroll events in 10ms
    // Expect: only 1 update triggered
  })
})
```

**Integration tests (ChannelsView.test.ts):**

```typescript
test('desktop table renders virtual scroller with channels', () => {
  // Mount ChannelsView with 50 channels
  // Expect: VirtualScroller present, renders subset of channels
})

test('mobile cards render virtual scroller', () => {
  // Mount ChannelsView in mobile viewport
  // Expect: VirtualScroller present with mobile card template
})

test('filtering updates virtual scroller items', () => {
  // Given: 100 channels
  // When: apply filter → 20 channels
  // Expect: VirtualScroller receives filtered 20 items
})

test('sorting works with virtual scrolling', () => {
  // Given: channels in default order
  // When: click sort column
  // Expect: VirtualScroller items reordered correctly
})

test('channel operations work in virtual list', () => {
  // When: click "Sync" button on a channel
  // Expect: operation triggers, UI updates
})
```

**Manual testing checklist:**

1. **Initial load:**
   - Navigate to `/channels`
   - Verify: page interactive in < 500ms
   - Check: ~20-30 DOM nodes rendered (DevTools Elements panel)

2. **Scrolling:**
   - Scroll slowly down the list
   - Verify: smooth 60fps, no jank
   - Scroll quickly to bottom
   - Verify: no white screen flicker

3. **Filtering:**
   - Change type filter
   - Verify: instant update, scroll resets to top
   - Search for channel name
   - Verify: filtered results render correctly

4. **Sorting:**
   - Click column headers (Title, Username, Indexed Messages)
   - Verify: sorts correctly, scroll resets to top
   - Three-state cycle (asc → desc → default) works

5. **Operations:**
   - Click "Sync", "Listen", "Check Web Access" buttons
   - Verify: operations work on visible items
   - Scroll to bottom, click buttons
   - Verify: operations work on scrolled items

6. **Mobile:**
   - Resize to mobile viewport
   - Verify: mobile cards use virtual scrolling
   - All above tests on mobile layout

7. **Edge cases:**
   - Filter to 0 results → empty state shown
   - Filter to 2 results → no scroll, renders 2 items
   - Search while scrolled → resets to top

**Performance benchmarking:**

1. **Chrome DevTools Performance:**
   - Record navigation to `/channels`
   - Measure: Time to Interactive (should be < 300ms)
   - Check: Main thread not blocked during scroll
   - Verify: No long tasks > 50ms

2. **Memory profiling:**
   - Take heap snapshot after initial load
   - Scroll to middle/bottom
   - Take another snapshot
   - Verify: Memory usage stable (no leaks)
   - Compare: Before/after virtual scrolling (should show 90%+ reduction)

3. **DOM node count:**
   - Before: ~6,000 nodes for 408 channels
   - After: ~500 nodes total (including headers/filters)
   - Reduction: 90%+

### 7. Implementation Plan Summary

**Phase 1: Create VirtualScroller component**
- File: `web/src/components/common/VirtualScroller.vue`
- Logic: Scroll handling, visible range calculation, slot rendering
- Tests: `web/src/components/common/VirtualScroller.test.ts`
- Estimated: ~150 lines component + ~100 lines tests

**Phase 2: Integrate desktop table**
- Modify: `web/src/views/ChannelsView.vue`
- Changes: Wrap `<tbody>` v-for with VirtualScroller + slot
- Styles: Add `.desktop-virtual-scroller` CSS
- Estimated: ~20 lines change

**Phase 3: Integrate mobile cards**
- Modify: `web/src/views/ChannelsView.vue`
- Changes: Wrap mobile v-for with VirtualScroller + slot
- Styles: Add `.mobile-virtual-scroller` CSS
- Estimated: ~15 lines change

**Phase 4: Optimize and test**
- Add throttling optimization
- Run unit + integration tests
- Manual testing on both layouts
- Performance benchmarking
- Fix any issues found

**Phase 5: Documentation and commit**
- Add JSDoc comments to VirtualScroller
- Update ChannelsView comments
- Commit with detailed message
- Estimated: ~10 minutes

**Total estimated changes:**
- New files: 2 (VirtualScroller.vue + .test.ts)
- Modified files: 1 (ChannelsView.vue, ~35 lines)
- New CSS: ~20 lines
- Net addition: ~300 lines (mostly new component + tests)

## Benefits

**User experience:**
- Route to channels page in < 300ms (from 1-2 seconds)
- Smooth scrolling through 408 items
- No perceptible lag when filtering/searching
- Lower memory usage = better performance on low-end devices

**Developer experience:**
- Reusable VirtualScroller for other lists (accounts, resources)
- Clean separation: scroll logic vs. item rendering
- Easy to test: component logic isolated
- Minimal changes to existing ChannelsView code

**Scalability:**
- Handles 1000+ channels with same performance
- Linear memory growth (O(visible) not O(total))
- Foundation for future list optimizations

## Risks and Mitigations

**Risk:** Virtual scrolling breaks table layout
- **Mitigation:** Use `display: block` on VirtualScroller, `display: table` on rows
- **Fallback:** If layout breaks, add wrapper `<div>` around `<tbody>` content

**Risk:** Scroll position jumps unexpectedly
- **Mitigation:** Reset scroll to top on filter/sort changes (expected behavior)
- **Alternative:** Preserve scroll if needed (add `key` to VirtualScroller)

**Risk:** Avatar loading causes layout shift
- **Mitigation:** Avatar component already has fixed dimensions (48×48px)
- **Verification:** Test during manual QA

**Risk:** Performance no better than expected
- **Mitigation:** Benchmark before/after; if < 50% improvement, investigate
- **Debugging:** Chrome DevTools Performance profile to find bottlenecks

**Risk:** Mobile scrolling feels janky
- **Mitigation:** Reduce buffer size on mobile (3 instead of 5)
- **Alternative:** Use touch-optimized scroll library if needed

## Future Enhancements

If virtual scrolling proves successful, could extend to:

1. **Accounts page:** Similar table with many accounts
2. **Resources page:** Large file lists
3. **Search results:** Thousands of messages
4. **Variable height support:** For content with dynamic sizing (more complex)
5. **Horizontal virtual scrolling:** For wide tables (if needed)

Not included in this design, would be separate projects.

## Approval

Design approved by user on 2026-06-14.

Ready for implementation planning.
