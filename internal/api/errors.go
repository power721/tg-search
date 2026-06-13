package api

import (
	"net/http"
	"strings"

	channelpkg "tg-search/internal/channel"
	historypkg "tg-search/internal/history"
	"tg-search/internal/model"
)

var exactErrorMessages = map[string]string{
	"api key is required":                                 "缺少 API Key",
	"app_hash is required":                                "请输入 App Hash",
	"app_id must be greater than zero":                    "App ID 必须大于 0",
	"backup is unavailable":                               "备份服务不可用",
	"base_url is required":                                "请输入 Base URL",
	"before_date and before_id must be provided together": "before_date 和 before_id 必须同时提供",
	"before_id must be a positive integer":                "before_id 必须是正整数",
	"channel history sync disabled":                       "该频道未启用历史同步",
	"channel id must be positive":                         "频道 ID 必须是正整数",
	"channel web access checker is unavailable":           "频道网页访问检测服务不可用",
	"channel sync already in progress":                    "频道同步正在进行中",
	"channel_id must be a positive integer":               "频道 ID 必须是正整数",
	"channel_id must reference an existing channel":       "频道不存在",
	"channel_ids is required":                             "请选择频道",
	"channel_ids must contain positive integers":          "频道 ID 必须是正整数",
	"current password is invalid":                         "当前密码错误",
	"database storage quota exceeded":                     "数据库存储空间已达到上限",
	"enabled is required":                                 "请指定是否启用",
	"events are unavailable":                              "事件服务不可用",
	"invalid api key":                                     "API Key 无效",
	"invalid credentials":                                 "用户名或密码错误",
	"invalid id":                                          "ID 无效",
	"invalid phone number":                                "手机号格式无效，请包含国家码，例如 +86 13800138000",
	"invalid task status transition":                      "任务状态不允许执行该操作",
	"link_types is required":                              "请选择链接类型",
	"links are unavailable":                               "链接服务不可用",
	"login code hash is missing; call send-code first":    "验证码状态已失效，请重新发送验证码",
	"maintenance repository is unavailable":               "维护服务不可用",
	"max_messages must be a positive integer":             "最大消息数必须是正整数",
	"message_types is required":                           "请选择消息类型",
	"model is required":                                   "请输入模型",
	"not authenticated":                                   "请先登录",
	"password must be at least 8 characters":              "密码至少需要 8 位",
	"phone and code are required":                         "请输入手机号码和验证码",
	"phone and password are required":                     "请输入手机号码和密码",
	"phone is required":                                   "请输入手机号码",
	"qr login session not found":                          "扫码登录会话不存在或已过期",
	"qr login session path is required":                   "扫码登录会话路径不能为空",
	"query is required":                                   "请输入搜索关键词",
	"remote search execution is unavailable":              "远程搜索执行服务不可用",
	"remote search is not allowed for this channel":       "该频道不允许远程搜索",
	"remote search is unavailable":                        "远程搜索服务不可用",
	"remote search requires an unsynced channel":          "远程搜索只能用于未同步的频道",
	"resource not found":                                  "资源不存在",
	"resources are unavailable":                           "资源服务不可用",
	"search query is required":                            "请输入搜索关键词",
	"sql: no rows in result set":                          "记录不存在",
	"task is paused":                                      "任务已暂停",
	"tasks are unavailable":                               "任务服务不可用",
	"telegram client is unavailable":                      "Telegram 客户端不可用",
	"telegram password required":                          "需要 Telegram 两步验证密码",
	"username is required":                                "请输入用户名",
	"watch rule already exists for channel":               "该频道已存在监听规则",
	"account has no avatar to sync":                       "该账号没有头像",
	"avatar service is unavailable":                       "头像同步服务不可用",
	"id must be a positive integer":                       "ID 必须是正整数",
}

var fieldLabels = map[string]string{
	"account_id":    "账号 ID",
	"before_id":     "before_id",
	"channel_id":    "频道 ID",
	"date_from":     "date_from",
	"date_to":       "date_to",
	"excludes":      "排除关键词",
	"includes":      "包含关键词",
	"limit":         "limit",
	"link_types":    "链接类型",
	"max_messages":  "最大消息数",
	"message_types": "消息类型",
	"offset":        "offset",
}

var exactTaskMessages = map[string]string{
	"completed": "已完成",
}

func localizedErrorMessage(status int, msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return fallbackErrorMessage(status)
	}
	if translated, ok := exactErrorMessages[msg]; ok {
		return translated
	}
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "unexpected eof"),
		strings.Contains(lower, "invalid character"),
		strings.Contains(lower, "cannot unmarshal"),
		strings.Contains(lower, "json:"),
		strings.Contains(lower, "eof"):
		return "请求体 JSON 格式错误"
	case strings.HasSuffix(msg, " must be a positive integer"):
		return fieldLabel(strings.TrimSuffix(msg, " must be a positive integer")) + "必须是正整数"
	case strings.HasSuffix(msg, " must be a non-negative integer"):
		return fieldLabel(strings.TrimSuffix(msg, " must be a non-negative integer")) + "不能为负数"
	case strings.HasSuffix(msg, " is too large"):
		return fieldLabel(strings.TrimSuffix(msg, " is too large")) + "过大"
	case strings.HasSuffix(msg, " must be YYYY-MM-DD or RFC3339"):
		return fieldLabel(strings.TrimSuffix(msg, " must be YYYY-MM-DD or RFC3339")) + " 必须是 YYYY-MM-DD 或 RFC3339 格式"
	case strings.HasSuffix(msg, " must be an array of strings"):
		return fieldLabel(strings.TrimSuffix(msg, " must be an array of strings")) + "必须是字符串数组"
	case strings.HasPrefix(msg, "invalid sync profile"):
		return "同步档位无效"
	case strings.Contains(lower, "flood_wait") || strings.Contains(lower, "flood wait"):
		return "Telegram 请求触发限流，请稍后重试"
	case strings.Contains(lower, "auth_key_unregistered") ||
		strings.Contains(lower, "auth_key_invalid") ||
		strings.Contains(lower, "session_revoked"):
		return "Telegram 登录已失效，请重新登录"
	case strings.Contains(lower, "phone_code_invalid") || strings.Contains(lower, "code invalid"):
		return "验证码错误"
	case strings.Contains(lower, "phone_code_expired") || strings.Contains(lower, "code expired"):
		return "验证码已过期，请重新发送验证码"
	case strings.Contains(lower, "password_hash_invalid") || strings.Contains(lower, "password invalid"):
		return "Telegram 两步验证密码错误"
	case strings.Contains(lower, "not found") || strings.Contains(lower, "no rows"):
		return "记录不存在"
	case strings.Contains(lower, "unique constraint") || strings.Contains(lower, "duplicate"):
		return "数据已存在"
	case strings.Contains(lower, "quota"):
		return "存储空间已达到上限"
	case strings.Contains(lower, "context deadline exceeded"):
		return "请求超时"
	case strings.Contains(lower, "context canceled"):
		return "请求已取消"
	case strings.Contains(lower, "telegram web access returned http") || strings.Contains(lower, "telegram web"):
		return "Telegram 网页访问检测失败"
	default:
		return fallbackErrorMessage(status)
	}
}

func fieldLabel(field string) string {
	if label, ok := fieldLabels[field]; ok {
		return label
	}
	return field
}

func fallbackErrorMessage(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "认证失败"
	case status == http.StatusForbidden:
		return "没有权限执行该操作"
	case status == http.StatusNotFound:
		return "资源不存在"
	case status == http.StatusConflict:
		return "请求冲突"
	case status == http.StatusServiceUnavailable:
		return "服务暂不可用"
	case status >= http.StatusBadRequest && status < http.StatusInternalServerError:
		return "请求参数错误"
	default:
		return "服务器内部错误"
	}
}

func localizeDisplayError(msg string) string {
	if strings.TrimSpace(msg) == "" {
		return ""
	}
	return localizedErrorMessage(http.StatusInternalServerError, msg)
}

func localizeTaskMessage(msg string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(msg))
	if key == "" {
		return "", false
	}
	translated, ok := exactTaskMessages[key]
	return translated, ok
}

func localizeAccount(account model.Account) model.Account {
	account.LastError = localizeDisplayError(account.LastError)
	return account
}

func localizeAccounts(accounts []model.Account) []model.Account {
	out := make([]model.Account, len(accounts))
	for i, account := range accounts {
		out[i] = localizeAccount(account)
	}
	return out
}

func localizeChannel(channel model.Channel) model.Channel {
	channel.WebAccessError = localizeDisplayError(channel.WebAccessError)
	return channel
}

func localizeChannels(channels []model.Channel) []model.Channel {
	out := make([]model.Channel, len(channels))
	for i, channel := range channels {
		out[i] = localizeChannel(channel)
	}
	return out
}

func localizeTask(task model.Task) model.Task {
	task.ErrorMessage = localizeDisplayError(task.ErrorMessage)
	if translated, ok := localizeTaskMessage(task.Message); ok {
		task.Message = translated
	} else if task.Status == model.TaskStatusFailed ||
		task.Status == model.TaskStatusFloodWait ||
		task.Status == model.TaskStatusReconnecting {
		task.Message = localizeDisplayError(task.Message)
	}
	return task
}

func localizeTasks(tasks []model.Task) []model.Task {
	out := make([]model.Task, len(tasks))
	for i, task := range tasks {
		out[i] = localizeTask(task)
	}
	return out
}

func localizeWebAccessResults(items []channelpkg.WebAccessResult) []channelpkg.WebAccessResult {
	out := make([]channelpkg.WebAccessResult, len(items))
	for i, item := range items {
		item.WebAccessError = localizeDisplayError(item.WebAccessError)
		out[i] = item
	}
	return out
}

func localizeSyncManyResult(result historypkg.SyncManyResult) historypkg.SyncManyResult {
	if len(result.Failures) == 0 {
		return result
	}
	failures := make(map[int64]string, len(result.Failures))
	for id, msg := range result.Failures {
		failures[id] = localizeDisplayError(msg)
	}
	result.Failures = failures
	return result
}
