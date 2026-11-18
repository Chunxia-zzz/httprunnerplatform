import { req } from './client'
import type { ID, NotifyConfig, NotifyLog, NotifySendResult } from './types'

export interface NotifyReq {
  name: string
  channel: string
  webhook_url: string
  secret: string
  on_success: boolean
}

export function listNotify(projectId: ID): Promise<NotifyConfig[]> {
  return req<NotifyConfig[]>({ method: 'GET', url: `/projects/${projectId}/notifications` })
}

export function createNotify(projectId: ID, data: NotifyReq): Promise<NotifyConfig> {
  return req<NotifyConfig>({ method: 'POST', url: `/projects/${projectId}/notifications`, data })
}

export function updateNotify(id: ID, data: NotifyReq): Promise<NotifyConfig> {
  return req<NotifyConfig>({ method: 'PUT', url: `/notifications/${id}`, data })
}

export function deleteNotify(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/notifications/${id}` })
}

export function testNotify(id: ID): Promise<NotifySendResult> {
  return req<NotifySendResult>({ method: 'POST', url: `/notifications/${id}/test` })
}

export function listNotifyLogs(projectId: ID, limit = 50): Promise<NotifyLog[]> {
  return req<NotifyLog[]>({ method: 'GET', url: `/projects/${projectId}/notifications/logs`, params: { limit } })
}
