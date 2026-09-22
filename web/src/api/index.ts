/** API 层统一出口。页面只从这里 import，便于日后整体替换实现。 */
export * from './types'
export * as authApi from './auth'
export * as projectApi from './project'
export * as environmentApi from './environment'
export * as caseApi from './case'
export * as runApi from './run'
export * as engineApi from './engine'

// ---------------------------------------------------------------------------
// 逐个透出子模块里的**具名导出**。
//
// `export * as xxx` 只创建命名空间对象，不会把子模块里的常量/函数提升到
// 本模块。页面若写 `import { MASKED_VALUE } from '@/api'`，TypeScript 会
// 报「has no exported member」——所以这些必须显式列出。
// ---------------------------------------------------------------------------
export { ApiError, Code, UNAUTHORIZED_EVENT, http, req } from './client'
export { ISSUE_CODE_HINT } from './case'
export { MASKED_VALUE } from './environment'
export { fetchReport, isTerminalRun } from './run'
export type { ReportResult } from './run'

