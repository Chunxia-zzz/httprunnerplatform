/**
 * 统计看板（M4-a）浏览器端到端验收（CDP 驱动）。
 *
 * 用法：
 *   HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/stats-smoke.mjs
 *
 * 前置：后端（8127）+ 前端 dev server（5173）在跑；桩服务由本脚本自起。
 *
 * 验收点（对应 M4 验收标准 ①「能看出趋势与不稳定用例排行」）：
 *   1. 表单登录 → 浏览器侧造 1 成功 + 1 失败两次执行，制造「不稳定」数据
 *   2. 打开 /stats 页 → 三个 ECharts canvas 都渲染
 *   3. 页面含「通过率趋势」「不稳定 / 常败用例」「慢用例排行」三个标题
 *   4. 切换到「近 7 天」仍正常（无报错、canvas 还在）
 *
 * 关键：所有 API 调用都走浏览器内的 fetch（evaluate），登录态 cookie 天然一致，
 * 避免 node 侧 fetch 与浏览器会话分离导致的 cookie 不同步。
 */
import { mkdirSync } from 'node:fs'
import http from 'node:http'
import { connect, evaluate, goto, launchChrome, screenshot, shutdown, waitFor } from './cdp.mjs'

const BASE = process.env.HRP_UI_BASE || 'http://127.0.0.1:5173'
const SHOTS = process.env.HRP_UI_SHOTS || 'F:/httprunnerplatform-tools/tmp/ui-shots'
const STUB_PORT = 8134

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
  canvases() { return document.querySelectorAll('canvas').length },
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

// 桩：/ok 返回 200，/boom 返回 500（制造失败）。
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

/** 浏览器侧 fetch：登录态 cookie 自动带上，与页面会话一致。 */
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

    // ---- 表单登录 ----
    await formLogin(send, ADMIN.user, ADMIN.pass)
    check('表单登录成功', true)

    // ---- 造数据：项目 + 环境 + 用例 ----
    const proj = data(await api(send, '/projects', 'POST', { code: `st${SUFFIX}`, name: `统计验收 ${SUFFIX}` }))
    const projectId = proj.id
    check('建项目成功', !!projectId)

    const env = data(await api(send, `/projects/${projectId}/environments`, 'POST', {
      name: '桩环境', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
    }))
    check('建环境成功', !!env.id)

    const tc = data(await api(send, `/projects/${projectId}/cases`, 'POST', {
      code: `tc${SUFFIX}`,
      name: '不稳定演示用例',
      module: 'stats',
      priority: 'P1',
      status: 'active',
      config: { verify: false, variables: {}, export: [] },
      steps: [
        {
          seq: 1, step_type: 'request', name: '探测', enabled: true,
          request: { method: 'GET', url: '/ok', headers: {}, body: null, body_type: 'none' },
          extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
        },
      ],
    }))
    const tcFail = data(await api(send, `/projects/${projectId}/cases`, 'POST', {
      code: `tf${SUFFIX}`,
      name: '不稳定演示用例B',
      module: 'stats',
      priority: 'P1',
      status: 'active',
      config: { verify: false, variables: {}, export: [] },
      steps: [
        {
          seq: 1, step_type: 'request', name: '探测失败', enabled: true,
          request: { method: 'GET', url: '/boom', headers: {}, body: null, body_type: 'none' },
          extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
        },
      ],
    }))
    check('建两个用例成功', !!tc.id && !!tcFail.id)

    console.log('  跑 2 次执行（1 成功 + 1 失败）…')
    const r1 = await runCase(send, projectId, env.id, tc.id)
    check('第一次执行成功', r1.status === 'success', r1.status)
    const r2 = await runCase(send, projectId, env.id, tcFail.id)
    check('第二次执行失败', r2.status === 'failed' || r2.status === 'error', r2.status)

    // ---- 浏览器验收：切项目 + 打开统计看板 ----
    await evaluate(
      send,
      `(() => { localStorage.setItem('hrp.currentProjectId', String(${projectId})); return 'ok' })()`,
    )
    await goto(send, `${BASE}/stats`)
    await evaluate(send, HELPERS)
    await waitFor(send, `window.__t.has('通过率趋势')`, { timeout: 20_000, label: '统计看板加载' })

    check('页面含「通过率趋势」标题', await evaluate(send, `window.__t.has('通过率趋势')`))
    check('页面含「不稳定 / 常败用例」标题', await evaluate(send, `window.__t.has('不稳定')`))
    check('页面含「慢用例排行」标题', await evaluate(send, `window.__t.has('慢用例排行')`))

    // 等 ECharts 渲染 canvas
    await sleep(1800)
    const canvasCount = await evaluate(send, `window.__t.canvases()`)
    check('三个图表 canvas 已渲染（>=3）', canvasCount >= 3, `实际 ${canvasCount} 个 canvas`)

    mkdirSync(SHOTS, { recursive: true })
    await screenshot(send, `${SHOTS}/stats-dashboard-${SUFFIX}.png`)

    // 切时间范围
    await evaluate(
      send,
      `(() => {
        const r = [...document.querySelectorAll('.el-radio-button')].find(b => b.innerText.includes('近 7 天'))
        if (r) { r.querySelector('input').click(); return 'clicked' }
        return 'not-found'
      })()`,
    )
    await sleep(1200)
    const canvasAfter = await evaluate(send, `window.__t.canvases()`)
    check('切换「近 7 天」后图表仍在', canvasAfter >= 3, `实际 ${canvasAfter}`)
    await screenshot(send, `${SHOTS}/stats-dashboard-7d-${SUFFIX}.png`)
  } finally {
    stub.close()
    await shutdown(proc)
  }

  console.log(`\n结果：${ok} 通过，${failures.length} 失败`)
  if (failures.length) {
    console.log('失败项：')
    failures.forEach((f) => console.log(`  - ${f}`))
    process.exit(1)
  }
}

main().catch((e) => {
  console.error('脚本异常：', e)
  process.exit(1)
})
