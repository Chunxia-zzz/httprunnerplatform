import { req } from './client'
import type { ID, PerfResult } from './types'

export interface PerfQuery {
  run_id?: number
  days?: number
  baseline_runs?: number
  min_samples?: number
  threshold_pct?: number
  min_delta_ms?: number
  verdict?: string
}

/** 接口级性能回归分析（F11）。 */
export function getPerf(projectId: ID, q: PerfQuery = {}): Promise<PerfResult> {
  return req<PerfResult>({ method: 'GET', url: `/projects/${projectId}/perf`, params: q })
}
