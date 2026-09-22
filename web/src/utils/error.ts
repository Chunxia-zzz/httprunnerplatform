import { ElMessage } from 'element-plus'

import { ApiError } from '@/api'

/** 从任意异常里抽出可直接展示给用户的文案。 */
export function apiMessage(e: unknown, fallback = '操作失败'): string {
  if (e instanceof ApiError) return e.message || fallback
  if (e instanceof Error) return e.message || fallback
  if (typeof e === 'string' && e) return e
  return fallback
}

/** 统一的错误提示。页面里不要再各自拼 ElMessage.error。 */
export function notifyError(e: unknown, fallback = '操作失败'): void {
  ElMessage.error(apiMessage(e, fallback))
}

/** 成功提示。集中一处便于日后统一加日志或埋点。 */
export function notifyOk(message: string): void {
  ElMessage.success(message)
}
