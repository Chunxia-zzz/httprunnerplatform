import { req } from './client'
import type { DbSource, DbSourceReq, ID } from './types'

/** 项目数据库连接清单（password 掩码）。 */
export function listDbSources(projectId: ID): Promise<DbSource[]> {
  return req<DbSource[]>({ method: 'GET', url: `/projects/${projectId}/dbsources` })
}

/** 新建连接（仅管理员）。 */
export function createDbSource(projectId: ID, data: DbSourceReq): Promise<DbSource> {
  return req<DbSource>({ method: 'POST', url: `/projects/${projectId}/dbsources`, data })
}

/** 更新连接（仅管理员）；password 传 *** 表示保持原值。 */
export function updateDbSource(id: ID, data: DbSourceReq): Promise<DbSource> {
  return req<DbSource>({ method: 'PUT', url: `/dbsources/${id}`, data })
}

/** 删除连接（仅管理员）；被 db 步骤引用时被拒。 */
export function deleteDbSource(projectId: ID, id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/projects/${projectId}/dbsources/${id}` })
}
