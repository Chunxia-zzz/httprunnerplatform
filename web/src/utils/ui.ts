/** Element Plus 的标签类型。集中定义，避免各处写裸字符串。 */
export type TagType = 'success' | 'info' | 'warning' | 'danger' | 'primary'

/** 按钮/文本语义色。 */
export const COLORS = {
  pass: '#67c23a',
  fail: '#f56c6c',
  error: '#e6a23c',
  muted: '#909399',
} as const
