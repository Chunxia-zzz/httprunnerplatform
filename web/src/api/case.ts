import { req } from './client'
import type {
  CaseDetail,
  CaseListQuery,
  CaseListItem,
  CaseReq,
  DebugStepReq,
  DebugStepResult,
  ID,
  ModuleCount,
  PageData,
  ValidateOutcome,
  YAMLPreview,
} from './types'

export function listCases(projectId: ID, q: CaseListQuery = {}): Promise<PageData<CaseListItem>> {
  return req<PageData<CaseListItem>>({
    method: 'GET',
    url: `/projects/${projectId}/cases`,
    params: q,
  })
}

/** 左侧模块树。该端点不分页，直接返回数组。 */
export function caseTree(projectId: ID): Promise<ModuleCount[]> {
  return req<ModuleCount[]>({ method: 'GET', url: `/projects/${projectId}/cases/tree` })
}

/** 详情含全部步骤（**包括被禁用的**）。 */
export function getCase(id: ID): Promise<CaseDetail> {
  return req<CaseDetail>({ method: 'GET', url: `/cases/${id}` })
}

export function createCase(projectId: ID, data: CaseReq): Promise<CaseDetail> {
  return req<CaseDetail>({ method: 'POST', url: `/projects/${projectId}/cases`, data })
}

/** 全量覆盖，步骤整体替换。 */
export function updateCase(id: ID, data: CaseReq): Promise<CaseDetail> {
  return req<CaseDetail>({ method: 'PUT', url: `/cases/${id}`, data })
}

export function deleteCase(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/cases/${id}` })
}

/**
 * 编译后的 YAML 预览（只读）。
 *
 * 契约硬要求：内容与执行时写到盘上的文件**逐字节一致**，
 * 因为它和执行共用同一条 compiler.Render 渲染链路。
 */
export function caseYaml(id: ID, envId?: ID): Promise<YAMLPreview> {
  return req<YAMLPreview>({
    method: 'GET',
    url: `/cases/${id}/yaml`,
    params: envId ? { env_id: envId } : {},
  })
}

/**
 * 保存源码视图里编辑过的 YAML（M3 ③-c 4.2）。
 *
 * 后端会把 YAML 反解析回结构化数据落库。反解析失败（非法 YAML、字段类型错）
 * 返回 code=50003，错误信息带「第 N 行」前缀，前端据此高亮报错行。
 */
export function saveCaseYaml(id: ID, yaml: string): Promise<CaseDetail> {
  return req<CaseDetail>({ method: 'PUT', url: `/cases/${id}/yaml`, data: { yaml } })
}

/**
 * 静态校验（不调用引擎）。
 *
 * ⚠️ 校验未通过时后端返回 **HTTP 200 + code=50004**，
 * `req()` 会把它抛成 ApiError，问题列表在 `err.data` 里。
 * 调用方必须 catch ApiError 并检查 `isValidateFail` —— 这不是接口错误，
 * 而是"请求成功、内容有问题"，处理路径完全不同（渲染问题列表 vs 弹错误提示）。
 */
export function validateCase(id: ID, envId?: ID): Promise<ValidateOutcome> {
  return req<ValidateOutcome>({
    method: 'POST',
    url: `/cases/${id}/validate`,
    params: envId ? { env_id: envId } : {},
  })
}

/** issue.code 到中文说明的补充提示。字典真源见 docs/接口契约.md 4 节。 */
export const ISSUE_CODE_HINT: Record<string, string> = {
  TRAILING_SLASH_ADDED:
    '引擎在 URL 不带查询串时会自动补结尾斜杠（实测 A1）：$base_url/get 实际发出的是 GET /get/。params 为空对象或 null 也照样补，平台无法代改，只能提示。修法：让 URL 带上真实查询串，或确认服务端确实支持尾斜杠。',
  UNKNOWN_FIELD:
    '引擎对不认识的字段完全静默（实测 A7）—— 写错的字段不是报错，而是不生效。这是唯一防线。',
  DUPLICATE_CASE_NAME:
    '引擎以 config.name 作为 summary.json 的唯一标识，重名会导致执行结果无法区分（实测 F11）。',
  EMPTY_STEPS:
    '引擎没有"跳过步骤"语法，全部禁用 = 空用例，会被静默丢弃（实测 F5：退出码 0 但一条都没跑）。',
}

/**
 * 单步调试（同步返回）。
 *
 * 这是 M3 调试体验的核心：平台临时构造「只含目标步骤 + 前置步骤」的最小用例，
 * 跑一个独立子进程后返回结构化结果（含每步的变量值、报文快照、断言明细），
 * 不落库、不进执行历史。
 */
export function debugStep(data: DebugStepReq): Promise<DebugStepResult> {
  return req<DebugStepResult>({
    method: 'POST',
    url: `/cases/${data.case_id}/debug`,
    data,
  })
}
