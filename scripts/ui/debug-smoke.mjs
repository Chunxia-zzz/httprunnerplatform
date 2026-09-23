/**
 * 单步调试的浏览器端到端验收（CDP 驱动，零依赖）。
 *
 * 用法：
 *   HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/debug-smoke.mjs
 *
 * 前置：
 *   1. 后端已在 HRP_UI_BASE 的 /api 代理目标上运行（默认 127.0.0.1:8127）；
 *   2. 前端 dev server 已起（代理指向后端）；
 *   3. 被测桩服务（/login 返回 token，/user 返回 200）在 8133。
 *
 * 为什么必须走浏览器：单步调试是 M3 的**交互式**核心体验，接口全绿不代表
 * 「步骤卡片上的『调试此步』按钮 → 右侧面板正确展示目标步 + 变量提取」这条
 * 用户真正会走的路径是通的。
 */
import { mkdirSync } from 'node:fs'
import { connect, evaluate, goto, launchChrome, screenshot, shutdown, waitFor } from './cdp.mjs'

const BASE = process.env.HRP_UI_BASE || 'http://127.0.0.1:5173'
const SHOTS = process.env.HRP_UI_SHOTS || 'F:/httprunnerplatform-tools/tmp/ui-shots'

const ADMIN = { user: 'admin', pass: 'admin123' }
const SUFFIX = String(Date.now()).slice(-6)

let ok = 0
const failures = []
function check(name, cond, extra = '') {
  if (cond) {
    ok += 1
    console.log(`  ✅ ${name}`)
  } else {
    failures.push(name)
    console.log(`  ❌ ${name}${extra ? ` — ${extra}` : ''}`)
  }
}
function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms))
}

// 与 auth-smoke / orchestration-smoke 共用的页面内助手。
const HELPERS = `
window.__t = {
  norm(s) { return (s || '').replace(/\\s+/g, ' ').trim() },
  text(el) { return this.norm(el && el.innerText) },
  body() { return this.norm(document.body.innerText) },
  has(s) { return this.body().includes(s) },
  setInput(el, v) {
    if (!el) throw new Error('setInput: 元素不存在')
    const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement : window.HTMLInputElement
    Object.getOwnPropertyDescriptor(proto.prototype, 'value').set.call(el, v)
    el.dispatchEvent(new Event('input', { bubbles: true }))
    el.dispatchEvent(new Event('change', { bubbles: true }))
  },
  dlg(title) {
    return [...document.querySelectorAll('.el-dialog')]
      .find(d => this.text(d.querySelector('.el-dialog__title')) === title) || null
  },
  btn(root, label) {
    if (!root) return null
    return [...root.querySelectorAll('button')].find(b => this.text(b) === label) || null
  },
  // 找到第 index 个「调试此步」按钮
  debugBtns() {
    return [...document.querySelectorAll('button')].filter(b => this.text(b) === '调试此步')
  },
}
'ok'
`

async function gotoPage(send, path, waitText) {
  await goto(send, `${BASE}${path}`)
  await evaluate(send, HELPERS)
  if (waitText) {
    await waitFor(send, `window.__t.has(${JSON.stringify(waitText)})`, {
      timeout: 20_000,
      label: `等待「${waitText}」`,
    })
  }
}

async function shard(send, name) {
  await sleep(600)
  mkdirSync(SHOTS, { recursive: true })
  await screenshot(send, `${SHOTS}/${name}.png`)
}

async function useCookie(send, value) {
  await send('Network.clearBrowserCookies')
  if (value) {
    await send('Network.setCookie', { name: 'hrp_session', value, url: BASE, path: '/', httpOnly: true })
  }
}

async function formLogin(send, user, pass) {
  await useCookie(send, null)
  await gotoPage(send, '/login', '接口自动化测试平台')
  await evaluate(
    send,
    `(() => {
      const inputs = [...document.querySelectorAll('.login__card input')]
      window.__t.setInput(inputs[0], ${JSON.stringify(user)})
      window.__t.setInput(inputs[1], ${JSON.stringify(pass)})
      document.querySelector('.login__submit').click()
      return 'clicked'
    })()`,
  )
  await waitFor(send, `location.pathname !== '/login'`, { timeout: 20_000, label: '登录跳转' })
  await evaluate(send, HELPERS)
}

async function api(send, pathname, method = 'GET', payload) {
  return await evaluate(
    send,
    `fetch('/api/v1${pathname}', {
        method: ${JSON.stringify(method)},
        headers: { 'Content-Type': 'application/json' },
        body: ${payload === undefined ? 'undefined' : JSON.stringify(JSON.stringify(payload))},
      }).then(async r => ({ http: r.status, body: await r.json().catch(() => null) }))`,
  )
}

async function pickProject(send, projectId, code) {
  await evaluate(
    send,
    `(() => { localStorage.setItem('hrp.currentProjectId', String(${projectId})); location.reload(); return 'ok' })()`,
  )
  await waitFor(send, `document.querySelector('#app')?.children.length > 0 && location.pathname !== '/login'`, {
    timeout: 20_000,
    label: '刷新后回到主布局',
  })
  await evaluate(send, HELPERS)
  await waitFor(
    send,
    `(document.querySelector('.layout__header-left')?.innerText || '').includes(${JSON.stringify(code)})`,
    { timeout: 15_000, label: '顶栏显示目标项目' },
  )
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------
const { proc } = await launchChrome()
const { send } = await connect()
await send('Network.enable')

try {
  console.log(`\n靶子 ${BASE}    后缀 ${SUFFIX}\n`)

  // =========================================================================
  console.log('STEP 1  登录 + 造前置数据（项目/环境/含变量依赖的用例）')
  // =========================================================================
  await formLogin(send, ADMIN.user, ADMIN.pass)

  const proj = await api(send, '/projects', 'POST', { code: `dbg${SUFFIX}`, name: `调试UI验收项目 ${SUFFIX}` })
  check('建项目成功', proj.body?.code === 0, JSON.stringify(proj.body))
  const projectId = proj.body.data.id

  const env = await api(send, `/projects/${projectId}/environments`, 'POST', {
    name: '本地桩', base_url: 'http://127.0.0.1:8133', variables: {}, is_default: true,
  })
  check('建环境成功', env.body?.code === 0)

  // 用例：第 1 步登录提取 token，第 2 步用 token 查用户（验证变量依赖）
  const tc = await api(send, `/projects/${projectId}/cases`, 'POST', {
    code: `tc${SUFFIX}`,
    name: '调试链路用例',
    module: 'dbg',
    priority: 'P0',
    status: 'active',
    config: { verify: false, variables: {}, export: [] },
    steps: [
      {
        seq: 1, step_type: 'request', name: '登录', enabled: true,
        request: { method: 'GET', url: '/login', headers: {}, body: null, body_type: 'none' },
        extract: [{ name: 'token', object: 'body', expression: 'token' }],
        validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
      },
      {
        seq: 2, step_type: 'request', name: '查用户', enabled: true,
        request: { method: 'GET', url: '/user', headers: { Authorization: 'Bearer $token' }, body: null, body_type: 'none' },
        extract: [],
        validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
      },
    ],
  })
  check('建用例成功', tc.body?.code === 0, JSON.stringify(tc.body))
  const caseId = tc.body.data.id

  await pickProject(send, projectId, `dbg${SUFFIX}`)

  // =========================================================================
  console.log('\nSTEP 2  打开用例编辑器，看到「调试此步」按钮')
  // =========================================================================
  await gotoPage(send, `/cases/${caseId}/edit`, '编辑用例')
  await waitFor(send, `window.__t.debugBtns().length === 2`, {
    timeout: 20_000,
    label: '出现两个「调试此步」按钮',
  })
  const debugBtnCount = await evaluate(send, `window.__t.debugBtns().length`)
  check('两个启用步骤各有一个「调试此步」按钮', debugBtnCount === 2, `实际 ${debugBtnCount}`)
  await shard(send, '01-editor-debug-buttons')

  // =========================================================================
  console.log('\nSTEP 3  点第 2 步的「调试此步」→ 调试面板展示结果')
  // =========================================================================
  await evaluate(
    send,
    `(() => {
      window.__t.debugBtns()[1].click()  // 第 2 步
      return 'clicked'
    })()`,
  )
  // 调试是同步阻塞的，等待面板出现「目标步骤」提示（表示结果已返回）
  await waitFor(send, `window.__t.has('目标步骤')`, { timeout: 40_000, label: '调试结果返回' })
  await shard(send, '02-debug-panel-result')

  const panelText = await evaluate(send, `window.__t.body()`)
  check('面板显示「实际执行 2 步（含前置）」', panelText.includes('实际执行 2 步'), panelText.slice(0, 300))
  check('面板标记了「目标」步骤', panelText.includes('目标'), panelText.slice(0, 300))
  check('面板展示第 1 步提取的变量 token=tok_12345', panelText.includes('tok_12345'), panelText.slice(0, 500))
  check('面板显示断言明细', panelText.includes('断言明细'), panelText.slice(0, 300))
  check('面板整体状态为「通过」', panelText.includes('通过'), panelText.slice(0, 300))

  // 目标步骤有高亮样式（dp__step--target）
  const targetCount = await evaluate(send, `document.querySelectorAll('.dp__step--target').length`)
  check('目标步骤有高亮标记', targetCount === 1, `实际 ${targetCount}`)

  await shard(send, '03-debug-panel-target-highlight')

  // =========================================================================
  console.log('\nSTEP 4  点第 1 步的「调试此步」→ 只执行 1 步')
  // =========================================================================
  await evaluate(
    send,
    `(() => {
      window.__t.debugBtns()[0].click()  // 第 1 步
      return 'clicked'
    })()`,
  )
  await waitFor(send, `window.__t.has('实际执行 1 步')`, { timeout: 40_000, label: '第 1 步调试结果' })
  const panelText2 = await evaluate(send, `window.__t.body()`)
  check('调试第 1 步时显示「实际执行 1 步」', panelText2.includes('实际执行 1 步'), panelText2.slice(0, 300))
  await shard(send, '04-debug-first-step-only')
} finally {
  await shutdown(proc, undefined)
  console.log(`\n===== ${ok} 项通过，${failures.length} 项失败 =====`)
  failures.forEach((f) => console.log(`  - ${f}`))
  console.log(`截图目录：${SHOTS}`)
  process.exit(failures.length ? 1 : 0)
}
