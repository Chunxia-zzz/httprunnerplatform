/**
 * UI 字典：把后端的枚举值翻译成标签文案与颜色。
 *
 * ⚠️ 归因（attribution）的**文案不在前端定义**：
 * 服务端已经返回了 `attribution_label` 与 `attribution_reason`，
 * 前端直接展示即可（见 internal/model/model.go 的注释）。
 * 这里只为它补一个颜色，因为颜色是纯展示层的事。
 */
import type { TagType } from './ui'

export interface StatusMeta {
  label: string
  type: TagType
  desc: string
}

/** 执行状态。`error` 表示平台/环境层面的失败，区别于 `failed`（有用例失败）。 */
export const RUN_STATUS_META: Record<string, StatusMeta> = {
  queued: { label: '排队中', type: 'info', desc: '已入队，等待执行槽位' },
  running: { label: '执行中', type: 'primary', desc: '正在执行，可轮询查看进度' },
  success: { label: '通过', type: 'success', desc: '执行完成且全部用例通过' },
  failed: { label: '失败', type: 'danger', desc: '有用例未通过（被测行为不符预期）' },
  error: { label: '错误', type: 'warning', desc: '平台/环境层面的失败，或用例数对账不一致' },
  canceled: { label: '已终止', type: 'info', desc: '被用户主动终止' },
}

export function runStatusMeta(status: string): StatusMeta {
  return RUN_STATUS_META[status] ?? { label: status || '未知', type: 'info', desc: '' }
}

/** 用例/步骤结果状态。 */
export const STEP_STATUS_META: Record<string, StatusMeta> = {
  pending: { label: '待执行', type: 'info', desc: '' },
  pass: { label: '通过', type: 'success', desc: '断言全部通过' },
  fail: { label: '失败', type: 'danger', desc: '断言未通过（被测行为不符预期）' },
  error: { label: '错误', type: 'warning', desc: '步骤未正常结束或环境异常' },
  skipped: { label: '跳过', type: 'info', desc: '' },
}

export function stepStatusMeta(status: string): StatusMeta {
  return STEP_STATUS_META[status] ?? { label: status || '未知', type: 'info', desc: '' }
}

/**
 * 归因标签配色。
 *
 * 八个归因枚举与 internal/model/model.go 的常量一一对应；
 * 标签文案仍取服务端的 attribution_label。
 */
export const ATTR_TAG_TYPE: Record<string, TagType> = {
  pass: 'success',
  system_under_test: 'danger',
  environment: 'warning',
  case_issue: 'warning',
  ops: 'warning',
  timeout: 'warning',
  canceled: 'info',
  unknown: 'info',
}

export function attrTagType(attr: string): TagType {
  return ATTR_TAG_TYPE[attr] ?? 'info'
}

export const PRIORITY_META: Record<string, { label: string; type: TagType }> = {
  P0: { label: 'P0', type: 'danger' },
  P1: { label: 'P1', type: 'warning' },
  P2: { label: 'P2', type: 'primary' },
  P3: { label: 'P3', type: 'info' },
}

export function priorityMeta(p: string): { label: string; type: TagType } {
  return PRIORITY_META[p] ?? { label: p || 'P1', type: 'info' }
}

export const CASE_STATUS_META: Record<string, { label: string; type: TagType }> = {
  draft: { label: '草稿', type: 'info' },
  active: { label: '启用', type: 'success' },
  disabled: { label: '禁用', type: 'warning' },
}

export function caseStatusMeta(s: string): { label: string; type: TagType } {
  return CASE_STATUS_META[s] ?? { label: s || '未知', type: 'info' }
}

/** hrp 支持的请求方法（M1 只开放 request 步骤）。 */
export const HTTP_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS'] as const
export const STEP_TYPE_META: Record<string, string> = {
  request: '请求',
  api: '引用接口',
  testcase: '引用用例',
  transaction: '事务',
  rendezvous: '集合点',
  think_time: '思考时间',
  websocket: 'WebSocket',
}

export function stepTypeLabel(t: string): string {
  return STEP_TYPE_META[t] ?? t
}

/**
 * 账号角色。只有两档（不引入 RBAC），标签刻意用平实词汇：
 * 「管理员」能管账号、能删项目；「成员」其余功能都能用。
 */
export const ROLE_META: Record<string, { label: string; type: TagType; desc: string }> = {
  admin: { label: '管理员', type: 'danger', desc: '可管理账号、可删除项目' },
  member: { label: '成员', type: 'info', desc: '可用全部用例功能；不能管理账号、不能删除项目' },
}

export function roleMeta(role: string): { label: string; type: TagType; desc: string } {
  return ROLE_META[role] ?? { label: role || '未知', type: 'info', desc: '' }
}

// ---------------------------------------------------------------------------
// 编排层（M2）：执行模式、遇错行为、触发方式
// ---------------------------------------------------------------------------

export const EXECUTE_MODE_META: Record<string, { label: string; type: TagType; desc: string }> = {
  sequential: { label: '串行', type: 'info', desc: '按顺序逐条执行，一个用例一个子进程' },
  parallel: { label: '并行', type: 'warning', desc: '尚未实现，保存时会被拒绝' },
}

export function executeModeMeta(m: string): { label: string; type: TagType; desc: string } {
  return EXECUTE_MODE_META[m] ?? { label: m || '串行', type: 'info', desc: '' }
}

export const ON_FAILURE_META: Record<string, { label: string; type: TagType; desc: string }> = {
  continue: { label: '继续执行', type: 'success', desc: '一条失败不影响后面，一次能看到全部失败' },
  abort: { label: '立即停止', type: 'warning', desc: '第一条失败后不再执行后面的用例' },
}

export function onFailureMeta(v: string): { label: string; type: TagType; desc: string } {
  return ON_FAILURE_META[v] ?? { label: v || '继续执行', type: 'info', desc: '' }
}

export const TRIGGER_TYPE_META: Record<string, { label: string; type: TagType; desc: string }> = {
  manual: { label: '手动', type: 'info', desc: '在页面上点「执行」才会跑' },
  cron: { label: '定时', type: 'primary', desc: '按 cron 表达式自动触发' },
  ci: { label: 'CI', type: 'success', desc: '由 CI 令牌触发（记录值，不能在计划里选）' },
}

export function triggerTypeMeta(t: string): { label: string; type: TagType; desc: string } {
  return TRIGGER_TYPE_META[t] ?? { label: t || '手动', type: 'info', desc: '' }
}

/**
 * 常用时区。给一个下拉而不是让用户手打 IANA 名 ——
 * `Asia/Shanghai` 这类名字打错一个字母就会保存失败，
 * 而"打错了"和"不合法"对用户是同一件事。
 */
export const COMMON_TIMEZONES = [
  { value: 'Asia/Shanghai', label: '中国标准时间 (UTC+8)' },
  { value: 'Asia/Tokyo', label: '日本标准时间 (UTC+9)' },
  { value: 'Asia/Singapore', label: '新加坡时间 (UTC+8)' },
  { value: 'Europe/London', label: '英国时间 (UTC+0/+1)' },
  { value: 'America/New_York', label: '美国东部时间 (UTC-5/-4)' },
  { value: 'America/Los_Angeles', label: '美国太平洋时间 (UTC-8/-7)' },
  { value: 'UTC', label: 'UTC' },
]

/** cron 常用示例，供"不会写 cron"的人直接挑一个。 */
export const CRON_PRESETS = [
  { label: '每分钟', value: '* * * * *' },
  { label: '每小时整点', value: '0 * * * *' },
  { label: '每天 09:00', value: '0 9 * * *' },
  { label: '每天 02:00', value: '0 2 * * *' },
  { label: '工作日 09:00', value: '0 9 * * 1-5' },
  { label: '每周一 09:00', value: '0 9 * * 1' },
  { label: '每 15 分钟', value: '*/15 * * * *' },
]
