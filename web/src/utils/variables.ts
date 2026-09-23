/**
 * 变量引用解析（M3 ③-c 4.3 变量依赖提示）。
 *
 * 三件套（对应设计文档 4.3）：
 *   1. **`$变量名` 识别**：`$` 后跟变量名，边界为「非标识符字符」。
 *   2. **`${变量名}` 边界**：花括号形式能把变量名与后续文本隔开，如 `${token}abc`。
 *   3. **`$$` 转义**：连续两个 `$` 是字面量 `$`，不视为变量引用。
 *
 * 引擎的真实解析规则（httprunner 变量求值）远比这里复杂（还支持函数 `${func()}`、
 * 嵌套取属性 `${body.id}`），这里只做「变量引用」这一层 —— 目的是**提示**，
 * 不是替代引擎。命中不了的复杂写法交给执行时引擎报错（退出码 21）。
 */

export interface VariableRef {
  /** 变量名（不含 $ 前缀与花括号） */
  name: string
  /** 在原文中的起始下标 */
  start: number
  /** 在原文中的结束下标（不含） */
  end: number
}

/**
 * 解析一段文本里的全部变量引用。
 *
 * 规则：
 *   - `$name`：$ 后连续 [A-Za-z0-9_]，边界为其他字符。
 *   - `${name}`：花括号内是变量名。
 *   - `$$`：转义，整体跳过一个 $，不产生引用。
 *
 * 返回按出现顺序排列的引用列表（可能重复，同名出现多次各算一条）。
 */
export function parseVariableRefs(text: string): VariableRef[] {
  const refs: VariableRef[] = []
  if (!text) return refs

  let i = 0
  while (i < text.length) {
    const ch = text[i]
    if (ch !== '$') {
      i += 1
      continue
    }

    // 转义：$$ → 字面量 $，跳过两个字符
    if (text[i + 1] === '$') {
      i += 2
      continue
    }

    // ${name} 花括号形式
    if (text[i + 1] === '{') {
      const close = text.indexOf('}', i + 2)
      if (close > i + 2) {
        const name = text.slice(i + 2, close)
        if (name.trim() !== '') {
          refs.push({ name: name.trim(), start: i, end: close + 1 })
        }
        i = close + 1
        continue
      }
      // 未闭合的 ${，不识别，跳过这个 $
      i += 1
      continue
    }

    // $name 形式：$ 后首字符必须是字母或下划线，其后可跟数字。
    // 首字符排除数字是有意的：`价格 $5` 这种金额表达不该被当成变量引用。
    if (i + 1 < text.length && isIdentStart(text[i + 1])) {
      let j = i + 1
      while (j < text.length && isIdentChar(text[j])) {
        j += 1
      }
      refs.push({ name: text.slice(i + 1, j), start: i, end: j })
      i = j
      continue
    }

    // 孤立的 $（后面既不是 $ 也不是 { 也不是合法变量名首字符），跳过
    i += 1
  }

  return refs
}

function isIdentStart(ch: string): boolean {
  return /[A-Za-z_]/.test(ch)
}

function isIdentChar(ch: string): boolean {
  return /[A-Za-z0-9_]/.test(ch)
}

/**
 * 收集一段文本里引用了但不在「已定义集合」里的变量名。
 *
 * 返回去重后的未定义变量名列表（保持首次出现顺序）。
 */
export function undefinedRefs(text: string, defined: Set<string>): string[] {
  const refs = parseVariableRefs(text)
  const seen = new Set<string>()
  const out: string[] = []
  for (const r of refs) {
    // 内置变量（引擎/平台注入）不算未定义
    if (BUILTIN_VARS.has(r.name)) continue
    if (defined.has(r.name)) continue
    if (seen.has(r.name)) continue
    seen.add(r.name)
    out.push(r.name)
  }
  return out
}

/**
 * 引擎与平台自动注入的变量，引用它们不算「未定义」。
 *
 * base_url 来自环境 .env（hrp v4.1 起 base_url 移到 .env），
 * 是最常见的引用目标；其余是 httprunner 内置。
 */
export const BUILTIN_VARS = new Set<string>([
  'base_url',
  'ENV',
  'hrp',
  // 参数化产生的变量名由数据集列名决定，运行时才注入，这里不做静态穷举
])
