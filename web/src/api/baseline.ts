import { req } from './client'
import type { BaselineResult } from './types'

/** 用例基线对比：拉取指定用例最近 N 次执行结果并排对比。 */
export function caseBaseline(q: { project_id: number; case_id: number; limit?: number }): Promise<BaselineResult> {
  return req<BaselineResult>({ method: 'GET', url: '/projects/{id}/baseline'.replace('{id}', String(q.project_id)), params: { case_id: q.case_id, limit: q.limit } })
}
