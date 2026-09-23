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

/**
 * 导出项目为 hrp 标准目录 zip（M4）。
 *
 * 响应是二进制流而不是统一 JSON 体，不能走 req()；
 * 失败时后端仍返回统一 JSON（application/json），据此区分成败。
 */
export async function exportProject(id: ID, envId?: ID): Promise<void> {
  const { http, ApiError } = await import('./client')
  let resp
  try {
    resp = await http.get<Blob>(`/projects/${id}/export`, {
      params: envId ? { env_id: envId } : {},
      responseType: 'blob',
    })
  } catch (e) {
    // blob 形式的错误响应解析成 message 再抛
    if (e && typeof e === 'object' && 'response' in (e as object)) {
      const err = e as { response?: { data?: Blob } }
      if (err.response?.data instanceof Blob) {
        const text = await err.response.data.text()
        try {
          const body = JSON.parse(text) as { message?: string }
          throw new ApiError(0, body.message || '导出失败', 0)
        } catch {
          /* 非 JSON 错误体，走下面的通用抛错 */
        }
      }
    }
    throw e
  }
  const blob = resp.data as Blob
  // Content-Disposition 里的文件名跨域/编码不可靠，本地从 header 取，取不到就用兜底名
  const cd = (resp.headers?.['content-disposition'] as string | undefined) || ''
  const m = /filename="?([^";]+)"?/i.exec(cd)
  const filename = m?.[1] || `project-${id}-export.zip`
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}
