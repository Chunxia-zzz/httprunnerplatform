import type { AssertItem, BodyType, ExtractItem } from '@/api'
import type { KVRow } from '@/utils/format'

/**
 * 编辑器内部使用的步骤模型。
 *
 * 刻意**不等于** `StepReq`：表单需要的是"用户正在打字的文本"，
 * 而接口要的是"已解析的结构化值"。例如请求体在界面上是 textarea 里的
 * JSON 文本，提交时才解析成对象 —— 若直接双向绑定接口结构，
 * 用户打到一半的 `{"a":` 会立刻让整个表单失去合法性。
 *
 * 因此在 CaseEditorView 里有一个明确的 `toStepReq()` 转换边界。
 */
export interface EditorStep {
  /** 仅用于 v-for key 与排序，不发给后端 */
  uid: number
  /** 后端步骤的 seq（数据库序号）。新建步骤时由父组件分配（当前最大 seq + 1） */
  seq: number
  name: string
  enabled: boolean
  method: string
  url: string
  headers: KVRow[]
  params: KVRow[]
  bodyType: BodyType
  /** body_type=json 时的原始文本 */
  bodyJson: string
  /** body_type=raw 时的原始文本 */
  bodyRaw: string
  /** body_type=form 时的键值对 */
  bodyForm: KVRow[]
  /** 单请求超时（秒），0 表示不设置。写在 request 层才生效（实测 F13） */
  timeout: number
  /** 步骤级变量 */
  variables: KVRow[]
  extract: ExtractItem[]
  validate: AssertItem[]
}

let seq = 0

/** 新建一个空请求步骤。 */
export function newStep(): EditorStep {
  seq += 1
  return {
    uid: seq,
    seq: 0,
    name: '',
    enabled: true,
    method: 'GET',
    url: '/',
    headers: [],
    params: [],
    bodyType: 'none',
    bodyJson: '',
    bodyRaw: '',
    bodyForm: [],
    timeout: 0,
    variables: [],
    extract: [],
    validate: [],
  }
}

/** 保证 uid 不重复地复制一个步骤（用于"复制步骤"）。 */
export function cloneStep(src: EditorStep): EditorStep {
  seq += 1
  return {
    ...src,
    uid: seq,
    seq: 0, // 复制出来的步骤是新的，seq 由父组件在保存时统一分配
    name: src.name,
    headers: src.headers.map((r) => ({ ...r })),
    params: src.params.map((r) => ({ ...r })),
    bodyForm: src.bodyForm.map((r) => ({ ...r })),
    variables: src.variables.map((r) => ({ ...r })),
    extract: src.extract.map((r) => ({ ...r })),
    validate: src.validate.map((r) => ({ ...r })),
  }
}

/**
 * 判断 URL 是否会被引擎补结尾斜杠（实测 A1）。
 *
 * 触发条件：最终 URL 不带查询串（既没有 inline `?x=y`，params 也为空）。
 * `params: {}` / `params: null` 也照样补 —— 但编译器对空 params 会直接
 * 不写进 YAML（`len(req.Params) > 0`），所以这里只需要看界面上的 params 行。
 */
export function willAppendTrailingSlash(url: string, params: KVRow[]): boolean {
  const u = (url || '').trim()
  if (!u) return false
  if (u.includes('?')) return false
  if (u.endsWith('/')) return false
  const hasParam = params.some((r) => (r.key || '').trim() !== '')
  return !hasParam
}
