import { req } from './client'
import type { ID, ImportedAPI, OpenAPICommitResult, OpenAPIPreview } from './types'

/** 上传 OpenAPI/Swagger 文档做预览（不落库）。 */
export function previewOpenAPI(projectId: ID, file: File): Promise<OpenAPIPreview> {
  const form = new FormData()
  form.append('file', file)
  return req<OpenAPIPreview>({
    method: 'POST',
    url: `/projects/${projectId}/import/openapi/preview`,
    data: form,
  })
}

/** 提交勾选的接口；policy=skip|overwrite。 */
export function commitOpenAPI(
  projectId: ID,
  apis: ImportedAPI[],
  policy: 'skip' | 'overwrite',
): Promise<OpenAPICommitResult> {
  return req<OpenAPICommitResult>({
    method: 'POST',
    url: `/projects/${projectId}/import/openapi/commit`,
    data: { apis, policy },
  })
}
