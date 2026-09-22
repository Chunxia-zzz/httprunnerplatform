/**
 * 展示层的格式化工具。
 */

/** 把 RFC3339 时间转成本地可读格式。空值返回占位符而不是 "Invalid Date"。 */
export function formatTime(iso: string | null | undefined, fallback = '—'): string {
  if (!iso) return fallback
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return fallback
  const pad = (n: number) => String(n).padStart(2, '0')
  return (
    `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
    `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  )
}

/** 时长格式化。执行耗时从毫秒到分钟都有可能，分档显示更易读。 */
export function formatDuration(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || Number.isNaN(ms)) return '—'
  if (ms < 0) return '—'
  if (ms < 1000) return `${ms}ms`
  const sec = ms / 1000
  if (sec < 60) return `${sec.toFixed(sec < 10 ? 2 : 1)}s`
  const m = Math.floor(sec / 60)
  const s = Math.round(sec % 60)
  return `${m}m ${s}s`
}

/** 截断长文本用于表格内联展示。 */
export function truncate(s: string, n = 60): string {
  if (!s) return ''
  return s.length > n ? `${s.slice(0, n)}…` : s
}

/**
 * 把任意值渲染成适合放进 `<pre>` 的文本。
 *
 * 对象走 JSON.stringify（缩进 2）；字符串原样返回 —— 这点很重要：
 * 报文 body 是**字符串**，如果统一 JSON.stringify 会给它加一层引号，
 * 用户复制出来的就是 `"{\"a\":1}"`。
 */
export function prettyValue(v: unknown): string {
  if (v === null || v === undefined) return ''
  if (typeof v === 'string') {
    const t = v.trim()
    // 字符串内容是 JSON 时尝试美化，方便阅读报文
    if ((t.startsWith('{') && t.endsWith('}')) || (t.startsWith('[') && t.endsWith(']'))) {
      try {
        return JSON.stringify(JSON.parse(t), null, 2)
      } catch {
        return v
      }
    }
    return v
  }
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
}

/** 键值对表格的一行。KeyValueEditor 与 map 互转共用。 */
export interface KVRow {
  key: string
  value: string
}

/** 键值对表格 → object。空键的行会被丢弃。 */
export function rowsToMap(rows: KVRow[]): Record<string, string> | null {
  const out: Record<string, string> = {}
  for (const r of rows) {
    const k = (r.key ?? '').trim()
    if (!k) continue
    out[k] = r.value ?? ''
  }
  return Object.keys(out).length ? out : null
}

/** object → 键值对表格。 */
export function mapToRows(m: Record<string, unknown> | null | undefined): KVRow[] {
  if (!m) return []
  return Object.entries(m).map(([key, value]) => ({
    key,
    value: typeof value === 'string' ? value : JSON.stringify(value),
  }))
}

/** 变量表导出为 .env 风格的只读文本。 */
export function mapToEnvText(m: Record<string, unknown> | null | undefined): string {
  if (!m) return ''
  return Object.entries(m)
    .map(([k, v]) => `${k}=${typeof v === 'string' ? v : JSON.stringify(v)}`)
    .join('\n')
}
