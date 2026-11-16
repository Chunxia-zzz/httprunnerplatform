import { req } from './client'
import type { APIDef, APIGroup, APIReq, ID } from './types'

/** 项目下的接口列表；module 非空时按服务过滤。 */
export function listApis(projectId: ID, module = ''): Promise<{ items: APIDef[] }> {
  return req<{ items: APIDef[] }>({
    method: 'GET',
    url: `/projects/${projectId}/apis`,
    params: module ? { module } : undefined,
  })
}

/** 按服务聚合的树（左树用）。该端点不分页，直接返回数组。 */
export function apiTree(projectId: ID): Promise<APIGroup[]> {
  return req<APIGroup[]>({ method: 'GET', url: `/projects/${projectId}/apis/tree` })
}

export function getApi(id: ID): Promise<APIDef> {
  return req<APIDef>({ method: 'GET', url: `/apis/${id}` })
}

export function createApi(projectId: ID, data: APIReq): Promise<APIDef> {
  return req<APIDef>({ method: 'POST', url: `/projects/${projectId}/apis`, data })
}

/** 全量覆盖。引用它的用例无需改动，下次编译自动生效。 */
export function updateApi(id: ID, data: APIReq): Promise<APIDef> {
  return req<APIDef>({ method: 'PUT', url: `/apis/${id}`, data })
}

/** 删除；被引用时后端返回 40003（CodeInUse）。 */
export function deleteApi(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/apis/${id}` })
}
