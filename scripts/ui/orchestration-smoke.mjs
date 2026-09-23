/**
 * 编排层三个页面的浏览器端到端验收（CDP 驱动，零依赖）。
 *
 * 用法：
 *   HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/orchestration-smoke.mjs
 *
 * 前置：
 *   1. 后端在 HRP_UI_BASE 的 /api 代理目标上运行（本仓库 dev 时 HRP_API_TARGET 指 8127）；
 *   2. 前端 dev server 或后端内嵌前端任一可用；
 *   3. 数据库里有种子管理员 admin/admin123。
 *
 * 为什么这三页必须走浏览器：
 *   编排层后端三片（用例集执行 / 测试计划 / CI 令牌）此前接口冒烟全绿，
 *   但这三页上真正要紧的东西**只在页面上才看得出来**——
 *   ① 用例集成员抽屉里「勾选顺序即执行顺序」的 ↑↓ 调序；
 *   ② 测试计划列表把 cron / 时区 / next_fire_at 同列展示、跳过留痕；
 *   ③ 令牌明文只在签发弹窗出现一次、关窗前二次确认。
 *   接口全绿但页面交互断了（点不动、顺序没生效、明文漏了），只有这条路能发现。
 *
 * 前置数据（项目/环境/用例）用页面内 fetch 直接打接口造 —— 它们是 M1 已验收的
 * 能力，不是本轮的验收对象；本轮的验收对象是三页的交互。
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

// ---------------------------------------------------------------------------
// 页面内助手。整页导航会清掉执行上下文，每次 goto 后都要重新注入。
// ---------------------------------------------------------------------------
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
  field(root, label) {
    if (!root) return null
    for (const item of root.querySelectorAll('.el-form-item')) {
      const l = this.text(item.querySelector('.el-form-item__label')).replace(/\\*/g, '').trim()
      if (l === label) return item.querySelector('input, textarea')
    }
    return null
  },
  row(text) {
    return [...document.querySelectorAll('.el-table__body tr')]
      .find(tr => this.text(tr).includes(text)) || null
  },
  rowCells(text) {
    const tr = this.row(text)
    return tr ? [...tr.querySelectorAll('td')].map(td => this.norm(td.innerText)) : null
  },
  btn(root, label) {
    if (!root) return null
    return [...root.querySelectorAll('button')].find(b => this.text(b) === label) || null
  },
  // 侧边栏菜单项是否可见
  hasMenu(label) {
    return [...document.querySelectorAll('.el-menu-item')].some(i => this.text(i) === label)
  },
  switchOn(root) { const s = root && root.querySelector('.el-switch'); return !!s && s.classList.contains('is-checked') },
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

async function body(send) {
  return await evaluate(send, 'window.__t.body()')
}
async function path(send) {
  return await evaluate(send, 'location.pathname')
}

async function shard(send, name) {
  await sleep(600)
  mkdirSync(SHOTS, { recursive: true })
  await screenshot(send, `${SHOTS}/${name}.png`)
}

async function useCookie(send, value) {
  await send('Network.clearBrowserCookies')
  if (value) {
    await send('Network.setCookie', {
      name: 'hrp_session', value, url: BASE, path: '/', httpOnly: true,
    })
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

/** 页面内 fetch 调后端接口。返回 { http, body }。 */
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

/** 切顶栏项目。项目 store 把 currentId 存在 localStorage（hrp.currentProjectId），
 * 直接写它再刷新，比点 el-select 下拉可靠 —— 下拉选项里混着分页/筛选的 dropdown，
 * 而且当前项目可能已经是目标项目（点了等于没切）。 */
async function pickProject(send, projectId) {
  await evaluate(
    send,
    `(() => { localStorage.setItem('hrp.currentProjectId', String(${projectId})); location.reload(); return 'ok' })()`,
  )
  // 刷新后等主布局挂载完成
  await waitFor(send, `document.querySelector('#app')?.children.length > 0 && location.pathname !== '/login'`, {
    timeout: 20_000,
    label: '刷新后回到主布局',
  })
  await evaluate(send, HELPERS)
  // 顶栏应显示当前项目（含 code 后缀），等它渲染
  await waitFor(
    send,
    `(document.querySelector('.layout__header-left')?.innerText || '').includes(${JSON.stringify(`or${SUFFIX}`)})`,
    { timeout: 15_000, label: '顶栏显示目标项目' },
  )
}

/** 点 ElMessageBox 确认按钮。 */
async function confirmBox(send, buttonText) {
  await waitFor(send, `!!document.querySelector('.el-message-box')`, { timeout: 10_000, label: '等待确认框' })
  await evaluate(
    send,
    `(() => {
      const box = document.querySelector('.el-message-box')
      const btns = [...box.querySelectorAll('button')]
      const b = btns.find(x => window.__t.norm(x.innerText) === ${JSON.stringify(buttonText)})
        || box.querySelector('.el-button--primary')
      b.click()
      return window.__t.norm(b.innerText)
    })()`,
  )
  await waitFor(send, `!document.querySelector('.el-message-box')`, { timeout: 10_000, label: '确认框关闭' })
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------
const { proc } = await launchChrome()
const { send } = await connect()
await send('Network.enable')

// 造前置数据用的状态
const P = { projectId: 0, envId: 0, caseIds: [] }

try {
  console.log(`\n靶子 ${BASE}    后缀 ${SUFFIX}\n`)

  // =========================================================================
  console.log('STEP 1  登录 + 前置数据（项目/环境/3 条用例）')
  // =========================================================================
  await formLogin(send, ADMIN.user, ADMIN.pass)
  check('管理员登录成功', (await path(send)) === '/cases')
  check('侧边栏有「用例集」', await evaluate(send, `window.__t.hasMenu('用例集')`))
  check('侧边栏有「测试计划」', await evaluate(send, `window.__t.hasMenu('测试计划')`))
  check('侧边栏有「CI 令牌」', await evaluate(send, `window.__t.hasMenu('CI 令牌')`))

  const proj = await api(send, '/projects', 'POST', { code: `or${SUFFIX}`, name: `编排验收项目 ${SUFFIX}` })
  check('建项目成功', proj.body.code === 0, JSON.stringify(proj.body))
  P.projectId = proj.body.data.id

  const env = await api(send, `/projects/${P.projectId}/environments`, 'POST', {
    name: '本地桩', base_url: 'http://127.0.0.1:8133', variables: {}, is_default: true,
  })
  check('建环境成功', env.body.code === 0, JSON.stringify(env.body))
  P.envId = env.body.data.id

  const CASE_CODES = { a: `a${SUFFIX}`, b: `b${SUFFIX}`, c: `c${SUFFIX}` }
  const caseBody = (tag) => ({
    code: CASE_CODES[tag],
    name: `用例 ${tag}`,
    module: 'or',
    priority: 'P0',
    status: 'active',
    config: { verify: false, variables: {}, export: [] },
    steps: [{
      seq: 1, step_type: 'request', name: '请求', enabled: true,
      request: { method: 'GET', url: '/echo', headers: {}, body: null, body_type: 'none' },
      extract: [], validate: [{ check: 'status_code', assert: 'eq', expect: 200 }],
    }],
  })
  for (const tag of ['a', 'b', 'c']) {
    const c = await api(send, `/projects/${P.projectId}/cases`, 'POST', caseBody(tag))
    check(`建用例 ${tag} 成功`, c.body.code === 0, JSON.stringify(c.body))
    P.caseIds.push(c.body.data.id)
  }

  // 切到当前项目（写 localStorage + 刷新，最可靠）
  await pickProject(send, P.projectId)
  await shard(send, '01-project-ready')

  // =========================================================================
  console.log('\nSTEP 2  用例集页：新建 + 成员勾选排序 + 执行')
  // =========================================================================
  await gotoPage(send, '/suites', '用例集管理')
  check('进入用例集页', (await path(send)) === '/suites')

  await evaluate(send, `window.__t.btn(document, '新建用例集').click(), 'ok'`)
  await waitFor(send, `!!window.__t.dlg('新建用例集')`, { timeout: 10_000, label: '新建用例集弹窗' })
  const suiteCode = `su${SUFFIX}`
  await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('新建用例集')
      window.__t.setInput(window.__t.field(d, '标识'), ${JSON.stringify(suiteCode)})
      window.__t.setInput(window.__t.field(d, '名称'), '编排验收用例集')
      window.__t.btn(d, '保存').click()
      return 'ok'
    })()`,
  )
  await waitFor(
    send,
    `[...document.querySelectorAll('.el-table__body tr')].some(tr => window.__t.text(tr).includes(${JSON.stringify(suiteCode)}))`,
    { timeout: 20_000, label: '用例集出现在列表' },
  )
  check('用例集创建成功并出现在列表', true)

  // 打开成员抽屉，勾选 3 条用例（顺序即执行顺序）
  await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(suiteCode)})
      window.__t.btn(tr, '成员').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!!document.querySelector('.el-drawer')`, { timeout: 10_000, label: '成员抽屉打开' })
  await shard(send, '02-suite-member-drawer')

  // 逐个勾选：先 a、再 c、再 b —— 验证顺序能偏离默认 ID 序
  const memberOrder = [CASE_CODES.a, CASE_CODES.c, CASE_CODES.b]
  const pickSelectOption = async (caseCode) => {
    await evaluate(send, `document.querySelector('.el-drawer .el-select__wrapper').click(), 'ok'`)
    await waitFor(send, `!!document.querySelector('.el-select-dropdown')`, { timeout: 10_000, label: '成员下拉' })
    await evaluate(
      send,
      `(() => {
        const opt = [...document.querySelectorAll('.el-select-dropdown__item')]
          .find(o => window.__t.text(o).includes(${JSON.stringify(caseCode)}))
        opt.click()
        return 'ok'
      })()`,
    )
    await sleep(300)
  }
  for (const code of memberOrder) {
    await pickSelectOption(code)
  }
  // 关闭下拉
  await evaluate(send, `document.body.click(), 'ok'`)

  // 成员表里应有 3 条，且顺序是 a/c/b（勾选顺序）
  const memberRows = await evaluate(
    send,
    `[...document.querySelectorAll('.el-drawer .el-table__body tr')].map(tr => window.__t.text(tr))`,
  )
  check('成员抽屉里勾了 3 条用例', memberRows.length === 3, JSON.stringify(memberRows))

  // 验证顺序即勾选顺序：第 1 条是 a，第 3 条是 b（我们按 a/c/b 勾的）
  const firstIsA = memberRows[0] && memberRows[0].includes(`a${SUFFIX}`)
  const thirdIsB = memberRows[2] && memberRows[2].includes(`b${SUFFIX}`)
  check('⭐ 成员顺序即勾选顺序（a/c/b，而非 ID 序）', firstIsA && thirdIsB, JSON.stringify(memberRows))

  // ↑↓ 调序：把第 3 条（b）上移到第 2 位
  await evaluate(
    send,
    `(() => {
      const rows = [...document.querySelectorAll('.el-drawer .el-table__body tr')]
      const upBtn = [...rows[2].querySelectorAll('button')].find(b => window.__t.text(b) === '↑')
      upBtn.click()
      return 'ok'
    })()`,
  )
  await sleep(400)
  const afterMove = await evaluate(
    send,
    `[...document.querySelectorAll('.el-drawer .el-table__body tr')].map(tr => window.__t.text(tr))`,
  )
  check('⭐ ↑ 调序生效（b 上移到第 2 位）', afterMove[1] && afterMove[1].includes(`b${SUFFIX}`), JSON.stringify(afterMove))
  await shard(send, '03-suite-member-ordered')

  await evaluate(send, `window.__t.btn(document.querySelector('.el-drawer'), '保存成员').click(), 'ok'`)
  await waitFor(send, `!!document.querySelector('.el-message--success')`, { timeout: 10_000, label: '成员保存成功提示' })
  check('成员保存成功', true)

  // 列表 case_count 应 = 3（保存后 load() 是异步的，需等它刷新）
  await waitFor(
    send,
    `(() => {
      const cells = window.__t.rowCells(${JSON.stringify(suiteCode)})
      return cells && cells.includes('3')
    })()`,
    { timeout: 15_000, label: '列表成员数刷新为 3' },
  )
  const suiteCells = await evaluate(send, `window.__t.rowCells(${JSON.stringify(suiteCode)})`)
  check('列表成员数 = 3', !!suiteCells && suiteCells.includes('3'), JSON.stringify(suiteCells))

  // 执行：跳转到执行详情
  await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(suiteCode)})
      window.__t.btn(tr, '执行').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `location.pathname.match(/^\\/runs\\/\\d+/)`, { timeout: 20_000, label: '执行跳详情' })
  check('⭐ 用例集执行跳转到执行详情页', true, await path(send))
  await shard(send, '04-suite-run-detail')

  // =========================================================================
  console.log('\nSTEP 3  测试计划页：新建 cron 计划 + 时区 + 开关 + 成员')
  // =========================================================================
  await gotoPage(send, '/plans', '计划 = 一组用例集')
  check('进入测试计划页', (await path(send)) === '/plans')

  await waitFor(send, `!!window.__t.btn(document, '新建计划')`, { timeout: 15_000, label: '新建计划按钮就绪' })
  await evaluate(send, `window.__t.btn(document, '新建计划').click(), 'ok'`)
  await waitFor(send, `!!window.__t.dlg('新建计划')`, { timeout: 10_000, label: '新建计划弹窗' })
  const planName = `每日回归 ${SUFFIX}`
  await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('新建计划')
      window.__t.setInput(window.__t.field(d, '名称'), ${JSON.stringify(planName)})
      // 触发方式选「定时」
      const radio = [...d.querySelectorAll('.el-radio')].find(x => window.__t.text(x).includes('定时'))
      radio.querySelector('input').click()
      return 'ok'
    })()`,
  )
  await sleep(400)
  // 点 cron 预设「工作日 09:00」
  await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('新建计划')
      const preset = [...d.querySelectorAll('button')].find(b => window.__t.text(b) === '工作日 09:00')
      preset.click()
      return 'ok'
    })()`,
  )
  await shard(send, '05-plan-cron-form')
  await evaluate(send, `window.__t.btn(window.__t.dlg('新建计划'), '保存').click(), 'ok'`)
  await waitFor(
    send,
    `[...document.querySelectorAll('.el-table__body tr')].some(tr => window.__t.text(tr).includes(${JSON.stringify(planName)}))`,
    { timeout: 20_000, label: '计划出现在列表' },
  )
  check('计划创建成功并出现在列表', true)

  const planCells = await evaluate(send, `window.__t.rowCells(${JSON.stringify(planName)})`)
  check('列表显示 cron 中文说明', !!planCells && planCells.some((c) => c.includes('工作日')), JSON.stringify(planCells))
  check('列表显示时区', !!planCells && planCells.some((c) => c.includes('Asia/Shanghai')), JSON.stringify(planCells))
  check('⭐ 列表显示下次执行时间', !!planCells && planCells.some((c) => c.includes('下次')), JSON.stringify(planCells))
  await shard(send, '06-plan-list')

  // 给计划挂用例集（成员抽屉）
  await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(planName)})
      window.__t.btn(tr, '成员').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!!document.querySelector('.el-drawer')`, { timeout: 10_000, label: '计划成员抽屉' })
  await evaluate(send, `document.querySelector('.el-drawer .el-select__wrapper').click(), 'ok'`)
  await waitFor(send, `!!document.querySelector('.el-select-dropdown')`, { timeout: 10_000, label: '计划成员下拉' })
  await evaluate(
    send,
    `(() => {
      const opt = [...document.querySelectorAll('.el-select-dropdown__item')]
        .find(o => window.__t.text(o).includes(${JSON.stringify(suiteCode)}))
      opt.click()
      return 'ok'
    })()`,
  )
  await sleep(300)
  await evaluate(send, `document.body.click(), 'ok'`)
  await evaluate(send, `window.__t.btn(document.querySelector('.el-drawer'), '保存成员').click(), 'ok'`)
  await waitFor(send, `!!document.querySelector('.el-message--success')`, { timeout: 10_000, label: '计划成员保存' })
  check('计划挂用例集成功', true)

  // 开关切换（默认应关闭）
  const planSwitchOn = await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(planName)})
      return window.__t.switchOn(tr)
    })()`,
  )
  check('新建计划默认不启用', planSwitchOn === false)

  // 打开开关 → 应启用
  await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(planName)})
      tr.querySelector('.el-switch').click()
      return 'ok'
    })()`,
  )
  await waitFor(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(planName)})
      return window.__t.switchOn(tr) === true
    })()`,
    { timeout: 15_000, label: '开关变启用' },
  )
  check('⭐ 开关切换到启用', true)
  await shard(send, '07-plan-enabled')

  // =========================================================================
  console.log('\nSTEP 4  CI 令牌页：签发 → 明文只显示一次 → 关窗确认 → 吊销')
  // =========================================================================
  await gotoPage(send, '/tokens', '按项目签发')
  check('进入 CI 令牌页', (await path(send)) === '/tokens')

  await waitFor(send, `!!window.__t.btn(document, '签发令牌')`, { timeout: 15_000, label: '签发按钮就绪' })
  await evaluate(send, `window.__t.btn(document, '签发令牌').click(), 'ok'`)
  await waitFor(send, `!!window.__t.dlg('签发 CI 令牌')`, { timeout: 10_000, label: '签发弹窗' })
  const tokenName = `流水线 ${SUFFIX}`
  await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('签发 CI 令牌')
      window.__t.setInput(window.__t.field(d, '名称'), ${JSON.stringify(tokenName)})
      window.__t.btn(d, '签发').click()
      return 'ok'
    })()`,
  )
  // 签发成功后弹窗应切换到「令牌已签发」视图，明文可见
  await waitFor(send, `window.__t.has('令牌已签发')`, { timeout: 15_000, label: '签发成功视图' })
  const secretText = await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('签发 CI 令牌')
      const code = d.querySelector('.secret code')
      return code ? code.innerText.trim() : ''
    })()`,
  )
  check('⭐ 明文出现在签发弹窗里', secretText.startsWith('hrp_ci_'), secretText.slice(0, 24) + '…')
  await shard(send, '08-token-issued')

  // 关窗（未复制）应弹确认框
  await evaluate(send, `window.__t.btn(window.__t.dlg('签发 CI 令牌'), '关闭').click(), 'ok'`)
  await waitFor(send, `!!document.querySelector('.el-message-box')`, { timeout: 10_000, label: '关窗确认框' })
  check('⭐ 未复制就关窗会弹二次确认', true)
  await shard(send, '09-token-before-close')
  // 选「再看看」留在窗口
  await evaluate(
    send,
    `(() => {
      const box = document.querySelector('.el-message-box')
      const b = [...box.querySelectorAll('button')].find(x => window.__t.norm(x.innerText) === '再看看')
      b.click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!document.querySelector('.el-message-box')`, { timeout: 10_000, label: '确认框关闭' })
  check('选择「再看看」后弹窗仍在', await evaluate(send, `!!window.__t.dlg('签发 CI 令牌')`))

  // 点复制。headless Chrome 里 navigator.clipboard 默认被拒，所以先 stub 一个
  // 会成功的 clipboard —— 我们验收的是「复制成功后关窗不再弹确认」这个产品逻辑，
  // 不是 Chrome 的剪贴板权限（那是环境，不是被测对象）。
  await evaluate(
    send,
    `(() => {
      Object.defineProperty(navigator, 'clipboard', {
        value: { writeText: () => Promise.resolve() },
        configurable: true,
      })
      return 'ok'
    })()`,
  )
  await evaluate(send, `window.__t.btn(window.__t.dlg('签发 CI 令牌'), '复制').click(), 'ok'`)
  await waitFor(
    send,
    `window.__t.btn(window.__t.dlg('签发 CI 令牌'), '已复制') !== null`,
    { timeout: 10_000, label: '复制按钮变「已复制」' },
  )
  check('点复制后按钮变「已复制」', true)
  await shard(send, '09b-token-copied')

  // 再关窗 —— 已复制则直接关，不再弹确认
  await evaluate(send, `window.__t.btn(window.__t.dlg('签发 CI 令牌'), '关闭').click(), 'ok'`)
  await waitFor(send, `!window.__t.dlg('签发 CI 令牌')`, { timeout: 10_000, label: '弹窗直接关闭' })
  const stillBox = await evaluate(send, `!!document.querySelector('.el-message-box')`)
  check('⭐ 已复制后再关窗直接关闭、不再弹确认', stillBox === false)

  // 列表只显示 prefix，不含明文
  const tokenCells = await evaluate(send, `window.__t.rowCells(${JSON.stringify(tokenName)})`)
  const listHasPlaintext = await evaluate(
    send,
    `window.__t.body().includes(${JSON.stringify(secretText)})`,
  )
  check('列表里只有 prefix、没有明文', !listHasPlaintext, JSON.stringify(tokenCells))
  check('列表显示前缀', !!tokenCells && tokenCells.some((c) => c.includes('hrp_ci_')), JSON.stringify(tokenCells))
  await shard(send, '10-token-list-prefix-only')

  // 吊销
  await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(tokenName)})
      window.__t.btn(tr, '吊销').click()
      return 'ok'
    })()`,
  )
  await confirmBox(send, '吊销')
  await waitFor(
    send,
    `![...document.querySelectorAll('.el-table__body tr')].some(tr => window.__t.text(tr).includes(${JSON.stringify(tokenName)}))`,
    { timeout: 15_000, label: '令牌从列表移除' },
  )
  check('吊销后令牌从列表移除', true)
  await shard(send, '11-token-revoked')
} catch (e) {
  failures.push(`脚本异常：${e.message}`)
  console.log(`\n[异常] ${e.stack}`)
} finally {
  await shutdown(proc, undefined)
  console.log(`\n===== ${ok} 项通过，${failures.length} 项失败 =====`)
  failures.forEach((f) => console.log(`  - ${f}`))
  console.log(`截图目录：${SHOTS}`)
  process.exit(failures.length ? 1 : 0)
}
