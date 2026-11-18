import { req } from './client'
import type { FailureClusterResult, ID } from './types'

export interface FailureQuery {
  run_id?: number
  days?: number
  limit?: number
}

/** 失败聚类（F13）：按根因特征把失败归成几簇。 */
export function getFailureClusters(projectId: ID, q: FailureQuery = {}): Promise<FailureClusterResult> {
  return req<FailureClusterResult>({
    method: 'GET',
    url: `/projects/${projectId}/failures/clusters`,
    params: q,
  })
}
