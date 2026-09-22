/**
 * HTTP 客户端：统一响应体的解包与错误归一化。
 *
 * 三件事在这里一次性解决，避免散落在每个页面里：
 *   1. `code != 0` 一律抛 ApiError（业务失败也是 HTTP 200，只看状态码会漏掉）；
 *   2. HTTP 4xx/5xx 的 JSON body 也归一化成同一个 ApiError 类型，
 *      页面只需要 catch 一种错误；
 *   3. 401 通过 window 事件广播出去，由 auth store 负责跳登录页 ——
 *      这样 http 层不需要 import store（避免循环依赖）。
 *
 * 请求走 Vite dev server 的 `/api` 代理（见 vite.config.ts），
 * 因此这里用相对路径，Cookie 同源自动携带。
 */
import axios, { type AxiosError, type AxiosRequestConfig } from 'axios'

import type { ApiBody } from './types'

/** 业务错误码（与 internal/pkg/response 常量对齐）。 */
export const Code = {
  OK: 0,
  BadParam: 40000,
  Conflict: 40001,
  NotFound: 40002,
  InUse: 40003,
  InvalidIdent: 40004,
  Unauthorized: 40100,
  Forbidden: 40300,
  Internal: 50000,
  EngineDown: 50001,
  WorkspaceFail: 50002,
  CompileFail: 50003,
  ValidateFail: 50004,
  ExecutorFail: 50005,
  /** 报告不存在（F8：断言失败时引擎不产出 report.html） */
  ReportMissing: 50006,
} as const

/** 未授权广播事件名。auth store 监听它做跳转。 */
export const UNAUTHORIZED_EVENT = 'hrp:unauthorized'

/** 归一化后的接口错误。所有失败路径都归到这一个类型。 */
export class ApiError extends Error {
  readonly code: number
  readonly httpStatus: number
  /** 业务数据。校验失败时是 ValidateOutcome（见 CaseService.Validate） */
  readonly data: unknown

  constructor(code: number, message: string, httpStatus = 200, data: unknown = null) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.httpStatus = httpStatus
    this.data = data
  }

  get isUnauthorized(): boolean {
    return this.code === Code.Unauthorized || this.httpStatus === 401
  }

  get isNotFound(): boolean {
    return this.code === Code.NotFound || this.httpStatus === 404
  }

  /** 报告不存在属于「引擎的既定行为」而非异常，UI 要走降级展示。 */
  get isReportMissing(): boolean {
    return this.code === Code.ReportMissing
  }

  /** 校验未通过：HTTP 200 + code=50004，data 是问题列表。 */
  get isValidateFail(): boolean {
    return this.code === Code.ValidateFail
  }
}

const http = axios.create({
  baseURL: '/api/v1',
  timeout: 60_000,
  // 会话是 HttpOnly Cookie；同源代理下浏览器会带，这里显式声明更稳。
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

http.interceptors.response.use(
  (resp) => resp,
  (error: AxiosError) => {
    const status = error.response?.status ?? 0
    const body = error.response?.data

    // 后端的错误响应始终是统一响应体，能拿到 code/message 就照用。
    if (body && typeof body === 'object' && 'code' in body) {
      const b = body as ApiBody
      const apiErr = new ApiError(b.code, b.message || '请求失败', status, b.data)
      if (apiErr.isUnauthorized) {
        window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
      }
      return Promise.reject(apiErr)
    }

    if (status === 401) {
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
      return Promise.reject(new ApiError(Code.Unauthorized, '登录已过期，请重新登录', 401))
    }
    if (error.code === 'ECONNABORTED') {
      return Promise.reject(new ApiError(Code.Internal, '请求超时（后端可能正在处理长任务）', 0))
    }
    if (!error.response) {
      return Promise.reject(
        new ApiError(Code.Internal, '无法连接后端服务，请确认服务已启动（默认 127.0.0.1:8080）', 0),
      )
    }
    return Promise.reject(
      new ApiError(Code.Internal, `HTTP ${status}：${error.message}`, status),
    )
  },
)

/** 发请求并解包 `data`。`code != 0` 抛 ApiError。 */
export async function req<T>(config: AxiosRequestConfig): Promise<T> {
  const resp = await http.request<ApiBody<T>>(config)
  const body = resp.data

  // GET /runs/{id}/report 之类的裸响应不走这里，见 raw()。
  if (!body || typeof body !== 'object' || !('code' in body)) {
    throw new ApiError(Code.Internal, '响应格式不符合契约（缺少 code 字段）', resp.status, body)
  }
  if (body.code !== Code.OK) {
    const err = new ApiError(body.code, body.message || '请求失败', resp.status, body.data)
    if (err.isUnauthorized) {
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT))
    }
    throw err
  }
  return body.data
}

export { http }
