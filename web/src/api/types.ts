/**
 * 与后端契约严格对齐的类型定义。
 *
 * 真源：docs/接口契约.md 与 internal/model/model.go、internal/service/*.go。
 * 任何一处后端契约变更都必须同步本文件 —— 否则前端会拿到一个
 * 「类型上存在、运行时是 undefined」的字段，而且 TypeScript 不会报错。
 *
 * ⚠️ 空值语义（由 pkg/jsonx 决定，前端必须按此处理）：
 *   - jsonx.Map  （environs / global_headers / extract_result / attribution）→ 空值序列化为 `null`
 *   - jsonx.Any  （config / request / request_snapshot / response_snapshot）→ 空值序列化为 `null`
 *   - jsonx.Slice（extract / validate）→ 空值序列化为 `[]`
 * 因此下面凡是从 jsonx.Map / jsonx.Any 来的字段，类型都带 `| null`。
 */

export type ID = number

// ---------------------------------------------------------------------------
// 通用
// ---------------------------------------------------------------------------

export interface ApiBody<T = unknown> {
  code: number
  message: string
  data: T
}

export interface PageData<T> {
  list: T[]
  total: number
  page: number
  page_size: number
}

export interface PageQuery {
  page?: number
  page_size?: number
}

// ---------------------------------------------------------------------------
// 认证
// ---------------------------------------------------------------------------

export interface Principal {
  user_id: ID
  username: string
  nickname: string
  role: string
}

// ---------------------------------------------------------------------------
// 项目
// ---------------------------------------------------------------------------

export interface Project {
  id: ID
  code: string
  name: string
  description: string
  hrp_version: string
  workspace_path: string
  owner_id: ID
  created_at: string
  updated_at: string
}

export interface ProjectReq {
  code: string
  name: string
  description: string
  hrp_version: string
}

// ---------------------------------------------------------------------------
// 环境
// ---------------------------------------------------------------------------

/** 环境的变量表。值一律被后端转成字符串写入 .env。 */
export type EnvMap = Record<string, unknown>

export interface Environment {
  id: ID
  project_id: ID
  name: string
  base_url: string
  environs: EnvMap | null
  global_headers: EnvMap | null
  verify_ssl: boolean
  is_default: boolean
  created_at: string
  updated_at: string
}

export interface EnvironmentReq {
  name: string
  base_url: string
  environs: EnvMap | null
  global_headers: EnvMap | null
  verify_ssl: boolean
  is_default: boolean
}

// ---------------------------------------------------------------------------
// 用例与步骤
// ---------------------------------------------------------------------------

/** 提取对象白名单（引擎只支持这 5 类，见 model.ValidExtractObjects）。 */
export const EXTRACT_OBJECTS = ['status_code', 'proto', 'headers', 'cookies', 'body'] as const
export type ExtractObject = (typeof EXTRACT_OBJECTS)[number]

/**
 * 内置校验器清单（引擎支持的全部）。
 * 来源：internal/model/model.go 的 AssertItem 注释。
 */
export const ASSERT_METHODS = [
  'eq',
  'equals',
  'equal',
  'lt',
  'le',
  'gt',
  'ge',
  'ne',
  'str_eq',
  'len_eq',
  'len_gt',
  'len_ge',
  'len_lt',
  'len_le',
  'contains',
  'contained_by',
  'type_match',
  'regex_match',
  'startswith',
  'endswith',
] as const
export type AssertMethod = (typeof ASSERT_METHODS)[number]

export const BODY_TYPES = ['json', 'form', 'raw', 'none'] as const
export type BodyType = (typeof BODY_TYPES)[number]

export interface ExtractItem {
  name: string
  object: ExtractObject | string
  expression: string
}

export interface AssertItem {
  check: string
  assert: AssertMethod | string
  expect?: unknown
  expect_type?: string
  msg?: string
}

export interface RequestSpec {
  method?: string
  url?: string
  headers?: EnvMap | null
  params?: EnvMap | null
  body?: unknown
  body_type?: BodyType | string
  timeout?: number
}

export interface TestStep {
  id: ID
  case_id: ID
  seq: number
  step_type: string
  name: string
  ref_api_id: ID
  ref_case_id: ID
  request: RequestSpec | null
  variables: EnvMap | null
  extract: ExtractItem[]
  validate: AssertItem[]
  hooks: unknown | null
  enabled: boolean
  ws_op_type: string
  ws_payload: unknown | null
  created_at: string
  updated_at: string
}

/** 写入用的步骤体。`enabled` 不传 = 默认 true（后端用指针接收）。 */
export interface StepReq {
  seq?: number
  step_type?: string
  name?: string
  ref_api_id?: ID
  ref_case_id?: ID
  request?: RequestSpec | null
  variables?: EnvMap | null
  extract?: ExtractItem[]
  validate?: AssertItem[]
  hooks?: unknown | null
  enabled?: boolean
  ws_op_type?: string
  ws_payload?: unknown | null
}

export interface CaseConfig {
  variables?: EnvMap | null
  parameters?: EnvMap | null
  headers?: EnvMap | null
  verify?: boolean
  export?: string[]
  weight?: number
}

export type CasePriority = 'P0' | 'P1' | 'P2' | 'P3'
export type CaseStatus = 'draft' | 'active' | 'disabled'

export interface TestCase {
  id: ID
  project_id: ID
  code: string
  name: string
  module: string
  priority: CasePriority | string
  tags: string
  status: CaseStatus | string
  description: string
  config: CaseConfig | null
  request_timeout: number
  case_timeout: number
  owner_id: ID
  last_run_id: ID
  last_status: string
  created_at: string
  updated_at: string
}

export interface CaseDetail extends TestCase {
  steps: TestStep[]
}

export interface CaseListItem {
  id: ID
  code: string
  name: string
  module: string
  priority: string
  tags: string
  status: string
  /** 启用步骤数，不是全部步骤数（见 internal/service/case.go enabledStepCounts） */
  step_count: number
  last_run_id: ID
  last_status: string
  updated_at: string
}

export interface ModuleCount {
  module: string
  count: number
}

export interface CaseReq {
  code: string
  name: string
  module: string
  priority: string
  tags: string
  status: string
  description: string
  config: CaseConfig | null
  request_timeout: number
  case_timeout: number
  steps: StepReq[]
}

export interface CaseListQuery extends PageQuery {
  module?: string
  priority?: string
  status?: string
  keyword?: string
}

export interface YAMLPreview {
  filename: string
  yaml: string
  env: string
}

export type IssueLevel = 'error' | 'warning'

export interface ValidateIssue {
  level: IssueLevel
  scope: string
  seq: number
  field: string
  code: string
  message: string
  hint: string
}

export interface ValidateOutcome {
  ok: boolean
  issues: ValidateIssue[]
}

// ---------------------------------------------------------------------------
// 执行
// ---------------------------------------------------------------------------

export type RunStatus = 'queued' | 'running' | 'success' | 'failed' | 'error' | 'canceled'
export type RunTargetType = 'case' | 'suite' | 'plan'
export type StepStatus = 'pending' | 'pass' | 'fail' | 'error' | 'skipped'

export interface RunRecord {
  id: ID
  project_id: ID
  target_type: RunTargetType | string
  target_id: ID
  target_name: string
  env_id: ID
  env_name?: string
  trigger_type: string
  trigger_by: ID
  status: RunStatus | string
  total: number
  passed: number
  failed: number
  error: number
  skipped: number
  duration_ms: number
  /** 归因计数。jsonx.Map 空值会序列化为 null，故带 | null */
  attribution: Record<string, number> | null
  expected_case_count: number
  actual_case_count: number
  /** 用例数对账不一致：无论退出码多好看，都已被强制升为 error */
  count_mismatch: boolean
  workspace_path: string
  log_path: string
  error_msg: string
  started_at: string | null
  finished_at: string | null
  created_at: string
  updated_at: string
}

export interface CaseResult {
  id: ID
  run_id: ID
  case_id: ID
  case_code: string
  /** 引擎 config.name，summary.json 的唯一标识 */
  config_name: string
  seq: number
  status: StepStatus | string
  exit_code: number
  /** 引擎在断言失败时 panic（实测 F8） */
  panic: boolean
  clean_exit: boolean
  attribution: string
  duration_ms: number
  step_total: number
  step_passed: number
  error_msg: string
  /** 产物是否真的还在盘上（服务端 stat 出来的，不是字段推断） */
  has_summary: boolean
  has_report: boolean
  created_at: string
  updated_at: string
}

export interface CaseResultView extends CaseResult {
  /** 服务端生成的归因短标签，前端直接展示，不要自己硬编码文案 */
  attribution_label: string
  /** 服务端生成的判断依据说明 */
  attribution_reason: string
  /** 派生统计：status=fail 的步骤数（被测行为不符预期） */
  step_failed: number
  /** 派生统计：status=error 的步骤数（步骤没跑完） */
  step_error: number
}

export interface RunDetail {
  run: RunRecord
  cases: CaseResultView[]
}

export interface AssertionResult {
  id: ID
  seq: number
  check_expr: string
  assert_method: string
  expect_value: string
  expect_value_type: string
  check_value: string
  check_value_type: string
  passed: boolean
  /** ⭐ true = 该结论不是引擎给的，而是平台用「声明的断言 + stdout 快照」重建的 */
  rebuilt: boolean
  msg: string
}

export interface RequestSnapshot {
  method?: string
  url?: string
  headers?: EnvMap | null
  body?: unknown
}

export interface ResponseSnapshot {
  status_code?: number
  headers?: EnvMap | null
  body?: unknown
  proto?: string
}

export interface StepResult {
  id: ID
  run_id: ID
  case_result_id: ID
  case_code: string
  seq: number
  step_name: string
  step_type: string
  status: StepStatus | string
  /** ⭐ 有 run step start 但无配对 end ⇒ 该步骤未正常结束（引擎在断言阶段崩溃） */
  inferred_failed: boolean
  /** ⭐ 最终生效 URL。可能与 YAML 声明的不同（引擎会补结尾斜杠，实测 A1） */
  final_url: string
  request_snapshot: RequestSnapshot | null
  response_snapshot: ResponseSnapshot | null
  elapsed_ms: number
  extract_result: EnvMap | null
  error_msg: string
  assertions: AssertionResult[]
}

export interface CaseSteps {
  case_result_id: ID
  case_code: string
  config_name: string
  status: string
  attribution: string
  steps: StepResult[]
}

export interface RunOptions {
  case_timeout?: number
  gen_html_report?: boolean
  /** 仅 target_type=suite/plan 生效（M2），M1 忽略 */
  concurrency?: number
}

export interface StartRunReq {
  project_id: ID
  target_type: RunTargetType | string
  target_id: ID
  env_id?: ID
  options?: RunOptions
}

export interface StartRunResp {
  run_id: ID
  status: RunStatus | string
}

export interface RunListQuery extends PageQuery {
  project_id?: ID
  status?: string
  target_type?: string
  target_id?: ID
}

export interface RunLogs {
  stdout: string
  stderr: string
  truncated: boolean
}

// ---------------------------------------------------------------------------
// 引擎
// ---------------------------------------------------------------------------

export interface EngineStatus {
  available: boolean
  binary_path?: string
  version?: string
  go_version?: string
  platform?: string
  python_plugin_enabled?: boolean
  note?: string
  error?: string
}
