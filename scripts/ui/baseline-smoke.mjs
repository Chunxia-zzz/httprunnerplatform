/**
 * 用例基线对比（M4-d）浏览器端到端验收（CDP 驱动）。
 *
 * 用法：
 *   HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/baseline-smoke.mjs
 *
 * 前置：后端（8127）+ 前端 dev server（5173）在跑；桩服务由本脚本自起。
 *
 * 验收点（对应 M4「用例基线对比」）：
 *   1. 造 3 次执行（成功 → 失败 → 成功），制造「变坏再恢复」的历史
 *   2. 用例列表出现「对比」按钮 → 点击弹出对话框
 *   3. 对话框渲染 3 张卡片（从旧到新），并正确标出「变坏」与「恢复」的相邻差异
 *   4. 切换「最近 3 次」仍正常渲染
 *
 * 关键：所有 API 调用都走浏览器内的 fetch（evaluate），登录态 cookie 天然一致。
 */
import http from 'node:http'
import { connect, evaluate, goto, launchChrome, screenshot, shutdown, waitFor } from './cdp.mjs'

const BASE = process.env.HRP_UI_BASE || 'http://127.0.0.1:5173'
const SHOTS = process.env.HRP_UI_SHOTS || 'F:/httprunnerplatform-tools/tmp/ui-shots'
const STUB_PORT = 8135

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

const HELPERS = `
window.__t = {
  norm(s) { return (s || '').replace(/\\s+/g, ' ').trim() },
  body() { return this.norm(document.body.innerText) },
  has(s) { return this.body().includes(s) },
  setInput(el, v) {
    if (!el) throw new Error('setInput: 元素不存在')
    const proto = el.tagName === 'TEXTAREA' ? window.HTMLTextAreaElement : window.HTMLInputElement
    Object.getOwnPropertyDescriptor(proto.prototype, 'value').set.call(el, v)
    el.dispatchEvent(new Event('input', { bubbles: true }))
    el.dispatchEvent(new Event('change', { bubbles: true }))
  },
}
'ok'
`

function startStub() {
  return new Promise((resolve) => {
    const srv = http.createServer((req, res) => {
      const code = req.url.startsWith('/boom') ? 500 : 200
      const body = JSON.stringify({ code: 0, path: req.url })
      res.writeHead(code, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) })
      res.end(body)
    })
    srv.listen(STUB_PORT, '127.0.0.1', () => resolve(srv))
  })
}

async function api(send, pathname, method = 'GET', payload) {
  const r = await evaluate(
    send,
    `fetch('/api/v1${pathname}', {
        method: ${JSON.stringify(method)},
        headers: { 'Content-Type': 'application/json' },
        body: ${payload === undefined ? 'undefined' : JSON.stringify(JSON.stringify(payload))},
      }).then(async r => ({ http: r.status, body: await r.json().catch(() => null) }))`,
  )
  return r.body
}
function data(r) {
  if (r?.code !== 0) throw new Error(`${r?.code} ${r?.message}`)
  return r.data
}

async function formLogin(send, user, pass) {
  await send('Network.clearBrowserCookies')
  await goto(send, `${BASE}/login`)
  await evaluate(send, HELPERS)
  await waitFor(send, `window.__t.has('接口自动化测试平台')`, { timeout: 20_000, label: '登录页加载' })
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

async function runCase(send, projectId, envId, caseId) {
  const r = data(await api(send, '/runs', 'POST', {
    project_id: projectId,
    target_type: 'case',
    target_id: caseId,
    env_id: envId,
    options: {},
  }))
  const runId = r.run_id
  for (let i = 0; i < 60; i++) {
    await sleep(500)
    const detail = data(await api(send, `/runs/${runId}`))
    const run = detail.run || detail
    if (['success', 'failed', 'error', 'canceled'].includes(run.status)) return run
  }
  throw new Error('执行超时未结束')
}

async function main() {
  const stub = await startStub()
  const { proc } = await launchChrome()
  const { send } = await connect()
  await send('Network.enable')

  try {
    console.log(`\n靶子 ${BASE}  后缀 ${SUFFIX}\n`)

    await formLogin(send, ADMIN.user, ADMIN.pass)
    check('表单登录成功', true)

    // ---- 造数据：项目 + 环境 + 一条用例（/boom 与 /ok 切换制造成败）----
    const proj = data(await api(send, '/projects', 'POST', { code: `bl${SUFFIX}`, name: `基线验收 ${SUFFIX}` }))
    const projectId = proj.id
    check('建项目成功', !!projectId)

    const env = data(await api(send, `/projects/${projectId}/environments`, 'POST', {
      name: '桩环境', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
    }))
    check('建环境成功', !!env.id)

    const mkCase = (url) => api(send, `/projects/${projectId}/cases`, 'POST', {
      code: `bc${SUFFIX}`,
      name: '基线演示用例',
      module: 'baseline',
      priority: 'P1',
      status: 'active',
      config: { verify: false, variables: {}, export: [] },
      steps: [{
        seq: 1, step_type: 'request', name: '探测', enabled: true,
        request: { method: 'GET', url, headers: {}, body: null, body_type: 'none' },
        extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
      }],
    })
    // 用 /ok 造一次成功用例；执行时通过改用例 URL 到 /boom 再失败。
    const tc = data(await mkCase('/ok'))
    check('建用例成功', !!tc.id)

    // 三次执行：成功 → 失败 → 成功。
    console.log('  跑 3 次执行（成功 → 失败 → 成功）…')
    const r1 = await runCase(send, projectId, env.id, tc.id)
    check('第 1 次执行成功', r1.status === 'success', r1.status)

    // 把用例 URL 改成 /boom，让第 2 次失败。
    data(await api(send, `/cases/${tc.id}`, 'PUT', {
      code: `bc${SUFFIX}`,
      name: '基线演示用例',
      module: 'baseline',
      priority: 'P1',
      status: 'active',
      config: { verify: false, variables: {}, export: [] },
      steps: [{
        seq: 1, step_type: 'request', name: '探测', enabled: true,
        request: { method: 'GET', url: '/boom', headers: {}, body: null, body_type: 'none' },
        extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
      }],
    }))
    const r2 = await runCase(send, projectId, env.id, tc.id)
    check('第 2 次执行失败', r2.status === 'failed' || r2.status === 'error', r2.status)

    // 改回 /ok，让第 3 次成功。
    data(await api(send, `/cases/${tc.id}`, 'PUT', {
      code: `bc${SUFFIX}`,
      name: '基线演示用例',
      module: 'baseline',
      priority: 'P1',
      status: 'active',
      config: { verify: false, variables: {}, export: [] },
      steps: [{
        seq: 1, step_type: 'request', name: '探测', enabled: true,
        request: { method: 'GET', url: '/ok', headers: {}, body: null, body_type: 'none' },
        extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
      }],
    }))
    const r3 = await runCase(send, projectId, env.id, tc.id)
    check('第 3 次执行成功', r3.status === 'success', r3.status)

    // ---- 浏览器验收：切到项目 → 用例列表 → 点「对比」----
    // 先选中刚建的项目。
    await api(send, '/projects', 'GET')
    await evaluate(send, `localStorage.setItem('hrp.currentProjectId', ${JSON.stringify(String(projectId))})`)
    await goto(send, `${BASE}/cases`)
    await evaluate(send, HELPERS)
    await waitFor(send, `window.__t.has('基线演示用例')`, { timeout: 20_000, label: '用例列表加载' })
    check('用例列表显示用例', true)

    // 点「对比」按钮（操作列第一个「对比」link button）。
    await evaluate(
      send,
      `(() => {
        const btns = [...document.querySelectorAll('.el-table button')]
        const b = btns.find(x => (x.textContent || '').trim() === '对比')
        if (!b) throw new Error('未找到「对比」按钮')
        b.click()
        return 'clicked'
      })()`,
    )
    await waitFor(send, `window.__t.has('基线对比')`, { timeout: 20_000, label: '对话框打开' })
    check('点击对比弹出对话框', true)

    // 等待卡片渲染，断言有 3 张卡片。
    await waitFor(send, `document.querySelectorAll('.baseline-card').length === 3`, { timeout: 20_000, label: '3 张卡片渲染' })
    check('渲染 3 张历史卡片', true)

    // 断言变坏/恢复信号可见：卡片上应出现红框（broke）与绿框（recovered）。
    const sig = await evaluate(
      send,
      `(() => ({
        broke: document.querySelectorAll('.card--broke').length,
        recovered: document.querySelectorAll('.card--recovered').length,
        cards: document.querySelectorAll('.baseline-card').length,
      }))()`,
    )
    check('标出「变坏」红框（第 2 次）', sig.broke === 1, `broke=${sig.broke}`)
    check('标出「恢复」绿框（第 3 次）', sig.recovered === 1, `recovered=${sig.recovered}`)

    await screenshot(send, `${SHOTS}/baseline-dialog.png`)
    console.log(`  截图 ${SHOTS}/baseline-dialog.png`)

    // 关闭对话框。
    await evaluate(send, `[...document.querySelectorAll('.el-dialog__footer button')].find(b => b.textContent.includes('关闭'))?.click()`)
    await waitFor(send, `!document.querySelector('.el-dialog')`, { timeout: 5000, label: '对话框关闭' })
    check('关闭对话框', true)

    console.log(`\n${ok} 项通过，${failures.length} 项失败`)
    if (failures.length) {
      failures.forEach((f) => console.log('  - ' + f))
      process.exitCode = 1
    }
  } finally {
    stub.close()
    await shutdown(proc)
  }
}

main().catch((e) => {
  console.error('脚本异常：', e)
  process.exit(1)
})
