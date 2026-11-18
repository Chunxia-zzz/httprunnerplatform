import { req } from './client'
import type { APIDoc, ID, IssuedShare, ShareLink } from './types'

/** 文档 JSON（登录态预览）。 */
export function getDoc(projectId: ID): Promise<APIDoc> {
  return req<APIDoc>({ method: 'GET', url: `/projects/${projectId}/docs` })
}

/** HTML 预览地址：iframe / 新窗口直接用（同源带会话 cookie）。 */
export function docHtmlUrl(projectId: ID): string {
  return `/api/v1/projects/${projectId}/docs/html`
}

/** 生成只读分享链接（明文 token 仅返回一次）。 */
export function createShare(
  projectId: ID,
  data: { name: string; expires_in_days: number },
): Promise<IssuedShare> {
  return req<IssuedShare>({ method: 'POST', url: `/projects/${projectId}/shares`, data })
}

export function listShares(projectId: ID): Promise<ShareLink[]> {
  return req<ShareLink[]>({ method: 'GET', url: `/projects/${projectId}/shares` })
}

export function revokeShare(projectId: ID, shareId: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/projects/${projectId}/shares/${shareId}` })
}
