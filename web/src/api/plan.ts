import { req } from './client'
import type { ID, PageData, Plan, PlanReq, PlanSuite, Token, TokenReq, IssuedToken } from './types'

// ---------------------------------------------------------------------------
// 测试计划
// ---------------------------------------------------------------------------

/**
 * 计划列表。
 *
 * `enabled` 要显式传字符串（`true` / `false`），不传就是不过滤 ——
 * 后端用字符串解析而不是 bool，是为了把"没传"与"传了 false"分开。
 */
export function listPlans(
  projectId: ID,
  params?: { keyword?: string; enabled?: 'true' | 'false'; page?: number; page_size?: number },
): Promise<PageData<Plan>> {
  return req<PageData<Plan>>({
    method: 'GET',
    url: `/projects/${projectId}/plans`,
    params,
  })
}

/** 详情（含下次执行时间与 cron 中文说明）。 */
export function getPlan(id: ID): Promise<Plan> {
  return req<Plan>({ method: 'GET', url: `/plans/${id}` })
}

export function createPlan(projectId: ID, data: PlanReq): Promise<Plan> {
  return req<Plan>({ method: 'POST', url: `/projects/${projectId}/plans`, data })
}

export function updatePlan(id: ID, data: PlanReq): Promise<Plan> {
  return req<Plan>({ method: 'PUT', url: `/plans/${id}`, data })
}

export function deletePlan(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/plans/${id}` })
}

/** 单独开关，不动其它字段。 */
export function setPlanEnabled(id: ID, enabled: boolean): Promise<Plan> {
  return req<Plan>({ method: 'PUT', url: `/plans/${id}/enabled`, data: { enabled } })
}

export function listPlanSuites(id: ID): Promise<PlanSuite[]> {
  return req<PlanSuite[]>({ method: 'GET', url: `/plans/${id}/suites` })
}

export function setPlanSuites(id: ID, suiteIds: ID[]): Promise<PlanSuite[]> {
  return req<PlanSuite[]>({ method: 'PUT', url: `/plans/${id}/suites`, data: { suite_ids: suiteIds } })
}

// ---------------------------------------------------------------------------
// CI 令牌
// ---------------------------------------------------------------------------

/** 列表（只有 prefix，没有任何秘密）。 */
export function listTokens(projectId: ID): Promise<Token[]> {
  return req<Token[]>({ method: 'GET', url: `/projects/${projectId}/tokens` })
}

/**
 * 签发。
 *
 * ⭐ 返回的 `token` 明文**只在这一次响应里出现**。之后列表里只有 prefix，
 * 忘了就重签 —— 对 CI 而言重签的成本远低于"明文长期躺在库里"。
 * 因此 UI 必须在弹窗里让用户当场复制，并提供"我已经保存好了"的确认。
 */
export function issueToken(projectId: ID, data: TokenReq): Promise<IssuedToken> {
  return req<IssuedToken>({ method: 'POST', url: `/projects/${projectId}/tokens`, data })
}

/** 吊销。必须带 projectId：否则一个项目的管理员能吊销别人的令牌。 */
export function revokeToken(projectId: ID, id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/tokens/${id}`, params: { project_id: projectId } })
}
