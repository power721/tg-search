import { describe, expect, it } from 'vitest'
import { taskTypeLabel, taskTypeLabels } from './taskLabels'

describe('taskTypeLabel', () => {
  // Mirrors the backend TaskType constants in internal/model/model.go.
  const backendTypes = [
    'metadata_sync',
    'channel_analysis',
    'web_access_detection',
    'history_sync',
    'listener_recovery',
    'remote_search',
    'backup',
    'gap_recovery',
    'ai_media_metadata',
    'repair_media_title'
  ]

  it('localizes every backend task type to Chinese', () => {
    for (const type of backendTypes) {
      const label = taskTypeLabel(type)
      expect(label, `expected Chinese label for ${type}`).not.toBe(type)
      expect(label, `expected Chinese label for ${type}`).toMatch(/\p{Script=Han}/u)
    }
  })

  it('includes ai_media_metadata', () => {
    expect(taskTypeLabels.ai_media_metadata).toBe('AI 媒体元数据')
    expect(taskTypeLabel('ai_media_metadata')).toBe('AI 媒体元数据')
  })

  it('falls back to the raw type for unknown values', () => {
    expect(taskTypeLabel('unknown')).toBe('unknown')
  })
})
