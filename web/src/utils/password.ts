import { MAX_PASSWORD_BYTES, MIN_PASSWORD_LEN } from '@/api'

/**
 * 密码策略的前端镜像。
 *
 * ⚠️ 两个刻意的取舍：
 *
 * 1. **按字节算长度，不按字符**。Go 的 `len(pw)` 是字节数，`len()` 在
 *    JavaScript 里是 UTF-16 码元数。若前端用 `pw.length`，一个「4 个汉字」
 *    的密码（12 字节）会被前端拒、被后端接受 —— 用户看到的提示是假的。
 *
 * 2. **不复制后端的弱密码表**。弱密码表是后端的策略（它随时可以调整），
 *    在前端再存一份等于制造第二个真源：后端改一次，前端就静默地开始
 *    误拦。这里只做格式校验，弱密码交给后端返回 40000 的原话。
 *
 * 返回 `null` 表示通过；否则返回可直接展示的原因。
 */
export function passwordIssue(pw: string, username = ''): string | null {
  if (!pw) return '请输入密码'
  // 与后端 strings.ContainsAny(pw, " \t\r\n") 保持一致：
  // 刻意不用 /\s/ —— 它还会匹配 NBSP 与 Unicode 行分隔符，
  // 那会让前端拦掉后端本来接受的密码。
  if (/[\t\r\n ]/.test(pw)) return '密码不能包含空白字符'
  if (byteLength(pw) < MIN_PASSWORD_LEN) return `密码至少 ${MIN_PASSWORD_LEN} 位`
  if (byteLength(pw) > MAX_PASSWORD_BYTES) {
    return `密码不能超过 ${MAX_PASSWORD_BYTES} 字节（bcrypt 会静默截断更长的输入）`
  }
  // 对应后端的 strings.EqualFold(pw, username)；用户名是 ASCII，
  // 所以 toLowerCase 与 EqualFold 在这里等价。
  if (username && pw.toLowerCase() === username.toLowerCase()) {
    return '密码不能与用户名相同'
  }
  return null
}

/** 字符串的 UTF-8 字节数。用于对齐 Go 侧的 len()。 */
export function byteLength(s: string): number {
  return new TextEncoder().encode(s).length
}

/**
 * Element Plus 表单校验器的适配层。
 *
 * 单独抽出来是因为「新建账号」「重置密码」「修改密码」三处都要用，
 * 而 el-form 的 validator 签名要求 callback 风格 —— 三处各写一遍
 * 必然出现某处漏了字节上限这类漂移。
 */
export function passwordValidator(username = '') {
  return (_rule: unknown, value: string, callback: (e?: Error) => void) => {
    const issue = passwordIssue(value ?? '', username)
    callback(issue ? new Error(issue) : undefined)
  }
}
