import { req } from './client'
import type { FlakyCase, SlowCase, StatsQuery, TrendPoint } from './types'

/** 通过率趋势（按天）。返回 { points }。 */
export function statsTrend(q: StatsQuery): Promise<{ points: TrendPoint[] }> {
  return req<{ points: TrendPoint[] }>({ method: 'GET', url: '/stats/trend', params: q })
}

/** 不稳定/常败用例排行。返回 { items }。 */
export function statsFlaky(q: StatsQuery): Promise<{ items: FlakyCase[] }> {
  return req<{ items: FlakyCase[] }>({ method: 'GET', url: '/stats/flaky', params: q })
}

/** 慢用例排行。返回 { items }。 */
export function statsSlowest(q: StatsQuery): Promise<{ items: SlowCase[] }> {
  return req<{ items: SlowCase[] }>({ method: 'GET', url: '/stats/slowest', params: q })
}
