import { req } from './client'
import type { ChangeLog, ID, PageData } from './types'

export interface ChangeQuery {
  entity?: string
  action?: string
  page?: number
  page_size?: number
}

/** 项目变更历史（倒序分页）。 */
export function listChanges(projectId: ID, q: ChangeQuery = {}): Promise<PageData<ChangeLog>> {
  return req<PageData<ChangeLog>>({
    method: 'GET',
    url: `/projects/${projectId}/changes`,
    params: q,
  })
}
