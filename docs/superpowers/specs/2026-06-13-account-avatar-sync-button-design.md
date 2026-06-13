# Account Avatar Sync Button Design

## Problem
账号页面已显示头像，后端已实现 `POST /api/accounts/:id/sync-avatar` API，但前端缺少触发按钮。

## Solution
在账号页面添加"同步头像"按钮，允许用户手动触发账号头像同步。

## Architecture

### Backend (Already Implemented)
- API: `POST /api/accounts/:id/sync-avatar`
- Response: `202 Accepted` with `{"status": "queued", "job_id": "..."}`
- Behavior: Enqueues avatar download job, returns immediately

### Frontend Changes

#### 1. Store Method (`web/src/stores/telegram.ts`)
Add `syncAccountAvatar(id: number)` method:
- Call `POST /api/accounts/${id}/sync-avatar`
- No need to reload accounts (async download)
- Pattern: same as `syncAccountChannels`

#### 2. UI Button (`web/src/views/AccountsView.vue`)
Add "同步头像" button next to "同步频道":
- Always visible for all accounts
- `disabled` when `account.status !== 'ONLINE'`
- `loading` when `syncingAvatarAccountIds.has(account.id)`
- Success message: `${account.phone} 头像同步已提交`
- Error message: `${account.phone} 头像同步失败`

#### 3. State Management
Add `syncingAvatarAccountIds` ref:
- Type: `ref(new Set<number>())`
- Add ID before API call
- Remove ID in finally block
- Pattern: same as `syncingAccountIds` for channel sync

## Component Layout

### Desktop Table (action-buttons div)
```
[登录/登出] [同步频道] [同步头像] [删除]
```

### Mobile Cards (mobile-card-actions div)
```
[登录/登出] [同步频道] [同步头像] [删除]
```

## Button State Logic

| Account Status | Button State | Tooltip/Reason |
|----------------|--------------|----------------|
| ONLINE | Enabled | - |
| LOGIN_REQUIRED | Disabled | 需要登录 |
| RECONNECTING | Disabled | 连接中 |
| Others | Disabled | 账号不在线 |

## Implementation Notes

1. **No photo_id check**: Button always visible, backend handles validation
2. **Async nature**: API returns immediately, download happens in background
3. **No reload needed**: Avatar appears when download completes (via existing avatar system)
4. **Error handling**: Show error message if API call fails, but queued jobs fail silently
5. **Position**: Place after "同步频道", before "删除"

## Testing Checklist

- [ ] Button visible for all accounts (both table and mobile views)
- [ ] Disabled when status !== 'ONLINE'
- [ ] Loading spinner during API call
- [ ] Success message shows account phone
- [ ] Error message shows account phone
- [ ] Concurrent syncs tracked independently
- [ ] Button re-enables after operation completes
