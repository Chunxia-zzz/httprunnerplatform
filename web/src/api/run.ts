import { ApiError, http, req } from './client'
import type {
  CaseSteps,
  ID,
  PageData,
  RunDetail,
  RunListQuery,
  RunLogs,
  RunRecord,
  StartRunReq,
  StartRunResp,
} from './types'

/** 启动一次执行。立即返回 run_id，真正执行在后台，前端轮询详情。 */
export function startRun(data: StartRunReq): Promise<StartRunResp> {
  return req<StartRunResp>({ method: 'POST', url: '/runs', data })
}

export function listRuns(q: RunListQuery = {}): Promise<PageData<RunRecord>> {
  return req<PageData<RunRecord>>({ method: 'GET', url: '/runs', params: q })
}

/** 执行详情：含用例级结果（含归因标签），**不含**步骤明细。 */
export function getRun(id: ID): Promise<RunDetail> {
  return req<RunDetail>({ method: 'GET', url: `/runs/${id}` })
}

/** 步骤与断言明细。单独端点，因为报文快照可能有几百 KB。 */
export function getRunCases(id: ID): Promise<CaseSteps[]> {
  return req<CaseSteps[]>({ method: 'GET', url: `/runs/${id}/cases` })
}

/** 终止执行。终态时返回 40003。 */
export function cancelRun(id: ID): Promise<{ run_id: ID; status: string }> {
  return req<{ run_id: ID; status: string }>({ method: 'POST', url: `/runs/${id}/cancel` })
}

/** 原始双流日志（stdout 报文快照 / stderr JSON 日志 + Go 栈回溯）。 */
export function getRunLogs(id: ID, caseResultId?: ID): Promise<RunLogs> {
  return req<RunLogs>({
    method: 'GET',
    url: `/runs/${id}/logs`,
    params: caseResultId ? { case_result_id: caseResultId } : {},
  })
}

/** HTML 报告的取回结果。`ok=false` 是**正常业务路径**，不是异常。 */
export interface ReportResult {
  ok: boolean
  html: string
  message?: string
}

/**
 * 取回 HTML 报告。
 *
 * ⚠️ 这个端点的成功响应是裸 `text/html`，失败时才是统一响应体 + HTTP 404。
 * 因此不能走 `req()`（它假设响应体一定有 code 字段），这里直接用 http。
 *
 * **断言失败时报告必然不存在**（实测 F8：引擎 panic，走不到生成报告那一步），
 * 所以 `ok=false` 是高频正常路径。UI 必须用友好文案替代 iframe，而不是显示破图。
 */
export async function fetchReport(runId: ID, caseResultId?: ID): Promise<ReportResult> {
  try {
    const resp = await http.get<string>(`/runs/${runId}/report`, {
      params: caseResultId ? { case_result_id: caseResultId } : {},
    })
    const html = typeof resp.data === 'string' ? resp.data : String(resp.data ?? '')
    if (!html) {
      return { ok: false, html: '', message: '报告内容为空' }
    }
    return { ok: true, html }
  } catch (e) {
    if (e instanceof ApiError && (e.isReportMissing || e.isNotFound)) {
      return { ok: false, html: '', message: e.message }
    }
    throw e
  }
}

/** 执行是否为终态。`queued` / `running` 之外的都算终态。 */
export function isTerminalRun(status: string): boolean {
  return ['success', 'failed', 'error', 'canceled'].includes(status)
}
