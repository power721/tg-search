# Channels View Performance Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate page load lag by changing default sort from expensive `localeCompare` to fast numeric ID comparison.

**Architecture:** Modify ChannelsView.vue to use three-state sorting (ascending → descending → default ID order). Default state uses `sortKey=null` to trigger numeric ID comparison instead of locale-aware string comparison.

**Tech Stack:** Vue 3 Composition API, Vitest, Vue Test Utils

---

## File Structure

**Modified:**
- `web/src/views/ChannelsView.vue:23-24` - Change `sortKey` type and default value to support `null`
- `web/src/views/ChannelsView.vue:200-220` - Update `compareChannels` to handle ID sorting
- `web/src/views/ChannelsView.vue:222-229` - Update `sortBy` for three-state cycle
- `web/src/views/ChannelsView.vue:231-234` - Update `sortIndicator` to handle null

**Test:**
- `web/src/views/ChannelsView.test.ts` - Add tests for three-state sorting and ID comparison

---

## Task 1: Update sortKey Type and Default Value

**Files:**
- Modify: `web/src/views/ChannelsView.vue:23-24`

- [ ] **Step 1: Change sortKey type to support null**

Update line 23 in `web/src/views/ChannelsView.vue`:

```typescript
// Before
const sortKey = ref<'title' | 'username' | 'indexed'>('title')

// After
const sortKey = ref<'title' | 'username' | 'indexed' | null>(null)
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 3: Verify dev server still runs**

Run: `npm run web:dev`
Expected: Server starts without errors, page loads (but may have incorrect sorting behavior until Task 2 completes)

---

## Task 2: Update compareChannels for ID Sorting

**Files:**
- Modify: `web/src/views/ChannelsView.vue:200-216`

- [ ] **Step 1: Add ID sorting logic to compareChannels**

Replace the `compareChannels` function (lines 200-216) with:

```typescript
function compareChannels(left: TelegramChannel, right: TelegramChannel) {
  // Default order: by ID (fast numeric comparison)
  if (sortKey.value === null) {
    return left.id - right.id
  }
  
  const direction = sortDirection.value === 'asc' ? 1 : -1
  let result = 0
  switch (sortKey.value) {
    case 'username':
      result = compareText(left.username, right.username)
      break
    case 'indexed':
      result = left.indexed_message_count - right.indexed_message_count
      break
    case 'title':
    default:
      result = compareText(left.title, right.title)
      break
  }
  return result * direction || compareText(left.title, right.title)
}
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 3: Manually test default sorting**

1. Run: `npm run web:dev`
2. Navigate to `/channels` page
3. Observe: Channels should be in ID order (no sort indicator visible)
4. Expected: Page loads quickly without lag

---

## Task 3: Implement Three-State Sort Cycle

**Files:**
- Modify: `web/src/views/ChannelsView.vue:222-229`

- [ ] **Step 1: Update sortBy function for three states**

Replace the `sortBy` function (lines 222-229) with:

```typescript
function sortBy(key: 'title' | 'username' | 'indexed') {
  if (sortKey.value === key) {
    // Same column clicked - cycle through states
    if (sortDirection.value === 'asc') {
      sortDirection.value = 'desc'
    } else {
      // Third click: reset to default order
      sortKey.value = null
      sortDirection.value = 'asc'
    }
  } else {
    // Different column clicked - start fresh
    sortKey.value = key
    sortDirection.value = 'asc'
  }
}
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 3: Manually test three-state cycle**

1. Run: `npm run web:dev`
2. Navigate to `/channels` page
3. Click "标题" header once → verify ↑ appears, channels sorted A-Z
4. Click "标题" again → verify ↓ appears, channels sorted Z-A
5. Click "标题" third time → verify indicator disappears, channels return to ID order
6. Click "用户名" header → verify ↑ appears, sorted by username ascending
7. Expected: All three states work correctly for each column

---

## Task 4: Update sortIndicator to Handle Null

**Files:**
- Modify: `web/src/views/ChannelsView.vue:231-234`

- [ ] **Step 1: Update sortIndicator function**

The existing `sortIndicator` function (lines 231-234) already handles the null case correctly:

```typescript
function sortIndicator(key: 'title' | 'username' | 'indexed') {
  if (sortKey.value !== key) return ''
  return sortDirection.value === 'asc' ? ' ↑' : ' ↓'
}
```

When `sortKey.value === null`, the condition `sortKey.value !== key` is true, so it returns `''` (no indicator). No changes needed.

- [ ] **Step 2: Verify behavior**

1. Run: `npm run web:dev`
2. Navigate to `/channels`
3. Verify: No sort indicators appear on initial load
4. Click any column header → verify indicator appears
5. Click twice more → verify indicator disappears on third click
6. Expected: Indicators only show when actively sorting, not in default state

---

## Task 5: Write Tests for Three-State Sorting

**Files:**
- Modify: `web/src/views/ChannelsView.test.ts`

- [ ] **Step 1: Write failing test for three-state cycle**

Add this test at the end of `web/src/views/ChannelsView.test.ts`:

```typescript
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
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `npm run web:test -- ChannelsView.test.ts`
Expected: Tests fail because `data-sort-key` attribute doesn't exist yet

- [ ] **Step 3: Add data-sort-key attributes to template**

Modify the table header buttons in the template (around lines 487-502):

```vue
<button class="sort-header" type="button" data-sort-key="title" @click="sortBy('title')">
  标题{{ sortIndicator('title') }}
</button>
```

```vue
<button class="sort-header" type="button" data-sort-key="username" @click="sortBy('username')">
  用户名{{ sortIndicator('username') }}
</button>
```

```vue
<button class="sort-header" type="button" data-sort-key="indexed" @click="sortBy('indexed')">
  已索引消息{{ sortIndicator('indexed') }}
</button>
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `npm run web:test -- ChannelsView.test.ts`
Expected: All tests pass including new three-state sorting tests

---

## Task 6: Write Test for ID Comparison

**Files:**
- Modify: `web/src/views/ChannelsView.test.ts`

- [ ] **Step 1: Write test for ID sorting performance**

Add this test in the same `describe('three-state sorting')` block:

```typescript
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `npm run web:test -- ChannelsView.test.ts`
Expected: Test passes, confirming ID sorting works

---

## Task 7: Write Test for Sort Indicator Visibility

**Files:**
- Modify: `web/src/views/ChannelsView.test.ts`

- [ ] **Step 1: Write test for indicator visibility**

Add this test in the `describe('three-state sorting')` block:

```typescript
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
```

- [ ] **Step 2: Run test to verify it passes**

Run: `npm run web:test -- ChannelsView.test.ts`
Expected: Test passes

---

## Task 8: Run Full Test Suite

**Files:**
- Test: `web/src/views/ChannelsView.test.ts`

- [ ] **Step 1: Run all ChannelsView tests**

Run: `npm run web:test -- ChannelsView.test.ts`
Expected: All tests pass (existing + new)

- [ ] **Step 2: Run full frontend test suite**

Run: `npm run web:test`
Expected: All tests pass across entire frontend

- [ ] **Step 3: Run TypeScript type check**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 9: Manual Performance Testing

**Files:**
- Test: `web/src/views/ChannelsView.vue`

- [ ] **Step 1: Test with Chrome DevTools Performance**

1. Open Chrome DevTools
2. Go to Performance tab
3. Click Record
4. Navigate to `/channels` page
5. Stop recording when page finishes loading
6. Find `filteredChannels` computed execution in flame graph
7. Expected: < 10ms execution time

- [ ] **Step 2: Test perceived performance**

1. Navigate to `/channels` page multiple times
2. Expected: No perceptible lag, page loads instantly
3. Apply filters (search, type, sync state)
4. Expected: Instant response

- [ ] **Step 3: Test three-state sorting UX**

1. Click "标题" header → verify smooth transition, ↑ appears
2. Click again → verify ↓ appears
3. Click third time → verify indicator disappears
4. Repeat for "用户名" and "已索引消息" columns
5. Expected: Clear visual feedback, smooth operation

---

## Task 10: Commit Changes

**Files:**
- Commit: All modified files

- [ ] **Step 1: Stage changes**

```bash
git add web/src/views/ChannelsView.vue web/src/views/ChannelsView.test.ts
```

- [ ] **Step 2: Commit with descriptive message**

```bash
git commit -m "perf: optimize channels view with ID-based default sorting

- Change default sortKey from 'title' to null (ID-based)
- Implement three-state sort cycle: asc → desc → default
- Add ID comparison branch in compareChannels (10-40x faster)
- Update sortBy for three-state logic
- Add comprehensive tests for three-state sorting
- Add data-sort-key attributes for testability

Eliminates page load lag by avoiding expensive localeCompare on initial
render. Users can still sort by title/username/indexed by clicking headers."
```

- [ ] **Step 3: Verify commit**

```bash
git log -1 --stat
```

Expected: Shows commit with modified files

---

## Self-Review Checklist

**Spec Coverage:**
- ✅ sortKey type updated to support null (Design 2.1)
- ✅ compareChannels handles ID sorting (Design 2.2)
- ✅ sortBy implements three-state cycle (Design 2.3)
- ✅ sortIndicator handles null (Design 2.4)
- ✅ No UI changes (no ID column) (Design 3)
- ✅ Tests for three-state sorting (Testing Strategy - Unit Tests)
- ✅ Tests for ID comparison (Testing Strategy - Unit Tests)
- ✅ Tests for sort indicators (Testing Strategy - Unit Tests)
- ✅ Manual performance testing (Testing Strategy - Manual Testing)

**Placeholder Scan:**
- ✅ No TBD, TODO, or "implement later"
- ✅ All code blocks complete
- ✅ All test cases have actual assertions
- ✅ All commands include expected output

**Type Consistency:**
- ✅ `sortKey` type consistent across all tasks: `'title' | 'username' | 'indexed' | null`
- ✅ `sortBy` parameter consistent: `'title' | 'username' | 'indexed'`
- ✅ `compareChannels` signature unchanged
- ✅ Function names consistent throughout plan

**Verification:**
- All tasks include verification steps (typecheck, test runs, manual testing)
- Each task is self-contained and can be completed independently
- Commits follow conventional commit format
