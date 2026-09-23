import { req } from './client'
import type { ID } from './types'

/** 导入预览：一个候选用例。 */
export interface ImportedCase {
  name: string
  code: string
  module: string
  step_count: number
  summary: string[]
  source_file: string
}

export interface ImportPreviewResult {
  detected: string
  cases: ImportedCase[]
}

export interface ImportCommitResult {
  created: Array<{ id: ID; code: string; name: string }>
  failed: Array<{ name: string; reason: string }>
}

/** multipart 上传：让 axios 自动设置 multipart 边界，不要手动写 Content-Type。 */
function form(file: File, format: string): FormData {
  const fd = new FormData()
  fd.append('file', file)
  if (format) fd.append('format', format)
  return fd
}

/** 转换并预览候选用例，不落库。 */
export function importPreview(projectId: ID, file: File, format: string): Promise<ImportPreviewResult> {
  return req<ImportPreviewResult>({
    method: 'POST',
    url: `/projects/${projectId}/import/preview`,
    data: form(file, format),
  })
}

/** 转换并落库。 */
export function importCommit(projectId: ID, file: File, format: string): Promise<ImportCommitResult> {
  return req<ImportCommitResult>({
    method: 'POST',
    url: `/projects/${projectId}/import/commit`,
    data: form(file, format),
  })
}
