import { req } from './client'
import type { ID, MockHit, MockInfo } from './types'

export function getMockInfo(projectId: ID): Promise<MockInfo> {
  return req<MockInfo>({ method: 'GET', url: `/projects/${projectId}/mock` })
}

export function setMockEnabled(projectId: ID, enabled: boolean): Promise<{ enabled: boolean }> {
  return req<{ enabled: boolean }>({
    method: 'PUT',
    url: `/projects/${projectId}/mock`,
    data: { enabled },
  })
}

export function listMockHits(projectId: ID, limit = 50): Promise<{ hits: MockHit[]; note: string }> {
  return req<{ hits: MockHit[]; note: string }>({
    method: 'GET',
    url: `/projects/${projectId}/mock/hits`,
    params: { limit },
  })
}
