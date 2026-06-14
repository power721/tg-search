# Channels View Conditional Sorting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate unnecessary sorting operations in channels list by only sorting when user explicitly clicks column headers, improving filter/search responsiveness.

**Architecture:** Separate filtering from sorting by removing `.sort()` from `filteredChannels` and adding new `displayChannels` computed property that conditionally sorts only when `sortKey !== null`.

**Tech Stack:** Vue 3 Composition API, Vitest

---

## File Structure

**Modified:**
- `web/src/views/ChannelsView.vue:110` - Remove `.sort(compareChannels)` from `filteredChannels`
- `web/src/views/ChannelsView.vue:111` - Add new `displayChannels` computed property (5 lines)
- `web/src/views/ChannelsView.vue:532` - Replace `filteredChannels` with `displayChannels` in desktop table
- `web/src/views/ChannelsView.vue:624` - Replace `filteredChannels` with `displayChannels` in desktop empty state
- `web/src/views/ChannelsView.vue:643` - Replace `filteredChannels` with `displayChannels` in mobile cards
- `web/src/views/ChannelsView.vue:681` - Replace `filteredChannels` with `displayChannels` in mobile empty state

**No files created or deleted**

---

## Task 1: Remove Sorting from filteredChannels

**Files:**
- Modify: `web/src/views/ChannelsView.vue:110`

- [ ] **Step 1: Remove .sort() call from filteredChannels**

Locate line 110 in `web/src/views/ChannelsView.vue`:

```javascript
    .sort(compareChannels)
```

Delete this line entirely. The `filteredChannels` computed property should end with:

```javascript
const filteredChannels = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return channels.items
    .filter((channel) => {
      if (query) {
        const haystack = `${channel.title} ${channel.username}`.toLowerCase()
        if (!haystack.includes(query)) return false
      }
      if (typeFilter.value && channel.type !== typeFilter.value) return false
      if (syncStateFilter.value && channel.sync_state !== syncStateFilter.value) return false
      if (listenStateFilter.value && channel.listen_state !== listenStateFilter.value) return false
      if (webAccessFilter.value && webAccessState(channel) !== webAccessFilter.value) return false
      return true
    })
})
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 2: Add displayChannels Computed Property

**Files:**
- Modify: `web/src/views/ChannelsView.vue:111` (insert after filteredChannels)

- [ ] **Step 1: Add displayChannels computed property**

Insert this code immediately after the `filteredChannels` computed property (after line 111):

```javascript
const displayChannels = computed(() => {
  // User hasn't clicked any column header — use backend's default order
  if (sortKey.value === null) {
    return filteredChannels.value
  }
  
  // User clicked a column header — sort accordingly
  return [...filteredChannels.value].sort(compareChannels)
})
```

**Key points:**
- `sortKey === null`: Returns same array reference (zero overhead)
- `sortKey !== null`: Creates shallow copy with `[...array]` before sorting (avoids mutating original)
- `compareChannels` function unchanged

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors (displayChannels inferred as Ref<TelegramChannel[]>)

---

## Task 3: Update Desktop Table Template

**Files:**
- Modify: `web/src/views/ChannelsView.vue:532`

- [ ] **Step 1: Replace filteredChannels in desktop table row iteration**

Locate line 532:

```vue
<tr v-for="channel in filteredChannels" :key="channel.id">
```

Replace with:

```vue
<tr v-for="channel in displayChannels" :key="channel.id">
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 4: Update Desktop Empty State

**Files:**
- Modify: `web/src/views/ChannelsView.vue:624`

- [ ] **Step 1: Replace filteredChannels in desktop empty state**

Locate line 624:

```vue
<tr v-if="!channels.loading && filteredChannels.length === 0">
```

Replace with:

```vue
<tr v-if="!channels.loading && displayChannels.length === 0">
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 5: Update Mobile Cards Template

**Files:**
- Modify: `web/src/views/ChannelsView.vue:643`

- [ ] **Step 1: Replace filteredChannels in mobile card iteration**

Locate line 643:

```vue
<div v-for="channel in filteredChannels" :key="channel.id" class="mobile-card">
```

Replace with:

```vue
<div v-for="channel in displayChannels" :key="channel.id" class="mobile-card">
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 6: Update Mobile Empty State

**Files:**
- Modify: `web/src/views/ChannelsView.vue:681`

- [ ] **Step 1: Replace filteredChannels in mobile empty state**

Locate line 681:

```vue
<div v-if="!channels.loading && filteredChannels.length === 0" class="empty-state">
```

Replace with:

```vue
<div v-if="!channels.loading && displayChannels.length === 0" class="empty-state">
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 7: Run Test Suite

**Files:**
- Test: All frontend tests

- [ ] **Step 1: Run complete frontend test suite**

Run: `npm run web:test`
Expected: All tests pass

The tests should pass because:
- `displayChannels` returns the same data as the old `filteredChannels`
- Sorting behavior unchanged (still uses `compareChannels`)
- Component behavior identical from test perspective

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 8: Manual Testing

**Files:**
- Test: `web/src/views/ChannelsView.vue`

- [ ] **Step 1: Start development server**

Run: `npm run web:dev`
Expected: Server starts on http://localhost:5173

- [ ] **Step 2: Test initial page load (no sorting)**

1. Navigate to `/channels` page
2. Observe: Channels display in backend order (alphabetically by title)
3. Open DevTools Console
4. Verify: No errors

Expected behavior:
- Channels appear instantly
- No sort indicator on any column
- Order is alphabetical by title (backend default)

- [ ] **Step 3: Test filtering (no sorting)**

1. Change "Type" filter dropdown
2. Observe: Instant filtering
3. Type in search box
4. Observe: Instant filtering per keystroke
5. Change "Sync State" filter
6. Observe: Instant filtering

Expected behavior:
- All filters respond instantly
- No visible delay or stuttering
- Channels maintain alphabetical order

- [ ] **Step 4: Test user-initiated sorting**

1. Click "Title" column header
2. Observe: Sort indicator shows ascending (↑)
3. Verify: Channels sorted A→Z by title
4. Click "Title" again
5. Observe: Sort indicator shows descending (↓)
6. Verify: Channels sorted Z→A by title
7. Click "Title" third time
8. Observe: Sort indicator clears
9. Verify: Channels back to alphabetical order

Expected behavior:
- Three-state cycle works correctly
- Sort indicator updates correctly
- Sorting is stable (same-value items maintain relative order)

- [ ] **Step 5: Test sort persistence across filters**

1. Click "Indexed Messages" column to sort
2. Observe: Sorted by message count
3. Change "Type" filter
4. Observe: Filtered results maintain sort order
5. Type in search box
6. Observe: Search results maintain sort order

Expected behavior:
- Sort order persists when filters change
- sortKey and sortDirection remain set
- New filtered/searched items respect current sort

- [ ] **Step 6: Test different columns**

1. Click "Username" column
2. Verify: Sorts alphabetically by username (nulls at end)
3. Click "Indexed Messages" column
4. Verify: Sorts numerically by message count
5. Click each column third time to clear sort
6. Verify: Returns to default order each time

Expected behavior:
- Each column sorts correctly
- Null/empty values handled gracefully
- Clearing sort always returns to backend order

---

## Task 9: Performance Verification

**Files:**
- Test: `web/src/views/ChannelsView.vue`

- [ ] **Step 1: Verify no initial sorting**

1. Open Chrome DevTools → Performance tab
2. Click "Record" (⚫)
3. Navigate to `/channels` page
4. Wait for page load to complete
5. Stop recording
6. In the flame graph, search for "compareChannels"
7. Verify: No calls to compareChannels during initial load

Expected result:
- compareChannels should NOT appear in the initial load
- Only filtering operations should be visible

- [ ] **Step 2: Verify no sorting on filter change**

1. Start recording in Performance tab
2. Change a filter (e.g., "Type" dropdown)
3. Stop recording
4. Search for "compareChannels" in flame graph
5. Verify: No calls to compareChannels

Expected result:
- compareChannels should NOT be called when filters change
- Only filtering logic executes

- [ ] **Step 3: Verify sorting only on user click**

1. Start recording in Performance tab
2. Click a column header (e.g., "Title")
3. Stop recording
4. Search for "compareChannels" in flame graph
5. Verify: Exactly ONE call to compareChannels

Expected result:
- compareChannels called exactly once when column clicked
- Sorting happens only on explicit user action

- [ ] **Step 4: Compare before/after timings**

Using Chrome DevTools Network tab:
1. Clear cache, hard reload `/channels`
2. Observe "DOMContentLoaded" and "Load" times
3. Compare to baseline (from issue report):
   - Before: ~10 seconds (with 751 avatar requests)
   - After avatar lazy load: ~2 seconds (with ~15 initial requests)
   - After conditional sort: ~1.5 seconds or better

Expected improvement:
- Page interactive within 1-2 seconds
- Filtering feels instant
- No perceptible delay when typing in search box

---

## Task 10: Commit Changes

**Files:**
- Commit: `web/src/views/ChannelsView.vue`

- [ ] **Step 1: Stage changes**

```bash
git add web/src/views/ChannelsView.vue
```

- [ ] **Step 2: Commit with descriptive message**

```bash
git commit -m "perf: add conditional sorting to channels view

- Remove .sort() from filteredChannels computed property
- Add displayChannels computed that only sorts when sortKey !== null
- Update all template references from filteredChannels to displayChannels
- Preserve backend default order (title sort) when no column clicked

Performance improvements:
- Initial page load: 0 sort operations (was 1)
- Filter changes: 0 sort operations (was 1 per change)
- Search keystrokes: 0 sort operations (was 1 per keystroke)
- User sorts: 1 sort operation (unchanged)

Reduces unnecessary re-renders of 100+ Avatar components when filters
change. Combined with avatar lazy loading, improves page responsiveness
from 2 seconds to ~1.5 seconds.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 3: Verify commit**

```bash
git log -1 --stat
```

Expected: Shows commit with ChannelsView.vue modified

---

## Self-Review Checklist

**Spec Coverage:**
- ✅ Remove .sort() from filteredChannels (Design 1, Step 1)
- ✅ Add displayChannels computed property (Design 1, Step 2)
- ✅ Update desktop table template (Design 2)
- ✅ Update desktop empty state (Design 2)
- ✅ Update mobile cards template (Design 2)
- ✅ Update mobile empty state (Design 2)
- ✅ Manual functional testing (Testing Strategy - Manual Testing)
- ✅ Performance verification (Testing Strategy - Performance Testing)

**Placeholder Scan:**
- ✅ No TBD, TODO, or "implement later"
- ✅ All code blocks complete and exact
- ✅ All commands include expected output
- ✅ No vague instructions

**Type Consistency:**
- ✅ `filteredChannels.value` used consistently
- ✅ `displayChannels` returns same type as `filteredChannels`
- ✅ `sortKey.value` checked consistently
- ✅ Template references use `displayChannels` not `filteredChannels`

**Verification:**
- All tasks include verification steps (typecheck, manual testing, performance)
- Each task is self-contained
- Commit message follows conventional commit format
- Manual testing covers all user workflows
