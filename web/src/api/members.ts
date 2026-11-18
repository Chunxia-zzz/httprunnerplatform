import { req } from './client'
import type { ID, ProjectMember } from './types'

/** 项目成员（含隐式所有者）。 */
export function listMembers(projectId: ID): Promise<ProjectMember[]> {
  return req<ProjectMember[]>({ method: 'GET', url: `/projects/${projectId}/members` })
}

/** 添加成员（仅管理员）。 */
export function addMember(
  projectId: ID,
  data: { username: string; role?: string },
): Promise<ProjectMember> {
  return req<ProjectMember>({ method: 'POST', url: `/projects/${projectId}/members`, data })
}

/** 移除成员（仅管理员）；项目所有者不可移除。 */
export function removeMember(projectId: ID, userId: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/projects/${projectId}/members/${userId}` })
}
