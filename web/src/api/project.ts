import { req } from './client'
import type { ID, PageData, PageQuery, Project, ProjectReq } from './types'

export interface ProjectListQuery extends PageQuery {
  keyword?: string
}

export function listProjects(q: ProjectListQuery = {}): Promise<PageData<Project>> {
  return req<PageData<Project>>({ method: 'GET', url: '/projects', params: q })
}

export function getProject(id: ID): Promise<Project> {
  return req<Project>({ method: 'GET', url: `/projects/${id}` })
}

export function createProject(data: ProjectReq): Promise<Project> {
  return req<Project>({ method: 'POST', url: '/projects', data })
}

/** PUT 为全量覆盖。`code` 不可修改，改了后端会返回 40000。 */
export function updateProject(id: ID, data: ProjectReq): Promise<Project> {
  return req<Project>({ method: 'PUT', url: `/projects/${id}`, data })
}

/** 软删除。项目下有执行中的任务时返回 40003。 */
export function deleteProject(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/projects/${id}` })
}
