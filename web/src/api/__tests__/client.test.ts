import { AxiosError, type AxiosAdapter, type AxiosResponse, type InternalAxiosRequestConfig } from 'axios'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiError, Code, http, req, UNAUTHORIZED_EVENT } from '../client'

/**
 * client.ts 的契约测试。
 *
 * 这个模块是全站请求的唯一出口，三条契约（文件头注释）在这里锁死：
 *   1. code != 0 必须抛 ApiError（业务失败也是 HTTP 200）；
 *   2. HTTP 4xx/5xx 归一化为同一个 ApiError；
 *   3. 401 广播 UNAUTHORIZED_EVENT。
 *
 * 全部走自定义 axios adapter，**不发任何真实网络请求**。
 */

// ---------------------------------------------------------------------------
// 测试基建：替换 transport 层
// ---------------------------------------------------------------------------

/** 用自定义 adapter 替换 axios 传输层，返回恢复函数。 */
function useAdapter(fn: AxiosAdapter): () => void {
  const prev = http.defaults.adapter
  http.defaults.adapter = fn
  return () => {
    http.defaults.adapter = prev
  }
}

const emptyCfg = {} as InternalAxiosRequestConfig

/** 构造一个「后端返回统一响应体」的成功响应。 */
function okResp(data: unknown): AxiosResponse {
  return { status: 200, statusText: 'OK', headers: {}, config: emptyCfg, data }
}

/** 构造一个带响应体的 axios 错误（HTTP 层失败）。 */
function httpErr(status: number, data: unknown): AxiosError {
  const resp: AxiosResponse = {
    status,
    statusText: String(status),
    headers: {},
    config: emptyCfg,
    data,
  }
  return new AxiosError(`Request failed with status code ${status}`, AxiosError.ERR_BAD_REQUEST, emptyCfg, {}, resp)
}

/** 监听 401 广播事件。 */
function spyUnauthorized(): { fired: () => boolean; restore: () => void } {
  let fired = false
  const handler = () => {
    fired = true
  }
  window.addEventListener(UNAUTHORIZED_EVENT, handler)
  return {
    fired: () => fired,
    restore: () => window.removeEventListener(UNAUTHORIZED_EVENT, handler),
  }
}

afterEach(() => {
  vi.restoreAllMocks()
})

// ---------------------------------------------------------------------------
// req：解包与契约
// ---------------------------------------------------------------------------

describe('req 解包统一响应体', () => {
  it('code=0 时返回 data 字段本身', async () => {
    const restore = useAdapter(() => Promise.resolve(okResp({ code: 0, message: 'ok', data: { hello: 1 } })))
    try {
      await expect(req({ url: '/x' })).resolves.toEqual({ hello: 1 })
    } finally {
      restore()
    }
  })

  it('code!=0 时抛 ApiError 且携带业务码（业务失败也是 HTTP 200）', async () => {
    const restore = useAdapter(() =>
      Promise.resolve(okResp({ code: Code.NotFound, message: '用例不存在', data: null })),
    )
    try {
      const err = await req({ url: '/x' }).catch((e: unknown) => e)
      expect(err).toBeInstanceOf(ApiError)
      expect((err as ApiError).code).toBe(Code.NotFound)
      expect((err as ApiError).message).toBe('用例不存在')
    } finally {
      restore()
    }
  })

  it('响应缺少 code 字段时报契约错误，而不是静默当成功', async () => {
    const restore = useAdapter(() => Promise.resolve(okResp({ foo: 1 })))
    try {
      const err = await req({ url: '/x' }).catch((e: unknown) => e)
      expect(err).toBeInstanceOf(ApiError)
      expect((err as ApiError).code).toBe(Code.Internal)
      expect((err as ApiError).message).toContain('契约')
    } finally {
      restore()
    }
  })
})

// ---------------------------------------------------------------------------
// 错误归一化：HTTP 层失败 → 同一个 ApiError
// ---------------------------------------------------------------------------

describe('HTTP 错误归一化', () => {
  it('4xx 统一响应体照用 code/message', async () => {
    const restore = useAdapter(() => Promise.reject(httpErr(400, { code: Code.Conflict, message: '名称已存在' })))
    try {
      const err = (await req({ url: '/x' }).catch((e: unknown) => e)) as ApiError
      expect(err).toBeInstanceOf(ApiError)
      expect(err.code).toBe(Code.Conflict)
      expect(err.httpStatus).toBe(400)
      expect(err.message).toBe('名称已存在')
    } finally {
      restore()
    }
  })

  it('401 + 统一响应体：抛 ApiError 并广播未授权事件', async () => {
    const spy = spyUnauthorized()
    const restore = useAdapter(() => Promise.reject(httpErr(401, { code: Code.Unauthorized, message: '未登录' })))
    try {
      const err = (await req({ url: '/x' }).catch((e: unknown) => e)) as ApiError
      expect(err.isUnauthorized).toBe(true)
      expect(spy.fired()).toBe(true)
    } finally {
      restore()
      spy.restore()
    }
  })

  it('401 但响应体没有 code（如网关返回 HTML）：仍归一化为 40100 并广播', async () => {
    const spy = spyUnauthorized()
    const restore = useAdapter(() => Promise.reject(httpErr(401, '<html>gateway</html>')))
    try {
      const err = (await req({ url: '/x' }).catch((e: unknown) => e)) as ApiError
      expect(err.code).toBe(Code.Unauthorized)
      expect(err.message).toContain('登录已过期')
      expect(spy.fired()).toBe(true)
    } finally {
      restore()
      spy.restore()
    }
  })

  it('超时（ECONNABORTED）归一化为内部错误 + 超时文案', async () => {
    const restore = useAdapter(() =>
      Promise.reject(new AxiosError('timeout of 60000ms exceeded', 'ECONNABORTED', emptyCfg)),
    )
    try {
      const err = (await req({ url: '/x' }).catch((e: unknown) => e)) as ApiError
      expect(err.httpStatus).toBe(0)
      expect(err.message).toContain('超时')
    } finally {
      restore()
    }
  })

  it('连接不上（无 response）给出可行动的提示而不是裸 error.message', async () => {
    const restore = useAdapter(() =>
      Promise.reject(new AxiosError('Network Error', AxiosError.ERR_NETWORK, emptyCfg)),
    )
    try {
      const err = (await req({ url: '/x' }).catch((e: unknown) => e)) as ApiError
      expect(err.message).toContain('无法连接后端服务')
    } finally {
      restore()
    }
  })
})

// ---------------------------------------------------------------------------
// ApiError 判定器
// ---------------------------------------------------------------------------

describe('ApiError 判定器', () => {
  it('isUnauthorized 同时认业务码与 HTTP 401 两条路径', () => {
    expect(new ApiError(Code.Unauthorized, 'x').isUnauthorized).toBe(true)
    expect(new ApiError(Code.Internal, 'x', 401).isUnauthorized).toBe(true)
    expect(new ApiError(Code.Forbidden, 'x').isUnauthorized).toBe(false)
  })

  it('isNotFound / isReportMissing / isValidateFail 按码判定', () => {
    expect(new ApiError(Code.NotFound, 'x').isNotFound).toBe(true)
    expect(new ApiError(Code.ReportMissing, 'x').isReportMissing).toBe(true)
    expect(new ApiError(Code.ValidateFail, 'x').isValidateFail).toBe(true)
    expect(new ApiError(Code.EngineDown, 'x').isNotFound).toBe(false)
  })

  it('data 字段透传（校验失败的问题列表靠它送到页面）', () => {
    const issues = [{ level: 'error', code: 'X', message: '缺 url' }]
    const e = new ApiError(Code.ValidateFail, '校验未通过', 200, issues)
    expect(e.data).toEqual(issues)
  })
})

// ---------------------------------------------------------------------------
// 业务码与后端常量对齐
// ---------------------------------------------------------------------------

describe('Code 与后端 response 包对齐', () => {
  it('16 个业务码两两不同且值正确', () => {
    // 值以 internal/pkg/response/response.go 为准；改后端时这里会红。
    const expected: Record<keyof typeof Code, number> = {
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
      ReportMissing: 50006,
    }
    // 与后端对齐的 16 个键一个都不能少（少一个说明前后端漂移了）。
    expect(Object.keys(Code).sort()).toEqual(Object.keys(expected).sort())
    for (const [k, v] of Object.entries(expected)) {
      expect(Code[k as keyof typeof Code]).toBe(v)
    }
  })
})
