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
// 用户与账号
// ---------------------------------------------------------------------------

/** 角色只有两档（方案 10.2：只做 admin / member，不引入 RBAC）。 */
export type Role = 'admin' | 'member'

export const ROLES: Role[] = ['admin', 'member']

/**
 * 账号列表项。
 *
 * `online_sessions` 不是装饰字段：管理员禁用某人后，这个数字归零才是
 * 「会话吊销真的发生了」的可见证据 —— 否则只能靠猜。
 */
export interface User {
  id: ID
  username: string
  nickname: string
  role: Role | string
  enabled: boolean
  online_sessions: number
  created_at: string
}

/** 新建账号。`enabled` 不传 = 默认启用（后端用指针接收）。 */
export interface CreateUserReq {
  username: string
  password: string
  nickname?: string
  role?: Role | string
  enabled?: boolean
}

/**
 * 修改账号。
 *
 * ⚠️ 刻意**没有** `username` 字段：用户名是登录标识，且会写进
 * `run_record.trigger_by` 关联的历史，允许改名等于让历史记录悄悄换人。
 * 要换名字就新建账号、停用旧账号。后端也据此拒绝了该字段。
 */
export interface UpdateUserReq {
  nickname?: string
  role?: Role | string
  enabled?: boolean
}

/** 本人修改密码（需验旧密码）。 */
export interface ChangePasswordReq {
  old_password: string
  new_password: string
}

/** 管理员重置他人密码（无需旧密码）。 */
export interface ResetPasswordReq {
  password: string
}

export interface UserListQuery extends PageQuery {
  keyword?: string
  role?: string
}

/**
 * 密码格式上限（与 internal/service/user.go 的常量对齐）。
 *
 * ⚠️ bcrypt **静默截断**超过 72 字节的输入，后端因此硬拦；
 * 前端一并拦下，免得用户设了 100 位密码却只有前 72 字节生效。
 */
export const MIN_PASSWORD_LEN = 8
export const MAX_PASSWORD_BYTES = 72

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
  /**
   * 平台层参数化引用（M3 · ③-b）。
   *
   * ⚠️ 这不是引擎的 config.parameters 键，而是独立的 platform-only 键：
   * compiler 渲染时会把它解引用成引擎语法（list → 关联 pairs；csv → ${P()}），
   * 并在写出 YAML 前摘掉。limit 是可选的用例级覆盖（0 = 用数据集默认值）。
   */
  datasets?: DatasetRef[]
}

/** 用例对数据集的引用。name 必须与参数集页的数据集名一致。 */
export interface DatasetRef {
  name: string
  limit?: number
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
// 参数化数据集（M3 · ③-b）
// ---------------------------------------------------------------------------

/** 数据源。list = 内联列表；csv = 项目工作区下的 CSV 文件。 */
export type ParamSource = 'list' | 'csv'

/** 迭代策略。引擎只支持顺序（实测 A22：无 random/unique 开关）。 */
export type ParamStrategy = 'sequential'

/**
 * 数据集视图。
 *
 * row_count / columns / limit_effective 是服务端算好的派生值，
 * 前端直接展示即可，不要自己数。
 */
export interface ParamDataset {
  id: ID
  project_id: ID
  name: string
  source: ParamSource | string
  /** list 数据集的 [{...}, ...]；csv 数据集为 null（jsonx.Any 空值语义） */
  inline: unknown
  /** csv 数据集的相对路径 data/xxx.csv；list 数据集为空串 */
  csv_path: string
  strategy: ParamStrategy | string
  /** 0 = 全部迭代；>0 = 只取前 N 行（编译期裁剪） */
  limit: number
  /** 数据行数（不含 CSV 表头） */
  row_count: number
  /** 列名列表 */
  columns: string[]
  /** 实际生效迭代次数 = limit>0 ? min(limit, row_count) : row_count */
  limit_effective: number
  created_at: string
  updated_at: string
}

/** 创建/更新请求。source=csv 时 csv_name 或 csv_text 至少一个。 */
export interface ParamDatasetReq {
  name: string
  source: ParamSource | string
  /** list 来源：形如 [{"username":"a"}, ...] */
  inline?: unknown
  /** csv 来源：纯文件名，形如 users.csv（不含路径） */
  csv_name?: string
  /** csv 来源：直接传 CSV 文本（新建或替换内容） */
  csv_text?: string
  strategy?: ParamStrategy | string
  limit?: number
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

// ---------------------------------------------------------------------------
// 用例集（M2 · 编排层）
// ---------------------------------------------------------------------------

/** 执行模式。`parallel` 后端明确拒绝（M2 只实现 sequential）。 */
export type ExecuteMode = 'sequential' | 'parallel'

/** 遇错行为。默认 `continue`：一次就能看到全部失败，不用"修一个跑一次"。 */
export type OnFailure = 'abort' | 'continue'

export interface Suite {
  id: ID
  project_id: ID
  code: string
  name: string
  description: string
  execute_mode: ExecuteMode
  on_failure: OnFailure
  timeout: number
  /** 成员数。列表接口带出来，省一次请求。 */
  case_count: number
  created_at: string
  updated_at: string
}

export interface SuiteReq {
  /** 仅创建时可填；更新接口刻意没有这个字段（code 会进入执行历史） */
  code?: string
  name: string
  description?: string
  execute_mode?: ExecuteMode
  on_failure?: OnFailure
  timeout?: number
}

/** 成员视图。`runnable=false` 时看 `skip_reason` 就知道为什么跑不了。 */
export interface SuiteMember {
  seq: number
  case_id: ID
  case_code: string
  case_name: string
  module: string
  priority: string
  status: string
  runnable: boolean
  skip_reason: string
}

// ---------------------------------------------------------------------------
// 测试计划（M2 · 编排层）
// ---------------------------------------------------------------------------

/** 触发方式。`ci` 是记录值，用户不能在计划里选它。 */
export type TriggerType = 'manual' | 'cron' | 'ci'

export interface Plan {
  id: ID
  project_id: ID
  name: string
  description: string
  env_id: ID
  env_name?: string
  trigger_type: TriggerType
  cron_expr: string
  /** cron 的中文说明，例如「每天 09:00」 */
  cron_human: string
  /** ⭐ IANA 时区名。只有配上时区，"几点跑"才有意义。 */
  timezone: string
  /** 下次执行时间（计划时区下的本地时间），手动计划为 null */
  next_fire_at: string | null
  enabled: boolean
  timeout: number
  suite_count: number
  // --- 运行态：让"跳过"与"错过"可见 ---
  last_run_id: ID
  last_fired_at: string | null
  last_missed_at: string | null
  last_skip_reason: string
  created_at: string
  updated_at: string
}

export interface PlanReq {
  name: string
  description?: string
  env_id?: ID
  trigger_type?: TriggerType
  cron_expr?: string
  timezone?: string
  timeout?: number
  /** 用指针语义：`false` 是合法值，不能跟"没传"混在一起 */
  enabled?: boolean
  /** 仅创建时可带：一次挂好成员 */
  suite_ids?: ID[]
}

export interface PlanSuite {
  seq: number
  suite_id: ID
  suite_code: string
  suite_name: string
  case_count: number
  runnable: boolean
  skip_reason: string
}

// ---------------------------------------------------------------------------
// CI 令牌（M2 · 编排层）
// ---------------------------------------------------------------------------

export interface Token {
  id: ID
  project_id: ID
  name: string
  /** 展示用前缀，如 `hrp_ci_a1b2c3d4` */
  prefix: string
  scope: string
  expire_at: string | null
  last_used_at: string | null
  created_at: string
  expired: boolean
}

export interface TokenReq {
  name: string
  /** 留空则给默认权限：run:trigger,run:read */
  scope?: string
  /** 有效天数，0 表示不过期 */
  ttl_days?: number
}

/** 签发结果。⭐ `token` 明文只在这里出现一次。 */
export interface IssuedToken extends Token {
  token: string
}

// ---------------------------------------------------------------------------
// CI 结果（/open/runs/{id}/result）
// ---------------------------------------------------------------------------

/** ⭐ pipeline 直接用的退出码：0 成功 / 1 有用例没过 / 2 没跑完 */
export type ExitCodeForCI = 0 | 1 | 2

export interface OpenRunResult {
  run_id: ID
  /** 这次结果是"等到终态"拿到的，还是"看一眼就返回"的 */
  wait: boolean
  status: RunStatus | string
  exit_code_for_ci: ExitCodeForCI
  total: number
  passed: number
  failed: number
  error: number
  skipped: number
  count_mismatch: boolean
  duration_ms: number
  error_msg: string
  failed_cases?: { case_code: string; status: string; attribution: string; error_msg: string }[]
}

export interface OpenTriggerResp {
  run_id: ID
  status: RunStatus | string
  wait: boolean
  /** 异步触发时给出的轮询地址（/open/runs/{id}/result） */
  poll_at?: string
}

// ---------------------------------------------------------------------------
// 单步调试（/cases/{id}/debug）
// ---------------------------------------------------------------------------

/** 单步调试请求体。 */
export interface DebugStepReq {
  project_id: ID
  case_id: ID
  /** 要调试的目标步骤 seq。平台会真实执行「启用的、seq ≤ 它」的前缀步骤。 */
  step_seq: number
  /** 0 = 项目默认环境 */
  env_id?: ID
  /** 同步阻塞上限（秒），0 = 平台默认 30s */
  timeout_sec?: number
}

/** 调试中单个步骤的结果（与执行详情 StepResult 同构，但字段是 parser 原始名）。 */
export interface DebugStep {
  seq: number
  name: string
  step_type: string
  status: StepStatus | string
  /** ⭐ 标记这一条就是用户要调试的目标步骤 */
  is_target: boolean
  elapsed_ms: number
  extract_result: EnvMap | null
  final_url: string
  final_url_source: string
  request: RequestSnapshot | null
  response: ResponseSnapshot | null
  assertions: AssertionResult[]
  error_msg: string
}

/** 单步调试结果。 */
export interface DebugStepResult {
  status: StepStatus | string
  attribution: string
  error_msg: string
  panic: boolean
  duration_ms: number
  steps: DebugStep[]
  target_seq: number
  actual_step_count: number
  compile_error?: string
}

// ---------------------------------------------------------------------------
// 统计看板（M4）
// ---------------------------------------------------------------------------

/** 通过率趋势上的一个点（按天）。 */
export interface TrendPoint {
  day: string
  total: number
  passed: number
  failed: number
  error: number
  rate: number
  avg_ms: number
}

/** 不稳定/常败用例排行项。 */
export interface FlakyCase {
  case_id: number
  case_code: string
  config_name: string
  runs: number
  passed: number
  rate: number
}

/** 慢用例排行项。 */
export interface SlowCase {
  case_id: number
  case_code: string
  config_name: string
  runs: number
  avg_ms: number
  max_ms: number
}

/** 统计查询参数。 */
export interface StatsQuery {
  project_id: number
  days?: number
  limit?: number
}

// ---------------------------------------------------------------------------
// 用例基线对比（M4-d）
// ---------------------------------------------------------------------------

/** 相邻两次执行之间的差异信号。第 0 项（最早）无 delta。 */
export interface BaselineDelta {
  /** 前一次通过、这一次失败/出错 —— 典型的最先变坏点 */
  broke: boolean
  /** 前一次失败/出错、这一次通过 */
  recovered: boolean
  /** 本次耗时 - 上次耗时（正数 = 变慢） */
  dur_delta_ms: number
  /** 本次通过步骤数 - 上次通过步骤数 */
  step_pass_delta: number
}

/** 某一次执行里该用例的结果快照。 */
export interface BaselineRun {
  run_id: ID
  /** 整次执行的终态（success/failed/error/canceled） */
  run_status: string
  trigger_type: string
  /** RFC3339；空串 = 还在跑 */
  finished_at: string
  /** 该用例的结果状态（pass/fail/error/skipped） */
  status: string
  attribution: string
  duration_ms: number
  step_total: number
  step_passed: number
  step_failed: number
  step_error: number
  error_msg: string
  delta?: BaselineDelta
}

/** 一次基线对比的完整返回。runs 按时间从旧到新。 */
export interface BaselineResult {
  case_id: ID
  case_code: string
  config_name: string
  /** 该项目下该用例的历史执行总次数 */
  total_runs: number
  runs: BaselineRun[]
}
