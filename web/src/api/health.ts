import { req } from './client'
import type { HealthReport, ID } from './types'

/** 项目资产健康度体检（F12）。 */
export function getHealth(projectId: ID, days = 30): Promise<HealthReport> {
  return req<HealthReport>({ method: 'GET', url: `/projects/${projectId}/health`, params: { days } })
}
