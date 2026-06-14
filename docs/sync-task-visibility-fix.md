# 账号同步频道列表任务显示问题修复

## 问题描述

用户反馈：账号同步频道列表时，前端显示"同步完成"，但是 API 返回 `{"job_id":"2","status":"queued"}`，并且在任务列表中看不到这个同步任务。

## 根本原因

系统中存在两种任务管理机制：

### 1. Task 系统（数据库持久化）
- 用于长时间运行的任务（历史同步、远程搜索等）
- 任务记录保存在数据库的 `tasks` 表中
- 通过 `/api/tasks` 端点查询
- 可以暂停、恢复、重试

### 2. RetryQueue 系统（内存队列）
- 用于短时间、轻量级的后台任务
- 任务状态只存在于内存中（`scheduler.RetryQueue`）
- **没有专门的查询端点**
- 任务包括：
  - `metadata-sync` - 账号登录后的元数据同步
  - `channel-sync` - 单个频道同步
  - `account-channels-sync` - 批量频道同步
  - 头像下载任务

### 问题所在

当用户登录或同步账号时，`respondWithOnlineAccount` 函数会：

```go
if h.deps.SyncQueue != nil {
    job := h.deps.SyncQueue.Enqueue(jobCtx, "metadata-sync", func(ctx context.Context) error {
        // 同步逻辑
        return nil
    })
    metadataSync = gin.H{"status": "queued", "channel_count": 0, "job_id": job.ID}
    // 立即返回，任务在后台异步执行
    return
}
```

这导致：
1. **API 返回 `status: "queued"`** - 任务已入队，但还在执行中
2. **前端可能误判为"完成"** - 如果没有正确处理 `queued` 状态
3. **任务列表看不到** - `/api/tasks` 只显示数据库中的 Task，不包括 RetryQueue 任务

## 解决方案

### 1. 添加 RetryJob 查询端点

**后端改动：**

在 `internal/api/router.go` 中添加：
```go
adminOnly.GET("/jobs/:id", h.retryJob)
```

在 `internal/api/handlers.go` 中添加：
```go
func (h handlers) retryJob(c *gin.Context) {
    if h.deps.SyncQueue == nil {
        errorText(c, http.StatusServiceUnavailable, "sync queue is unavailable")
        return
    }
    id := c.Param("id")
    if id == "" {
        errorText(c, http.StatusBadRequest, "id is required")
        return
    }
    job, ok := h.deps.SyncQueue.Snapshot(id)
    if !ok {
        errorText(c, http.StatusNotFound, "job not found")
        return
    }
    c.JSON(http.StatusOK, job)
}
```

### 2. 更新前端类型定义

在 `web/src/api/types.ts` 中：

**添加 RetryJob 类型：**
```typescript
export interface RetryJob {
  id: string
  name: string
  status: string  // queued, running, succeeded, failed
  attempts: number
  error?: string
  created_at: string
  updated_at: string
}
```

**更新 TelegramLoginResponse：**
```typescript
metadata_sync?: {
  status: string
  channel_count: number
  job_id?: string  // 新增字段
  error?: string
}
```

### 3. 改进前端状态显示

在 `web/src/views/AccountsView.vue` 中更新 `metadataText` 计算属性：

```typescript
const metadataText = computed(() => {
  const sync = telegram.loginResult?.metadata_sync
  if (!sync) return ''
  if (sync.status === 'succeeded') 
    return `元数据同步成功：${sync.channel_count} 个频道`
  if (sync.status === 'failed') 
    return `元数据同步失败：${sync.error ?? '未知错误'}`
  if (sync.status === 'queued') {
    const jobId = sync.job_id ? ` (任务 #${sync.job_id})` : ''
    return `正在后台同步频道元数据${jobId}，请稍候...`
  }
  return `元数据同步状态：${sync.status}`
})
```

### 4. 添加测试

创建 `internal/api/retry_job_test.go` 测试新端点的各种场景：
- ✓ 任务存在时返回任务信息
- ✓ 任务不存在时返回 404
- ✓ ID 为空时返回 400
- ✓ SyncQueue 不可用时返回 503

## 使用方式

### 查询 RetryJob 状态

```bash
# 获取 job_id 为 "2" 的任务状态
curl -X GET http://localhost:8080/api/jobs/2 \
  -H "Cookie: tg_search_session=..."
```

响应示例：
```json
{
  "id": "2",
  "name": "metadata-sync",
  "status": "running",
  "attempts": 1,
  "created_at": "2026-06-14T03:22:15Z",
  "updated_at": "2026-06-14T03:22:15Z"
}
```

可能的状态：
- `queued` - 已入队，等待执行
- `running` - 正在执行
- `succeeded` - 执行成功
- `failed` - 执行失败

## 注意事项

1. **RetryJob 是短期任务** - 它们只存在于内存中，服务重启后会丢失
2. **无法暂停/恢复** - RetryJob 一旦开始就会执行到结束
3. **自动重试** - 失败的任务会根据 `retry.Policy` 自动重试
4. **不适合长时间任务** - 超过几分钟的任务应该使用 Task 系统

## 相关文件

**后端：**
- `internal/api/router.go` - 新增 `/jobs/:id` 路由
- `internal/api/handlers.go` - 新增 `retryJob` 处理函数
- `internal/api/retry_job_test.go` - 新增测试
- `internal/scheduler/retry_queue.go` - RetryQueue 实现

**前端：**
- `web/src/api/types.ts` - 更新类型定义
- `web/src/views/AccountsView.vue` - 改进状态显示

## 测试验证

```bash
# 后端测试
GOCACHE=/tmp/go-build-cache go test ./internal/api -run TestRetryJobEndpoint -v

# 前端测试
npm run web:test

# 类型检查
npm run web:typecheck
```

所有测试应该通过 ✓
