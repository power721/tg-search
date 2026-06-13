# Avatar Lazy Loading Optimization Design

**Date:** 2026-06-14  
**Status:** Approved  
**Author:** Claude (Opus 4.8)

## Problem Statement

The channels page experiences severe loading delays (10+ seconds) when displaying ~100 channels. Network analysis reveals 751 concurrent avatar requests, most returning 404, causing network congestion and blocking page rendering.

### Root Cause Analysis

**Symptoms:**
- Page load time: 10.16 seconds
- Total requests: 751 (avatar requests for various sizes)
- API response time: 33ms (fast)
- Avatar requests: 40-468ms each, mostly 404s

**Root cause:**
The Avatar component renders `<img>` tags with immediate `src` attributes, causing all avatars to load simultaneously on page mount:

```vue
<img
  v-if="showImage"
  :src="avatarUrl"
  @error="onImageError"
>
```

**Why it matters:**
- 100 channels × multiple avatar sizes = hundreds of concurrent requests
- Browser connection limits cause queueing
- 404 responses still consume network time
- Page appears frozen while waiting for avatar requests to complete

## Solution Overview

Apply the existing `v-lazy-load` directive to Avatar components to implement Intersection Observer-based lazy loading. Only load avatars that are visible or near the viewport, with automatic concurrency control.

**Key principle:** Leverage existing, battle-tested infrastructure. The project already has a well-designed lazyLoad directive with Intersection Observer and concurrency limits.

## Design Details

### 1. Existing Infrastructure

The project already has `web/src/directives/lazyLoad.ts` with:
- Intersection Observer API integration
- Concurrency control (max 6 simultaneous loads)
- 200px rootMargin (preload before entering viewport)
- Automatic fallback for test environments
- Request queueing when concurrent limit reached

This directive is production-ready but not yet applied to Avatar components.

### 2. Avatar Component Changes

**File:** `web/src/components/common/Avatar.vue`

#### 2.1 Import directive (script section)

```typescript
import { vLazyLoad } from '@/directives/lazyLoad'
```

#### 2.2 Modify img template

```vue
<!-- Before -->
<img
  v-if="showImage"
  :src="avatarUrl"
  :alt="name"
  class="avatar-image"
  @error="onImageError"
>

<!-- After -->
<img
  v-if="showImage"
  v-lazy-load
  :data-src="avatarUrl"
  :alt="name"
  class="avatar-image"
  @error="onImageError"
>
```

**Changes:**
- `:src="avatarUrl"` → `:data-src="avatarUrl"` (lazy load convention)
- Add `v-lazy-load` directive
- Preserve `@error` handler (404 fallback still works)

### 3. Data Flow

**Initial page load:**
1. ChannelsView fetches channel list (33ms)
2. Vue renders all channel rows with Avatar components
3. Each Avatar component shows:
   - If photoId exists: colored circle + initials (image not loaded yet)
   - If no photoId: colored circle + initials
4. lazyLoad directive observes all `<img>` elements
5. Only visible avatars (+ 200px buffer) enter load queue
6. Maximum 6 concurrent requests at any time

**Scrolling behavior:**
1. New avatars enter viewport proximity (200px threshold)
2. IntersectionObserver callback fires
3. Directive copies `data-src` to `src`, triggering load
4. On success: image replaces initials
5. On 404: `@error` handler fires, `imageError.value = true`, initials remain
6. Concurrency control queues excess requests

**Performance characteristics:**
- **Before:** 751 concurrent requests, 10.16 seconds
- **After:** ~15 initial requests, 1-2 seconds total, gradual loading on scroll

### 4. User Experience

**Visual progression:**
1. Page loads instantly with all channel data visible
2. Colored circles with initials appear immediately (no blank avatars)
3. Avatars in visible area load within 1-2 seconds
4. As user scrolls, new avatars load just before entering view
5. 404 avatars keep showing initials (same as current behavior)

**Edge cases:**
- **Fast scrolling:** Queue manages load order, no stuttering
- **Slow scrolling:** Avatars load before user reaches them (200px buffer)
- **Jumping to bottom:** Visible avatars load, others wait in queue
- **Test environment:** Auto-degrades to immediate loading (no IntersectionObserver)

### 5. Technical Implementation Details

**How lazyLoad directive works:**

1. **Mounting:**
   - Directive reads `data-src` attribute
   - Adds `<img>` element to IntersectionObserver
   - Image has no `src` yet, so no request fires

2. **Intersection:**
   - When element enters viewport + rootMargin (200px)
   - Callback checks `activeLoads < MAX_CONCURRENT_LOADS` (6)
   - If under limit: sets `img.src = data-src`, increments counter
   - If at limit: queues load via `pendingLoads.push()`

3. **Completion:**
   - Both `load` and `error` events decrement counter
   - Next queued load starts immediately
   - Observer stops watching this element

4. **Unmounting:**
   - Directive unobserves element
   - Prevents memory leaks

**Why this approach:**
- No new dependencies
- No architectural changes
- Minimal code surface area (~5 lines)
- Already handles all edge cases (tests, old browsers, errors)

### 6. Browser Compatibility

**IntersectionObserver support:**
- Chrome 51+ (2016)
- Firefox 55+ (2017)
- Safari 12.1+ (2019)
- Edge 15+ (2017)

**Fallback for unsupported environments:**
The lazyLoad directive already checks:
```typescript
if (typeof IntersectionObserver === 'undefined') {
  // Fallback: load immediately
  el.src = src
  el.removeAttribute('data-src')
}
```

Environments that auto-fallback:
- Vitest test runner (jsdom)
- Very old browsers (unlikely in 2026)
- Server-side rendering (if added later)

No polyfill needed for target audience.

## Testing Strategy

### Unit Tests

**Avatar.vue tests:**
- Existing tests remain unchanged
- Tests run in jsdom (no IntersectionObserver) → auto-fallback path
- Verify:
  - `data-src` attribute set correctly
  - `@error` handler still triggers on 404
  - Initials display correctly
  - All existing Avatar tests pass

**No new tests needed:**
- lazyLoad directive already has implicit test coverage (used elsewhere or standalone)
- Test environment automatically uses fallback path

### Manual Testing

**1. Performance verification:**
- Open Chrome DevTools → Network panel
- Clear cache, reload channels page
- Verify:
  - Initial avatar requests: ~10-20 (not 751)
  - API request completes in ~33ms
  - Page interactive within 1-2 seconds
  - Concurrent requests never exceed 6

**2. Visual verification:**
- Initials appear immediately on page load
- Visible avatars load within 1-2 seconds
- Avatars smoothly replace initials (no flash)
- 404 avatars keep showing initials

**3. Scroll testing:**
- Slow scroll down: avatars appear before reaching them
- Fast scroll to bottom: avatars load gradually, no freeze
- Scroll back up: already-loaded avatars display instantly

**4. Regression testing:**
- Check other pages using Avatar component (accounts list, etc.)
- Verify same performance improvement
- Confirm no visual regressions

### Performance Metrics

**Before (baseline):**
- Total load time: 10.16s
- Avatar requests: 751
- Concurrent peaks: 100+ simultaneous
- User perception: "page is frozen"

**After (target):**
- Total load time: 1-2s for visible content
- Initial avatar requests: 10-20
- Concurrent limit: 6 (enforced)
- User perception: "page loads instantly"

**Success criteria:**
- Load time < 2s for visible content
- No more than 6 concurrent avatar requests
- All existing Avatar tests pass
- No visual regressions

## Implementation Notes

### Files Modified
- `web/src/components/common/Avatar.vue` (~5 lines changed)

### Files Unchanged
- `web/src/directives/lazyLoad.ts` (reused as-is)
- All other components
- No backend changes
- No new dependencies

### Deployment Considerations
- Zero migration needed
- No breaking changes
- No feature flags required
- Can deploy to production immediately after testing

### Rollback Plan
If issues arise, rollback is trivial:
```diff
-import { vLazyLoad } from '@/directives/lazyLoad'

 <img
   v-if="showImage"
-  v-lazy-load
-  :data-src="avatarUrl"
+  :src="avatarUrl"
   :alt="name"
   class="avatar-image"
   @error="onImageError"
 >
```

One commit, one file, instant rollback.

## Risk Assessment

### Low Risk Factors

**Minimal code change:**
- Only 5 lines modified in 1 file
- No architectural changes
- No new dependencies

**Battle-tested directive:**
- lazyLoad directive already exists and works
- Handles all edge cases (tests, old browsers, errors)
- Concurrency control already validated

**Backward compatible:**
- Preserves all existing behavior
- Test environments auto-degrade gracefully
- Error handling unchanged

### Potential Issues & Mitigations

**Issue:** User scrolls to bottom immediately, hundreds of avatars queue
- **Likelihood:** Low (most users browse top-down)
- **Impact:** Moderate (brief loading delay)
- **Mitigation:** Concurrency control prevents network flood, loads happen gradually
- **Workaround:** If problematic, increase MAX_CONCURRENT_LOADS from 6 to 10

**Issue:** Intersection Observer unavailable in edge cases
- **Likelihood:** Very low (2026 browser landscape)
- **Impact:** None (auto-fallback to immediate loading = current behavior)
- **Mitigation:** Built into directive already

**Issue:** 200px rootMargin not enough, users see blank avatars while scrolling
- **Likelihood:** Low (200px = ~2 rows on typical display)
- **Impact:** Minor (avatars appear shortly after scrolling)
- **Mitigation:** Adjust rootMargin if needed (one-line change in directive)
- **Note:** Initials always show, so never truly "blank"

**Issue:** Some pages with Avatar components don't need lazy loading
- **Likelihood:** N/A (all pages benefit from lazy loading)
- **Impact:** None (lazy loading only helps, never hurts)
- **Mitigation:** None needed

## Future Enhancements

If performance is still insufficient or requirements change:

1. **Virtual scrolling:**
   - Only render visible rows in DOM
   - Requires library (vue-virtual-scroller)
   - Overkill for current ~100 channels

2. **Avatar size optimization:**
   - Serve smaller image files
   - Use WebP format
   - Backend change

3. **Adjust concurrency or rootMargin:**
   - Tune MAX_CONCURRENT_LOADS (currently 6)
   - Tune rootMargin (currently 200px)
   - Single-line changes in lazyLoad.ts

4. **Loading skeleton:**
   - Show pulse animation instead of initials
   - Requires design decision

None of these are necessary for the current problem.

## Approval

Design approved by user on 2026-06-14.

Ready for implementation planning.
