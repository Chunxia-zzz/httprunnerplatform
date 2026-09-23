/**
 * 参数化数据集（M3 ③-b）的浏览器端到端验收（CDP 驱动，零依赖）。
 *
 * 用法：
 *   HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/param-smoke.mjs
 *
 * 前置：
 *   1. 后端已在 HRP_UI_BASE 的 /api 代理目标上运行（默认 127.0.0.1:8127）；
 *   2. 前端 dev server 已起（代理指向后端）；
 *   3. 被测桩服务（/echo 回显）在 8133。
 *
 * 为什么必须走浏览器：参数化是 M3 的**交互式**能力，接口全绿不代表
 * 「参数集页新建数据集 → 用例编辑器勾选 → 编译后 YAML 出现 ${P()} 引用」
 * 这条用户真正会走的路径是通的。
 */
import { mkdirSync } from 'node:fs'
import http from 'node:http'
import { connect, evaluate, goto, launchChrome, screenshot, shutdown, waitFor } from './cdp.mjs'

const BASE = process.env.HRP_UI_BASE || 'http://127.0.0.1:5173'
const SHOTS = process.env.HRP_UI_SHOTS || 'F:/httprunnerplatform-tools/tmp/ui-shots'
const STUB_PORT = 8133

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

// 桩服务：/echo 回显查询参数（供参数化用例执行用）。
function startStub() {
  return new Promise((resolve) => {
    const srv = http.createServer((req, res) => {
      const body = JSON.stringify({ code: 0, path: req.url })
      res.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) })
      res.end(body)
    })
    srv.listen(STUB_PORT, '127.0.0.1', () => resolve(srv))
  })
}

// 与 auth-smoke / debug-smoke 共用的页面内助手。
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
  // 所有 tab 标题
  tabs() { return [...document.querySelectorAll('.el-tabs__item')].map(t => this.text(t)) },
  // 点击某个 tab
  clickTab(label) {
    const t = [...document.querySelectorAll('.el-tabs__item')].find(x => this.text(x) === label)
    if (!t) throw new Error('tab 不存在: ' + label)
    t.click()
    return 'ok'
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
const stub = await startStub()

try {
  console.log(`\n靶子 ${BASE}    后缀 ${SUFFIX}\n`)

  // =========================================================================
  console.log('STEP 1  登录 + 造前置数据（项目/环境/CSV 数据集/引用它的用例）')
  // =========================================================================
  await formLogin(send, ADMIN.user, ADMIN.pass)

  const proj = await api(send, '/projects', 'POST', { code: `prm${SUFFIX}`, name: `参数化UI验收项目 ${SUFFIX}` })
  check('建项目成功', proj.body?.code === 0, JSON.stringify(proj.body))
  const projectId = proj.body.data.id

  const env = await api(send, `/projects/${projectId}/environments`, 'POST', {
    name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
  })
  check('建环境成功', env.body?.code === 0)

  // CSV 数据集：3 行（username,password）
  const ds = await api(send, `/projects/${projectId}/datasets`, 'POST', {
    name: '登录数据', source: 'csv', csv_name: 'login.csv',
    csv_text: 'username,password\nalice,pw1\nbob,pw2\ncarol,pw3\n',
    strategy: 'sequential', limit: 0,
  })
  check('建 CSV 数据集成功', ds.body?.code === 0, JSON.stringify(ds.body))

  // 引用该数据集的用例：一个 GET 步骤，用 $username 变量
  const tc = await api(send, `/projects/${projectId}/cases`, 'POST', {
    code: `tc${SUFFIX}`,
    name: '参数化用例',
    module: 'prm',
    priority: 'P0',
    status: 'active',
    config: { verify: false, variables: {}, export: [], datasets: [{ name: '登录数据', limit: 0 }] },
    steps: [
      {
        seq: 1, step_type: 'request', name: '回显', enabled: true,
        request: { method: 'GET', url: '/echo?u=$username', headers: {}, body: null, body_type: 'none' },
        extract: [],
        validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
      },
    ],
  })
  check('建引用数据集的用例成功', tc.body?.code === 0, JSON.stringify(tc.body))
  const caseId = tc.body.data.id

  await pickProject(send, projectId, `prm${SUFFIX}`)

  // =========================================================================
  console.log('\nSTEP 2  参数化数据集页：看到列表 + 行数 + 实际迭代')
  // =========================================================================
  await gotoPage(send, '/datasets', '参数化数据集')
  await waitFor(send, `window.__t.has('登录数据')`, { timeout: 20_000, label: '列表出现数据集' })

  const dsPageText = await evaluate(send, `window.__t.body()`)
  check('列表显示数据集名「登录数据」', dsPageText.includes('登录数据'))
  check('列表显示来源 CSV', dsPageText.includes('CSV'))
  check('列表显示数据行数 3', /登录数据[\s\S]{0,120}3/.test(dsPageText), dsPageText.slice(0, 400))
  check('列表显示实际迭代 3 次', dsPageText.includes('3 次'))
  await shard(send, '01-param-list')

  // =========================================================================
  console.log('\nSTEP 3  用例编辑器：数据集 tab 勾选已回填')
  // =========================================================================
  await gotoPage(send, `/cases/${caseId}/edit`, '编辑用例')
  await evaluate(send, `window.__t.clickTab('数据集')`)
  await sleep(400)

  const dsTabText = await evaluate(send, `window.__t.body()`)
  check('数据集 tab 显示已勾选「登录数据」', dsTabText.includes('登录数据'))
  check('数据集 tab 显示 3 行 · 实际 3 次', dsTabText.includes('3 行') && dsTabText.includes('3 次'))
  await shard(send, '02-editor-dataset-checked')

  // =========================================================================
  console.log('\nSTEP 4  编译后 YAML 出现 ${P()} 引用')
  // =========================================================================
  await evaluate(send, `window.__t.clickTab('编译后 YAML')`)
  await sleep(600)
  await waitFor(send, `window.__t.has('P(data/')`, { timeout: 15_000, label: 'YAML 出现 P 函数引用' })
  const yamlText = await evaluate(send, `window.__t.body()`)
  check('YAML 含 ${P(data/login.csv)} 引用', yamlText.includes('${P(data/login.csv)}'), yamlText.slice(0, 500))
  check('YAML 不含平台保留键 datasets', !yamlText.includes('datasets:'))
  await shard(send, '03-yaml-param-ref')

  // =========================================================================
  console.log('\nSTEP 5  真实执行：CSV 3 行 → 3 次迭代（对账 step_total）')
  // =========================================================================
  const run = await api(send, '/runs', 'POST', {
    project_id: projectId, target_type: 'case', target_id: caseId, env_id: env.body.data.id, options: {},
  })
  check('启动执行成功', run.body?.code === 0, JSON.stringify(run.body))
  const runId = run.body.data.run_id

  let cases = null
  for (let i = 0; i < 40; i++) {
    await sleep(500)
    const d = await api(send, `/runs/${runId}`)
    if (d.body?.code === 0) {
      const status = d.body.data?.run?.status
      cases = d.body.data?.cases
      if (['success', 'failed', 'error', 'canceled'].includes(status)) break
    }
  }
  const case0 = cases?.[0]
  check('执行完成且成功', case0?.status === 'pass', JSON.stringify(case0))
  check('step_total = 3（3 行数据全迭代）', case0?.step_total === 3, `实际 ${case0?.step_total}`)
  check('step_passed = 3', case0?.step_passed === 3, `实际 ${case0?.step_passed}`)

  console.log(`\n===== ${ok} 项通过，${failures.length} 项失败 =====`)
  if (failures.length) {
    console.log('失败项：')
    failures.forEach((f) => console.log('  - ' + f))
    process.exitCode = 1
  }
} catch (e) {
  console.error('\n💥 异常中止：', e)
  process.exitCode = 1
} finally {
  stub.close()
  await shutdown(proc, send)
}
