import { req } from './client'
import type { Environment, EnvironmentReq, ID, PageData } from './types'

/**
 * 项目下的环境列表。
 *
 * 该端点返回的是分页结构（`PageData`），虽然环境数量天然很少，
 * 但服务端把它包在了 OKPage 里，前端必须按分页解包。
 */
export function listEnvironments(projectId: ID): Promise<PageData<Environment>> {
  return req<PageData<Environment>>({
    method: 'GET',
    url: `/projects/${projectId}/environments`,
  })
}

export function getEnvironment(id: ID): Promise<Environment> {
  return req<Environment>({ method: 'GET', url: `/environments/${id}` })
}

export function createEnvironment(projectId: ID, data: EnvironmentReq): Promise<Environment> {
  return req<Environment>({ method: 'POST', url: `/projects/${projectId}/environments`, data })
}

/**
 * 全量更新环境。
 *
 * 非管理员读到的是 `***` 掩码值；把它原样回写时后端会忽略该键并保留原值
 * （见 service.sanitizeEnvMap），所以"只改备注"的保存不会把 token 冲掉。
 */
export function updateEnvironment(id: ID, data: EnvironmentReq): Promise<Environment> {
  return req<Environment>({ method: 'PUT', url: `/environments/${id}`, data })
}

export function deleteEnvironment(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/environments/${id}` })
}

/** 掩码占位符。UI 上用它判断"这个值我没权限看到"。 */
export const MASKED_VALUE = '***'
