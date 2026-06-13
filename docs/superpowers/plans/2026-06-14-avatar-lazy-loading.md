# Avatar Lazy Loading Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply lazy loading to Avatar component to reduce initial page load from 10+ seconds to 1-2 seconds by loading only visible avatars.

**Architecture:** Add existing `v-lazy-load` directive to Avatar.vue's `<img>` tag. Change `:src` to `:data-src` to defer loading until element enters viewport. Preserve existing error handling and fallback behavior.

**Tech Stack:** Vue 3 Composition API, Intersection Observer (via existing lazyLoad directive), Vitest

---

## File Structure

**Modified:**
- `web/src/components/common/Avatar.vue:2` - Add import for vLazyLoad directive
- `web/src/components/common/Avatar.vue:60-64` - Modify `<img>` tag to use lazy loading

**Test:**
- `web/src/components/common/Avatar.test.ts` - Verify data-src attribute and existing behavior preserved

---

## Task 1: Add vLazyLoad Import to Avatar Component

**Files:**
- Modify: `web/src/components/common/Avatar.vue:2`

- [ ] **Step 1: Add import statement for lazyLoad directive**

Add this line after line 2 in `web/src/components/common/Avatar.vue`:

```typescript
import { vLazyLoad } from '@/directives/lazyLoad'
```

The imports section should look like:
```typescript
import { computed, ref } from 'vue'
import { vLazyLoad } from '@/directives/lazyLoad'
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 2: Apply Lazy Loading to Avatar Image

**Files:**
- Modify: `web/src/components/common/Avatar.vue:58-64`

- [ ] **Step 1: Modify img tag to use lazy loading**

Replace the `<img>` tag (lines 58-64) with:

```vue
    <img
      v-if="showImage"
      v-lazy-load
      :data-src="avatarUrl"
      :alt="name"
      class="avatar-image"
      @error="onImageError"
    >
```

Changes:
- Add `v-lazy-load` directive
- Change `:src="avatarUrl"` to `:data-src="avatarUrl"`
- Keep all other attributes unchanged

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 3: Run existing tests**

Run: `npm run web:test -- Avatar.test.ts`
Expected: All tests pass (lazyLoad directive auto-degrades in test environment)

---

## Task 3: Verify Avatar Tests Still Pass

**Files:**
- Test: `web/src/components/common/Avatar.test.ts`

- [ ] **Step 1: Run Avatar component tests**

Run: `npm run web:test -- Avatar.test.ts`
Expected: All existing tests pass

The tests should pass because:
- lazyLoad directive checks for IntersectionObserver
- In jsdom (test environment), IntersectionObserver is undefined
- Directive falls back to immediate loading: `el.src = el.dataset.src`
- Tests see same behavior as before

- [ ] **Step 2: Verify data-src attribute is set**

The existing tests don't need modification because:
- Fallback path sets `src` from `data-src` immediately
- Component behavior in tests is identical to before
- No new test cases needed

---

## Task 4: Manual Performance Testing

**Files:**
- Test: `web/src/components/common/Avatar.vue`

- [ ] **Step 1: Build and run development server**

Run: `npm run web:dev`
Expected: Server starts on http://localhost:5173

- [ ] **Step 2: Test initial page load performance**

1. Open Chrome DevTools → Network panel
2. Check "Disable cache"
3. Navigate to `/channels` page
4. Observe Network panel during load

Expected behavior:
- API request (`/api/channels`) completes in ~30-50ms
- Initial avatar requests: ~10-20 (visible avatars only)
- No more than 6 concurrent avatar requests at any time
- Page interactive within 1-2 seconds

Before optimization:
- 751 avatar requests
- 10+ seconds total load time

- [ ] **Step 3: Test scroll behavior**

1. Slowly scroll down the channels list
2. Observe Network panel

Expected behavior:
- New avatar requests appear as you scroll
- Avatars load ~200px before entering viewport
- Concurrent limit of 6 maintained
- No visual stuttering

- [ ] **Step 4: Test visual appearance**

1. Reload page with Network panel open
2. Observe avatar rendering

Expected behavior:
- Initials appear immediately (colored circles)
- Visible avatars load and replace initials within 1-2 seconds
- 404 avatars remain as initials (error handler still works)
- No blank spaces or missing avatars

- [ ] **Step 5: Test fast scrolling**

1. Quickly scroll to bottom of list
2. Observe avatar loading

Expected behavior:
- Avatars load gradually as they enter viewport
- No page freeze or stuttering
- Queue manages load order automatically

---

## Task 5: Run Full Test Suite

**Files:**
- Test: All frontend tests

- [ ] **Step 1: Run complete frontend test suite**

Run: `npm run web:test`
Expected: All tests pass (including Avatar tests)

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

---

## Task 6: Commit Changes

**Files:**
- Commit: `web/src/components/common/Avatar.vue`

- [ ] **Step 1: Stage changes**

```bash
git add web/src/components/common/Avatar.vue
```

- [ ] **Step 2: Commit with descriptive message**

```bash
git commit -m "perf: add lazy loading to Avatar component

- Import vLazyLoad directive from existing lazyLoad.ts
- Change :src to :data-src for Intersection Observer
- Add v-lazy-load directive to <img> tag
- Preserve error handling and fallback behavior

Reduces initial page load from 751 concurrent requests to ~15,
improving load time from 10+ seconds to 1-2 seconds. Only visible
avatars load immediately; others load on scroll with 6-request
concurrency limit.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 3: Verify commit**

```bash
git log -1 --stat
```

Expected: Shows commit with Avatar.vue modified

---

## Self-Review Checklist

**Spec Coverage:**
- ✅ Import vLazyLoad directive (Design 2.1)
- ✅ Modify img tag to use :data-src (Design 2.2)
- ✅ Add v-lazy-load directive (Design 2.2)
- ✅ Preserve @error handler (Design 2.2)
- ✅ Verify tests pass (Testing Strategy - Unit Tests)
- ✅ Manual performance testing (Testing Strategy - Manual Testing)

**Placeholder Scan:**
- ✅ No TBD, TODO, or "implement later"
- ✅ All code blocks complete and exact
- ✅ All commands include expected output
- ✅ No vague instructions

**Type Consistency:**
- ✅ Import statement matches directive name: `vLazyLoad`
- ✅ Attribute names consistent: `:data-src` (not `:dataSrc` or `:datasrc`)
- ✅ Directive name consistent: `v-lazy-load` (not `v-lazyLoad` or `vLazyLoad`)

**Verification:**
- All tasks include verification steps (typecheck, test runs, manual testing)
- Each task is self-contained and can be completed independently
- Commit message follows conventional commit format
