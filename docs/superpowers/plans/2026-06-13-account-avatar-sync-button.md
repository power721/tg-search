# Account Avatar Sync Button Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add "同步头像" button to accounts page for manual avatar sync

**Architecture:** Add store method calling existing backend API, add button to AccountsView with independent loading state, disabled when account status is not ONLINE

**Tech Stack:** Vue 3, TypeScript, Pinia, Naive UI

---

## Task 1: Add Store Method

**Files:**
- Modify: `web/src/stores/telegram.ts`

- [ ] **Step 1: Add syncAccountAvatar method**

Add this method after the `syncAccountChannels` method (around line 133):

```typescript
async syncAccountAvatar(id: number) {
  return this.withLoading(async () => {
    await apiPost(`/api/accounts/${id}/sync-avatar`)
  })
}
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 3: Commit**

```bash
git add web/src/stores/telegram.ts
git commit -m "feat: add syncAccountAvatar store method"
```

---

## Task 2: Add Button State Management

**Files:**
- Modify: `web/src/views/AccountsView.vue`

- [ ] **Step 1: Add syncingAvatarAccountIds reactive state**

Add after `syncingAccountIds` declaration (around line 27):

```typescript
const syncingAvatarAccountIds = ref(new Set<number>())
```

- [ ] **Step 2: Add syncAccountAvatar handler function**

Add after `syncAccountChannels` function (around line 279):

```typescript
async function syncAccountAvatar(account: TelegramAccount) {
  const next = new Set(syncingAvatarAccountIds.value)
  next.add(account.id)
  syncingAvatarAccountIds.value = next
  try {
    await telegram.syncAccountAvatar(account.id)
    message.success(`${account.phone} 头像同步已提交`)
  } catch {
    message.error(`${account.phone} 头像同步失败`)
  } finally {
    const done = new Set(syncingAvatarAccountIds.value)
    done.delete(account.id)
    syncingAvatarAccountIds.value = done
  }
}
```

- [ ] **Step 3: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 4: Commit**

```bash
git add web/src/views/AccountsView.vue
git commit -m "feat: add avatar sync state management and handler"
```

---

## Task 3: Add Desktop Table Button

**Files:**
- Modify: `web/src/views/AccountsView.vue`

- [ ] **Step 1: Add button in action-buttons div**

Locate the desktop table action buttons (around line 350-366). Add the "同步头像" button after the "同步频道" button:

```vue
<n-button
  v-if="!needsLogin(account)"
  size="small"
  :disabled="account.status !== 'ONLINE'"
  :loading="syncingAvatarAccountIds.has(account.id)"
  @click="syncAccountAvatar(account)"
>
  同步头像
</n-button>
```

The full action-buttons section should look like:

```vue
<div class="action-buttons">
  <n-button v-if="needsLogin(account)" size="small" type="primary" @click="openTelegramLogin(account)">
    登录
  </n-button>
  <n-button v-else size="small" :loading="telegram.loading" @click="logoutAccount(account)">
    登出
  </n-button>
  <n-button
    v-if="!needsLogin(account)"
    size="small"
    :loading="syncingAccountIds.has(account.id)"
    @click="syncAccountChannels(account)"
  >
    同步频道
  </n-button>
  <n-button
    v-if="!needsLogin(account)"
    size="small"
    :disabled="account.status !== 'ONLINE'"
    :loading="syncingAvatarAccountIds.has(account.id)"
    @click="syncAccountAvatar(account)"
  >
    同步头像
  </n-button>
  <n-button
    size="small"
    type="error"
    ghost
    :loading="telegram.loading"
    @click="confirmDeleteAccount(account)"
  >
    删除
  </n-button>
</div>
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 3: Test in browser (dev mode)**

Run: `npm run web:dev`
Open: `http://127.0.0.1:5173/accounts`
Verify:
- "同步头像" button visible for non-LOGIN_REQUIRED accounts
- Button disabled when status is not ONLINE
- Button enabled when status is ONLINE

- [ ] **Step 4: Commit**

```bash
git add web/src/views/AccountsView.vue
git commit -m "feat: add avatar sync button to desktop table view"
```

---

## Task 4: Add Mobile Card Button

**Files:**
- Modify: `web/src/views/AccountsView.vue`

- [ ] **Step 1: Add button in mobile-card-actions div**

Locate the mobile card action buttons (around line 411-421). Add the "同步头像" button after the "同步频道" button:

```vue
<n-button
  v-if="!needsLogin(account)"
  size="small"
  :disabled="account.status !== 'ONLINE'"
  :loading="syncingAvatarAccountIds.has(account.id)"
  @click="syncAccountAvatar(account)"
>同步头像</n-button>
```

The full mobile-card-actions section should look like:

```vue
<div class="mobile-card-actions">
  <n-button v-if="needsLogin(account)" size="small" type="primary" @click="openTelegramLogin(account)">登录</n-button>
  <n-button v-else size="small" :loading="telegram.loading" @click="logoutAccount(account)">登出</n-button>
  <n-button
    v-if="!needsLogin(account)"
    size="small"
    :loading="syncingAccountIds.has(account.id)"
    @click="syncAccountChannels(account)"
  >同步频道</n-button>
  <n-button
    v-if="!needsLogin(account)"
    size="small"
    :disabled="account.status !== 'ONLINE'"
    :loading="syncingAvatarAccountIds.has(account.id)"
    @click="syncAccountAvatar(account)"
  >同步头像</n-button>
  <n-button size="small" type="error" ghost :loading="telegram.loading" @click="confirmDeleteAccount(account)">删除</n-button>
</div>
```

- [ ] **Step 2: Verify TypeScript compilation**

Run: `npm run web:typecheck`
Expected: No type errors

- [ ] **Step 3: Test in browser (mobile view)**

Run: `npm run web:dev`
Open: `http://127.0.0.1:5173/accounts`
Resize browser to < 760px width
Verify:
- "同步头像" button visible in mobile cards for non-LOGIN_REQUIRED accounts
- Button disabled when status is not ONLINE
- Button enabled when status is ONLINE

- [ ] **Step 4: Commit**

```bash
git add web/src/views/AccountsView.vue
git commit -m "feat: add avatar sync button to mobile card view"
```

---

## Task 5: Manual Testing

**Files:**
- None (manual testing only)

- [ ] **Step 1: Test ONLINE account button interaction**

Prerequisites: At least one account with status ONLINE

Run: `npm run web:dev`
Open: `http://127.0.0.1:5173/accounts`

Test:
1. Click "同步头像" on an ONLINE account
2. Verify button shows loading spinner
3. Verify success message: "{phone} 头像同步已提交"
4. Verify button re-enables after completion

- [ ] **Step 2: Test disabled state for non-ONLINE accounts**

Prerequisites: Account with status other than ONLINE (or temporarily logout one)

Test:
1. Verify "同步头像" button is visible but disabled (grayed out)
2. Verify clicking does nothing

- [ ] **Step 3: Test concurrent syncs**

Prerequisites: Multiple ONLINE accounts

Test:
1. Click "同步头像" on account A
2. Immediately click "同步头像" on account B
3. Verify each button shows independent loading state
4. Verify success messages appear for both

- [ ] **Step 4: Test error handling**

Prerequisites: Backend stopped or network disconnected

Test:
1. Stop backend or disconnect network
2. Click "同步头像"
3. Verify error message: "{phone} 头像同步失败"
4. Verify button re-enables

- [ ] **Step 5: Document test results**

Create a quick note of any issues found or all-clear confirmation

---

## Self-Review Checklist

**Spec Coverage:**
- ✓ Task 1: Store method `syncAccountAvatar` 
- ✓ Task 2: State management `syncingAvatarAccountIds` and handler
- ✓ Task 3: Desktop table button with disabled/loading states
- ✓ Task 4: Mobile card button with disabled/loading states
- ✓ Task 5: Manual testing covers all requirements from spec testing checklist

**Placeholders:** None - all code complete

**Type Consistency:**
- `syncAccountAvatar(account: TelegramAccount)` - consistent across handler and click events
- `syncingAvatarAccountIds` - consistent Set<number> usage
- `account.status !== 'ONLINE'` - consistent disabled condition
