/**
 * ③-c（断言配置器 + 源码可编辑 + 变量依赖提示）的浏览器端到端验收（CDP）。
 *
 * 用法：
 *   HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/m3c-smoke.mjs
 *
 * 前置：
 *   1. 后端已在 8127 运行（含 SaveYAML 端点）；
 *   2. 前端 dev server 已起（代理指向后端）；
 *   3. 桩服务 8134（/echo 回显）。
 *
 * 覆盖三条用户路径：
 *   1. 断言配置器：步骤里「断言」tab 有校验方法下拉 + 期望值类型选择；
 *   2. 源码可编辑：YAML tab → 编辑源码 → 保存 → 表单同步；
 *   3. 变量依赖：第 2 步引用未定义的 $token → 步骤卡片出现红色「未定义变量」提示。
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
  if (cond) { ok++; console.log(`  ✅ ${name}`); }
  else { failures.push(name); console.log(`  ❌ ${name}${extra ? ` — ${extra}` : ''}`); }
}
function sleep(ms) { return new Promise((r) => setTimeout(r, ms)); }

function startStub() {
  return new Promise((resolve) => {
    const srv = http.createServer((req, res) => {
      const body = JSON.stringify({ code: 0, path: req.url });
      res.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) });
      res.end(body);
    });
    srv.listen(STUB_PORT, '127.0.0.1', () => resolve(srv));
  });
}

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
  tabs() { return [...document.querySelectorAll('.el-tabs__item')].map(t => this.text(t)) },
  clickTab(label) {
    const t = [...document.querySelectorAll('.el-tabs__item')].find(x => this.text(x) === label)
    if (!t) throw new Error('tab 不存在: ' + label)
    t.click(); return 'ok'
  },
  btns() { return [...document.querySelectorAll('button')].map(b => this.text(b)) },
  clickBtn(label) {
    const b = [...document.querySelectorAll('button')].find(x => this.text(x) === label)
    if (!b) throw new Error('按钮不存在: ' + label)
    b.click(); return 'ok'
  },
}
'ok'
`

async function gotoPage(send, path, waitText) {
  await goto(send, `${BASE}${path}`)
  await evaluate(send, HELPERS)
  if (waitText) await waitFor(send, `window.__t.has(${JSON.stringify(waitText)})`, { timeout: 20_000, label: `等待「${waitText}」` })
}
async function shard(send, name) { await sleep(600); mkdirSync(SHOTS, { recursive: true }); await screenshot(send, `${SHOTS}/${name}.png`) }
async function useCookie(send, value) {
  await send('Network.clearBrowserCookies')
  if (value) await send('Network.setCookie', { name: 'hrp_session', value, url: BASE, path: '/', httpOnly: true })
}
async function formLogin(send, user, pass) {
  await useCookie(send, null)
  await gotoPage(send, '/login', '接口自动化测试平台')
  await evaluate(send, `(() => {
    const inputs = [...document.querySelectorAll('.login__card input')]
    window.__t.setInput(inputs[0], ${JSON.stringify(user)})
    window.__t.setInput(inputs[1], ${JSON.stringify(pass)})
    document.querySelector('.login__submit').click(); return 'clicked'
  })()`)
  await waitFor(send, `location.pathname !== '/login'`, { timeout: 20_000, label: '登录跳转' })
  await evaluate(send, HELPERS)
}
async function api(send, pathname, method = 'GET', payload) {
  return await evaluate(send, `fetch('/api/v1${pathname}', {
    method: ${JSON.stringify(method)}, headers: { 'Content-Type': 'application/json' },
    body: ${payload === undefined ? 'undefined' : JSON.stringify(JSON.stringify(payload))},
  }).then(async r => ({ http: r.status, body: await r.json().catch(() => null) }))`)
}
async function pickProject(send, projectId, code) {
  await evaluate(send, `(() => { localStorage.setItem('hrp.currentProjectId', String(${projectId})); location.reload(); return 'ok' })()`)
  await waitFor(send, `document.querySelector('#app')?.children.length > 0 && location.pathname !== '/login'`, { timeout: 20_000, label: '刷新后回到主布局' })
  await evaluate(send, HELPERS)
  await waitFor(send, `(document.querySelector('.layout__header-left')?.innerText || '').includes(${JSON.stringify(code)})`, { timeout: 15_000, label: '顶栏显示目标项目' })
}

const { proc } = await launchChrome()
const { send } = await connect()
await send('Network.enable')
const stub = await startStub()

try {
  console.log(`\n靶子 ${BASE}    后缀 ${SUFFIX}\n`)

  // =========================================================================
  console.log('STEP 1  登录 + 造数据（用例：第 1 步提取 token，第 2 步引用 $token）')
  // =========================================================================
  await formLogin(send, ADMIN.user, ADMIN.pass)
  const proj = await api(send, '/projects', 'POST', { code: `m3c${SUFFIX}`, name: `M3c验收项目 ${SUFFIX}` })
  check('建项目成功', proj.body?.code === 0)
  const projectId = proj.body.data.id
  const env = await api(send, `/projects/${projectId}/environments`, 'POST', {
    name: '本地桩', base_url: `http://127.0.0.1:${STUB_PORT}`, variables: {}, is_default: true,
  })
  check('建环境成功', env.body?.code === 0)

  // 用例：第 1 步提取 token（提供变量），第 2 步引用 $token + $未定义变量
  const tc = await api(send, `/projects/${projectId}/cases`, 'POST', {
    code: `tc${SUFFIX}`, name: '变量依赖用例', module: 'm3c', priority: 'P0', status: 'active',
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
        request: { method: 'GET', url: '/user?t=$token&u=$undef_var', headers: {}, body: null, body_type: 'none' },
        extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
      },
    ],
  })
  check('建用例成功', tc.body?.code === 0, JSON.stringify(tc.body))
  const caseId = tc.body.data.id
  await pickProject(send, projectId, `m3c${SUFFIX}`)

  // =========================================================================
  console.log('\nSTEP 2  断言配置器：步骤「断言」tab 有校验方法下拉')
  // =========================================================================
  await gotoPage(send, `/cases/${caseId}/edit`, '编辑用例')
  await waitFor(send, `document.querySelectorAll('.step').length === 2`, { timeout: 20_000, label: '两个步骤卡片' })
  // 第 1 步的断言 tab
  await evaluate(send, `(() => {
    const step = document.querySelectorAll('.step')[0]
    const tabs = [...step.querySelectorAll('.el-tabs__item')].map(t => window.__t.text(t))
    const assertTab = [...step.querySelectorAll('.el-tabs__item')].find(t => window.__t.text(t) === '断言（Validate）')
    if (assertTab) assertTab.click()
    return 'ok'
  })()`)
  await sleep(500)
  const step1Text = await evaluate(send, `window.__t.text(document.querySelectorAll('.step')[0])`)
  check('断言 tab 有校验方法下拉（eq）', step1Text.includes('eq'), step1Text.slice(0, 300))
  check('断言有类型选择（数字）', step1Text.includes('数字'), step1Text.slice(0, 300))
  await shard(send, '01-assert-configurator')

  // =========================================================================
  console.log('\nSTEP 3  变量依赖提示：第 2 步引用未定义 $undef_var 标红')
  // =========================================================================
  const step2Text = await evaluate(send, `window.__t.text(document.querySelectorAll('.step')[1])`)
  check('第 2 步出现「未定义变量」提示', step2Text.includes('引用了未定义的变量'), step2Text.slice(0, 400))
  check('提示点名 $undef_var', step2Text.includes('undef_var'), step2Text.slice(0, 400))
  // $token 已定义（第 1 步 extract），不应出现在未定义列表里
  const hasTokenUndef = await evaluate(send, `(() => {
    const step = document.querySelectorAll('.step')[1]
    const tags = [...step.querySelectorAll('.undef-tag')].map(t => window.__t.text(t))
    return tags.some(t => t.includes('token'))
  })()`)
  check('$token 不误报（第 1 步已提取）', hasTokenUndef === false)
  await shard(send, '02-undefined-var')

  // =========================================================================
  console.log('\nSTEP 4  源码可编辑：YAML tab → 编辑 → 保存 → 表单同步')
  // =========================================================================
  await evaluate(send, `(() => {
    // 右侧边栏的 YAML tab（用「编译后 YAML」标题定位）
    const side = document.querySelector('.grid__side')
    const tabs = [...side.querySelectorAll('.el-tabs__item')]
    const yt = tabs.find(t => window.__t.text(t) === '编译后 YAML')
    if (yt) yt.click()
    return 'ok'
  })()`)
  await sleep(500)
  await waitFor(send, `window.__t.has('编辑源码')`, { timeout: 15_000, label: '出现「编辑源码」按钮' })
  await evaluate(send, `window.__t.clickBtn('编辑源码')`)
  await sleep(400)

  // textarea 里改 name
  await evaluate(send, `(() => {
    const ta = document.querySelector('.yaml__editor')
    if (!ta) throw new Error('没有找到 YAML 编辑器 textarea')
    const v = ta.value.replace('name: 变量依赖用例', 'name: 变量依赖用例_改')
    window.__t.setInput(ta, v)
    return 'ok'
  })()`)
  await evaluate(send, `window.__t.clickBtn('保存源码')`)
  await waitFor(send, `!window.__t.has('保存源码') || window.__t.has('源码已保存')`, { timeout: 15_000, label: '保存源码完成' })
  await sleep(600)
  const afterSave = await evaluate(send, `window.__t.body()`)
  check('保存后表单 name 已同步为「_改」', afterSave.includes('变量依赖用例_改'), afterSave.slice(0, 500))
  await shard(send, '03-source-edited')

  console.log(`\n===== ${ok} 项通过，${failures.length} 项失败 =====`)
  if (failures.length) { failures.forEach((f) => console.log('  - ' + f)); process.exitCode = 1 }
} catch (e) {
  console.error('\n💥 异常中止：', e)
  process.exitCode = 1
} finally {
  stub.close()
  await shutdown(proc, send)
}
