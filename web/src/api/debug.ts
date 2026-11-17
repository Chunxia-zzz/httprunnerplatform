import { req } from './client'
import type { DebugRequestReq, DebugStepResult, ID } from './types'

/**
 * 调试台：发一个临时请求（不落库，不进执行历史）。
 *
 * 语义与单步调试一致：平台合成一个单步骤用例，编译后在独立临时工作区
 * 跑一次引擎，返回后即弃。响应结构复用 DebugStepResult。
 */
export function request(projectId: ID, data: DebugRequestReq): Promise<DebugStepResult> {
  return req<DebugStepResult>({
    method: 'POST',
    url: `/projects/${projectId}/debug/request`,
    data,
  })
}
