import { req } from './client'
import type { EngineStatus } from './types'

/**
 * 引擎可用性探测。
 *
 * ⚠️ 引擎不可用时后端返回 **HTTP 200 + code=50001**：
 * 这是"基础设施状态查询"，不是一个失败的请求。因此这里不能靠
 * try/catch 判断，必须读返回体里的 `available`。
 */
export function engineStatus(): Promise<EngineStatus> {
  return req<EngineStatus>({ method: 'GET', url: '/engine/status' })
}
