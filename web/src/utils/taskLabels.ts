// Chinese localization for task types.
// Keep in sync with the backend TaskType constants in internal/model/model.go.
export const taskTypeLabels: Record<string, string> = {
  metadata_sync: '元数据同步',
  channel_analysis: '频道分析',
  web_access_detection: '网页访问检测',
  history_sync: '历史同步',
  listener_recovery: '监听恢复',
  remote_search: '远程搜索',
  backup: '备份',
  gap_recovery: '消息同步',
  ai_media_metadata: 'AI 媒体元数据',
  repair_media_title: '媒体标题修复'
}

export function taskTypeLabel(type: string) {
  return taskTypeLabels[type] ?? type
}
