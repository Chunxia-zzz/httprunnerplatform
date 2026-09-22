/**
 * 账号与会话的浏览器端到端验收（CDP 驱动，零依赖）。
 *
 * 用法：
 *   HRP_UI_BASE=http://127.0.0.1:5173 node scripts/ui/auth-smoke.mjs
 *
 * 前置：
 *   1. 后端已在 HRP_UI_BASE 的 /api 代理目标上运行（默认 127.0.0.1:8080）；
 *   2. 数据库里存在种子管理员 admin/admin123（migrate 会建）；
 *   3. 前端 dev server 或后端内嵌前端任一可用。
 *
 * 为什么这些断言必须走浏览器而不能只打接口：
 *   本轮的改动有三处只在**页面**上才看得出来 ——
 *   侧边栏按角色显隐、被吊销会话后前端是否真的退回登录页、
 *   以及"改完密码当前会话要保留"。接口全绿但页面把人莫名登出，
 *   只有这条路能发现。
 *
 * ⭐ 核心验收是 STEP 12：管理员禁用某账号后，**该账号正在使用的那个会话**
 *    必须立刻失效。会话表里缓存着 Principal（含 role），中间件信任这份缓存，
 *    所以不吊销的话，被禁用/降级的人在 TTL（默认 24h）内照常使用 ——
 *    数据库改对了、页面也刷新了，但改动等于没生效。
 */
import { mkdirSync } from 'node:fs'
import { connect, evaluate, goto, launchChrome, screenshot, shutdown, waitFor } from './cdp.mjs'

const BASE = process.env.HRP_UI_BASE || 'http://127.0.0.1:5173'
const SHOTS = process.env.HRP_UI_SHOTS || 'F:/httprunnerplatform-tools/tmp/ui-shots'

const ADMIN = { user: 'admin', pass: 'admin123' }
const SUFFIX = String(Date.now()).slice(-6)
const MEMBER = { user: `ui_m_${SUFFIX}`, pass: 'memberpass123' }
const MEMBER_NEW_PASS = 'memberpass456'
const PROJECT = { code: `ui_p_${SUFFIX}`, name: `UI 冒烟项目 ${SUFFIX}` }

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
// 注入页面内助手。整页导航会清掉执行上下文，所以每次 goto 后都要重新注入。
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
  // 按表单项的 label 取输入框。label 前的必填星号是伪元素（不在 innerText 里），
  // 但保险起见仍去掉可能的 '*'。
  field(root, label) {
    if (!root) return null
    for (const item of root.querySelectorAll('.el-form-item')) {
      const l = this.text(item.querySelector('.el-form-item__label')).replace(/\\*/g, '').trim()
      if (l === label) return item.querySelector('input, textarea')
    }
    return null
  },
  row(username) {
    return [...document.querySelectorAll('.el-table__body tr')]
      .find(tr => this.text(tr).includes(username)) || null
  },
  btn(root, label) {
    if (!root) return null
    return [...root.querySelectorAll('button')].find(b => this.text(b) === label) || null
  },
  // 表格某行的全部操作按钮文案，用于断言"按钮被禁用"而不是"按钮不存在"
  rowBtns(username) {
    const tr = this.row(username)
    if (!tr) return null
    return [...tr.querySelectorAll('button')].map(b => ({ label: this.text(b), disabled: b.disabled }))
  },
  rowCells(username) {
    const tr = this.row(username)
    return tr ? [...tr.querySelectorAll('td')].map(td => this.norm(td.innerText)) : null
  },
  // 某个 .el-switch 是否处于开启态
  switchOn(root) { const s = root && root.querySelector('.el-switch'); return !!s && s.classList.contains('is-checked') },
  pickRadio(root, label) {
    const r = [...root.querySelectorAll('.el-radio')].find(x => this.text(x).includes(label))
    if (!r) throw new Error('找不到单选项: ' + label)
    const input = r.querySelector('input')
    if (!input.checked) input.click()
  },
  sessionCookie() {
    return document.cookie.split('; ').find(c => c.startsWith('hrp_session=')) || null
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

async function body(send) {
  return await evaluate(send, 'window.__t.body()')
}
async function path(send) {
  return await evaluate(send, 'location.pathname')
}
/**
 * 截图存证。
 *
 * 先等一下再截：Element Plus 的 dialog / message-box 有 0.3s 淡入动画，
 * 立刻截会得到一张半透明的、什么都读不出来的图 —— 看着像 UI 坏了，
 * 实际只是拍早了。这类"假故障"会白花很多时间去查一个不存在的 bug。
 */
async function shard(send, name) {
  await sleep(600)
  mkdirSync(SHOTS, { recursive: true })
  await screenshot(send, `${SHOTS}/${name}.png`)
}

/** 浏览器当前持有的会话 Cookie 值（HttpOnly，所以只能通过 CDP 读）。 */
async function sessionCookie(send) {
  const { cookies } = await send('Network.getCookies', { urls: [BASE] })
  const c = cookies.find((x) => x.name === 'hrp_session')
  return c ? c.value : null
}

/** 把浏览器切到指定会话（模拟"另一台设备/另一个标签页"）。 */
async function useCookie(send, value) {
  await send('Network.clearBrowserCookies')
  if (value) {
    await send('Network.setCookie', {
      name: 'hrp_session',
      value,
      url: BASE,
      path: '/',
      httpOnly: true,
    })
  }
}

/**
 * 用页面表单登录。
 *
 * 登录页是 meta.public，已登录时会被守卫弹回 /cases —— 所以这里先清干净
 * 浏览器 Cookie，保证一定停在登录页上。
 */
async function formLogin(send, user, pass, { expectFail = false } = {}) {
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

  if (expectFail) {
    await waitFor(send, `window.__t.has('登录') && !!document.querySelector('.el-message--error')`, {
      timeout: 15_000,
      label: '等待登录失败提示',
    })
    return null
  }
  await waitFor(send, `location.pathname !== '/login'`, { timeout: 20_000, label: '登录跳转' })
  await evaluate(send, HELPERS)
  return await sessionCookie(send)
}

/** 从页面内直接打接口：证明"前端没给入口"之外，后端是否真的在拦。 */
async function apiFromPage(send, pathname, method = 'GET') {
  return await evaluate(
    send,
    `fetch('/api/v1${pathname}', { method: ${JSON.stringify(method)}, headers: { 'Content-Type': 'application/json' } })
       .then(async r => ({ http: r.status, body: await r.json().catch(() => null) }))`,
  )
}

/** 点一次 ElMessageBox 的确认按钮。 */
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

try {
  console.log(`\n靶子 ${BASE}    后缀 ${SUFFIX}\n`)

  // =========================================================================
  console.log('STEP 1  管理员登录')
  // =========================================================================
  const adminCookie = await formLogin(send, ADMIN.user, ADMIN.pass)
  check('管理员登录成功并拿到会话 Cookie', !!adminCookie)
  const adminText = await body(send)
  check('顶栏显示「管理员」标签', adminText.includes('管理员'))
  check('侧边栏出现「账号管理」入口', (await evaluate(send, `!!window.__t.btn(document, '账号管理')`)) || (await evaluate(send, `[...document.querySelectorAll('.el-menu-item')].some(i => window.__t.text(i) === '账号管理')`)))
  await shard(send, '01-admin-logged-in')

  // =========================================================================
  console.log('\nSTEP 2  管理员建一个项目（供成员验证「删项目被拒」）')
  // =========================================================================
  await gotoPage(send, '/projects', '项目管理')
  await evaluate(send, `(window.__t.btn(document, '新建项目') || [...document.querySelectorAll('button')].find(b => window.__t.text(b).includes('新建项目'))).click(), 'ok'`)
  await waitFor(send, `!!window.__t.dlg('新建项目')`, { timeout: 10_000, label: '新建项目弹窗' })
  await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('新建项目')
      window.__t.setInput(window.__t.field(d, '标识'), ${JSON.stringify(PROJECT.code)})
      window.__t.setInput(window.__t.field(d, '名称'), ${JSON.stringify(PROJECT.name)})
      window.__t.btn(d, '保存').click()
      return 'ok'
    })()`,
  )
  await waitFor(
    send,
    `[...document.querySelectorAll('.el-table__body tr')].some(tr => window.__t.text(tr).includes(${JSON.stringify(PROJECT.code)}))`,
    { timeout: 20_000, label: '项目出现在列表' },
  )
  check('项目创建成功并出现在列表', true)

  // =========================================================================
  console.log(`\nSTEP 3  管理员新建成员账号 ${MEMBER.user}`)
  // =========================================================================
  await gotoPage(send, '/users', '账号管理')
  check('管理员能进入账号管理页', (await path(send)) === '/users')
  const usersPageText = await body(send)
  check('账号管理页说明了「立即吊销会话」的行为', usersPageText.includes('立即吊销'))
  check('列表里能看到种子管理员 admin', (await evaluate(send, `!!window.__t.row('admin')`)))

  await evaluate(
    send,
    `(window.__t.btn(document, '新建账号') || [...document.querySelectorAll('button')].find(b => window.__t.text(b).includes('新建账号'))).click(), 'ok'`,
  )
  await waitFor(send, `!!window.__t.dlg('新建账号')`, { timeout: 10_000, label: '新建账号弹窗' })
  await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('新建账号')
      window.__t.setInput(window.__t.field(d, '账号'), ${JSON.stringify(MEMBER.user)})
      window.__t.setInput(window.__t.field(d, '初始密码'), ${JSON.stringify(MEMBER.pass)})
      window.__t.pickRadio(d, '成员')
      window.__t.btn(d, '创建').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!!window.__t.row(${JSON.stringify(MEMBER.user)})`, {
    timeout: 20_000,
    label: '新账号出现在列表',
  })
  const memberCells = await evaluate(send, `window.__t.rowCells(${JSON.stringify(MEMBER.user)})`)
  check('新账号落库并出现在列表', !!memberCells, JSON.stringify(memberCells))
  check('新账号角色为「成员」', (memberCells || []).includes('成员') || (memberCells || []).join('|').includes('成员'))
  check('新账号默认启用', (memberCells || []).join('|').includes('已启用'))

  // =========================================================================
  console.log('\nSTEP 4  用成员账号登录，验证页面上的角色隔离')
  // =========================================================================
  const memberCookie = await formLogin(send, MEMBER.user, MEMBER.pass)
  check('成员账号登录成功', !!memberCookie)
  await waitFor(send, `window.__t.has('用例管理')`, { timeout: 15_000, label: '进入主布局' })

  const memberNav = await evaluate(
    send,
    `[...document.querySelectorAll('.el-menu-item')].map(i => window.__t.text(i))`,
  )
  check('成员侧边栏**没有**「账号管理」', !memberNav.includes('账号管理'), JSON.stringify(memberNav))
  check('成员侧边栏仍有用例/执行/环境/项目', ['用例管理', '执行记录', '环境管理', '项目管理'].every((x) => memberNav.includes(x)), JSON.stringify(memberNav))
  const memberTop = await evaluate(send, `window.__t.text(document.querySelector('.layout__header-right'))`)
  check('成员顶栏不显示「管理员」标签', !memberTop.includes('管理员'), memberTop)
  await shard(send, '02-member-logged-in')

  // =========================================================================
  console.log('\nSTEP 5  成员直接输 URL 访问 /users')
  // =========================================================================
  await gotoPage(send, '/users')
  await waitFor(send, `location.pathname !== '/users'`, { timeout: 15_000, label: '被重定向' })
  const afterUsers = await path(send)
  check('成员访问 /users 被重定向到用例页', afterUsers === '/cases', `实际 ${afterUsers}`)
  const redirectText = await body(send)
  check('给出可读的拒绝理由而不是一屏 403', redirectText.includes('仅管理员可访问'))
  await shard(send, '03-member-blocked-from-users')

  // 后端层面也要拦（前端隐藏从来不是权限控制）
  const apiUsers = await apiFromPage(send, '/users')
  check('成员直接打 GET /users 得 HTTP 403', apiUsers.http === 403, JSON.stringify(apiUsers))
  check('业务码为 40300', apiUsers.body?.code === 40300, JSON.stringify(apiUsers.body))

  // =========================================================================
  console.log('\nSTEP 6  成员不能删项目')
  // =========================================================================
  await gotoPage(send, '/projects', '项目管理')
  const memberBtns = await evaluate(send, `window.__t.rowBtns(${JSON.stringify(PROJECT.code)})`)
  const delBtn = (memberBtns || []).find((b) => b.label === '删除')
  check('成员看到的删除按钮存在但被禁用', !!delBtn && delBtn.disabled === true, JSON.stringify(memberBtns))
  check('成员仍可编辑项目（收紧删除不该误伤正常功能）', (memberBtns || []).some((b) => b.label === '编辑' && !b.disabled), JSON.stringify(memberBtns))
  await shard(send, '04-member-project-delete-disabled')

  // 绕过 UI 直接打接口，验证后端真的在拦。
  // 用 id 而非 code：删除端点是按 id 路由的（/projects/:id）。
  const delResp = await evaluate(
    send,
    `fetch('/api/v1/projects/1', { method: 'DELETE', headers: { 'Content-Type': 'application/json' } })
       .then(async r => ({ http: r.status, body: await r.json().catch(() => null) }))`,
  )
  check('成员直接打 DELETE /projects/1 得 HTTP 403', delResp.http === 403, JSON.stringify(delResp))
  check('业务码为 40300', delResp.body?.code === 40300, JSON.stringify(delResp.body))

  // =========================================================================
  console.log('\nSTEP 7  切回管理员（成员会话 M 在服务端仍然有效）')
  // =========================================================================
  await useCookie(send, adminCookie)
  await gotoPage(send, '/users', '账号管理')
  check('切回管理员会话后仍能进账号管理页', (await path(send)) === '/users')

  // 证明 M 是独立有效的：把它装回浏览器也能用
  await useCookie(send, memberCookie)
  await gotoPage(send, '/cases', '用例管理')
  const memberStillAlive = await path(send)
  check('成员会话 M 独立有效（未禁用前能正常访问）', memberStillAlive === '/cases', `实际 ${memberStillAlive}`)
  await gotoPage(send, '/users')
  await waitFor(send, `location.pathname !== '/users'`, { timeout: 15_000, label: '成员被挡' })
  check('同一份 M 依然没有管理员权限', (await path(send)) === '/cases')

  // =========================================================================
  console.log('\nSTEP 8  管理员看到该成员在线会话 ≥ 1')
  // =========================================================================
  await useCookie(send, adminCookie)
  await gotoPage(send, '/users', '账号管理')
  const beforeCells = await evaluate(send, `window.__t.rowCells(${JSON.stringify(MEMBER.user)})`)
  // 列序：ID | 账号 | 昵称 | 角色 | 状态 | 在线会话 | 创建时间 | 操作
  const sessionsBefore = Number(((beforeCells || [])[5] || '').replace(/\D/g, ''))
  check('禁用前在线会话数 ≥ 1（说明 M 真的在服务端表里）', sessionsBefore >= 1, `实际 ${sessionsBefore}，整行 ${JSON.stringify(beforeCells)}`)

  // =========================================================================
  console.log('\nSTEP 9  管理员禁用该成员（走编辑弹窗）')
  // =========================================================================
  await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(MEMBER.user)})
      window.__t.btn(tr, '编辑').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!!window.__t.dlg('编辑账号')`, { timeout: 10_000, label: '编辑弹窗' })
  const switchWasOn = await evaluate(send, `window.__t.switchOn(window.__t.dlg('编辑账号'))`)
  check('编辑弹窗里该成员的「启用」开关是开着的', switchWasOn === true)
  await evaluate(send, `window.__t.dlg('编辑账号').querySelector('.el-switch').click(), 'ok'`)
  check('点一次后开关变为关闭', (await evaluate(send, `window.__t.switchOn(window.__t.dlg('编辑账号'))`)) === false)
  await shard(send, '05-admin-disable-member')
  await evaluate(send, `window.__t.btn(window.__t.dlg('编辑账号'), '保存').click(), 'ok'`)
  await confirmBox(send, '确认变更')
  await waitFor(send, `!window.__t.dlg('编辑账号')`, { timeout: 10_000, label: '编辑弹窗关闭' })

  await waitFor(
    send,
    `window.__t.rowCells(${JSON.stringify(MEMBER.user)})?.[4] === '已禁用'`,
    { timeout: 20_000, label: '列表刷新为已禁用' },
  )
  const afterCells = await evaluate(send, `window.__t.rowCells(${JSON.stringify(MEMBER.user)})`)
  const sessionsAfter = Number(((afterCells || [])[5] || '').replace(/\D/g, ''))
  check('禁用后状态列变为「已禁用」', (afterCells || [])[4] === '已禁用', JSON.stringify(afterCells))
  check('⭐ 禁用后在线会话数归零（吊销真的发生了）', sessionsAfter === 0, `实际 ${sessionsAfter}`)
  await shard(send, '06-admin-disabled-member-zero-sessions')

  // =========================================================================
  console.log('\nSTEP 10  ⭐ 核心验收：被禁用者的会话立即失效')
  // =========================================================================
  await useCookie(send, memberCookie)
  await gotoPage(send, '/cases')
  await waitFor(send, `location.pathname === '/login'`, {
    timeout: 20_000,
    label: '被吊销的会话退回登录页',
  })
  const revokedPath = await path(send)
  check('⭐ 被禁用的成员会话立即失效，退回登录页', revokedPath === '/login', `实际 ${revokedPath}`)
  await shard(send, '07-revoked-session-kicked-to-login')

  const relogin = await formLogin(send, MEMBER.user, MEMBER.pass, { expectFail: true })
  check('被禁用的账号无法再登录', relogin === null)

  // =========================================================================
  console.log('\nSTEP 11  管理员重新启用该成员')
  // =========================================================================
  await useCookie(send, adminCookie)
  await gotoPage(send, '/users', '账号管理')
  await evaluate(
    send,
    `(() => {
      const tr = window.__t.row(${JSON.stringify(MEMBER.user)})
      window.__t.btn(tr, '编辑').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!!window.__t.dlg('编辑账号')`, { timeout: 10_000, label: '编辑弹窗' })
  await evaluate(send, `window.__t.dlg('编辑账号').querySelector('.el-switch').click(), 'ok'`)
  await evaluate(send, `window.__t.btn(window.__t.dlg('编辑账号'), '保存').click(), 'ok'`)
  await confirmBox(send, '确认变更')
  await waitFor(
    send,
    `window.__t.rowCells(${JSON.stringify(MEMBER.user)})?.[4] === '已启用'`,
    { timeout: 20_000, label: '列表刷新为已启用' },
  )
  check('管理员重新启用成员成功', true)

  // =========================================================================
  console.log('\nSTEP 12  本人改密码：当前会话保留、其它会话被踢')
  // =========================================================================
  // 两处登录 ⇒ 同账号两个独立会话，模拟"另一台设备"。
  const dev1 = await formLogin(send, MEMBER.user, MEMBER.pass)
  const dev2 = await formLogin(send, MEMBER.user, MEMBER.pass)
  check('同一账号拿到两个不同的会话', !!dev1 && !!dev2 && dev1 !== dev2)

  // 用 dev1 在浏览器里改密码
  await useCookie(send, dev1)
  await gotoPage(send, '/cases', '用例管理')
  await evaluate(
    send,
    `(() => {
      document.querySelector('.layout__user').click()
      return 'ok'
    })()`,
  )
  await waitFor(
    send,
    `[...document.querySelectorAll('.el-dropdown-menu__item')].some(i => window.__t.text(i) === '修改密码')`,
    { timeout: 10_000, label: '用户下拉菜单' },
  )
  await evaluate(
    send,
    `(() => {
      [...document.querySelectorAll('.el-dropdown-menu__item')].find(i => window.__t.text(i) === '修改密码').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!!window.__t.dlg('修改密码')`, { timeout: 10_000, label: '修改密码弹窗' })
  await shard(send, '08-change-password-dialog')
  await evaluate(
    send,
    `(() => {
      const d = window.__t.dlg('修改密码')
      window.__t.setInput(window.__t.field(d, '原密码'), ${JSON.stringify(MEMBER.pass)})
      window.__t.setInput(window.__t.field(d, '新密码'), ${JSON.stringify(MEMBER_NEW_PASS)})
      window.__t.setInput(window.__t.field(d, '确认新密码'), ${JSON.stringify(MEMBER_NEW_PASS)})
      window.__t.btn(d, '确认修改').click()
      return 'ok'
    })()`,
  )
  await waitFor(send, `!window.__t.dlg('修改密码')`, { timeout: 20_000, label: '修改密码弹窗关闭' })

  await gotoPage(send, '/cases', '用例管理')
  check('改密码后**当前**会话仍然有效（没有被自己登出）', (await path(send)) === '/cases', `实际 ${await path(send)}`)
  await shard(send, '09-current-session-survives-password-change')

  await useCookie(send, dev2)
  await gotoPage(send, '/cases')
  await waitFor(send, `location.pathname === '/login'`, { timeout: 20_000, label: '另一设备的会话被踢' })
  check('改密码后**另一设备**的会话被踢回登录页', (await path(send)) === '/login')

  const oldPassLogin = await formLogin(send, MEMBER.user, MEMBER.pass, { expectFail: true })
  check('旧密码不能再登录', oldPassLogin === null)
  const newPassLogin = await formLogin(send, MEMBER.user, MEMBER_NEW_PASS)
  check('新密码可以登录', !!newPassLogin)

  // =========================================================================
  console.log('\nSTEP 13  自我保护与最后管理员守卫在页面上也拦得住')
  // =========================================================================
  await useCookie(send, adminCookie)
  await gotoPage(send, '/users', '账号管理')
  const adminBtns = await evaluate(send, `window.__t.rowBtns('admin')`)
  const adminDel = (adminBtns || []).find((b) => b.label === '删除')
  const adminReset = (adminBtns || []).find((b) => b.label === '重置密码')
  check('删除自己的按钮被禁用', !!adminDel && adminDel.disabled === true, JSON.stringify(adminBtns))
  check('重置自己密码的按钮被禁用（应走「修改密码」）', !!adminReset && adminReset.disabled === true, JSON.stringify(adminBtns))
  check('但管理员仍可编辑自己（改昵称）', (adminBtns || []).some((b) => b.label === '编辑' && !b.disabled))
  await shard(send, '10-admin-self-protection')

  // 直接打接口验证后端守卫（前端禁用只是"不给按钮"）
  const selfDel = await evaluate(
    send,
    `fetch('/api/v1/users/1', { method: 'DELETE', headers: { 'Content-Type': 'application/json' } })
       .then(async r => ({ http: r.status, body: await r.json().catch(() => null) }))`,
  )
  check('直接打 DELETE /users/1（自己）被拒', selfDel.body?.code === 40300, JSON.stringify(selfDel))

  // 只有一个可用管理员时，不能把自己降级
  const selfDemote = await evaluate(
    send,
    `fetch('/api/v1/users/1', { method: 'PUT', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ nickname: '管理员', role: 'member' }) })
       .then(async r => ({ http: r.status, body: await r.json().catch(() => null) }))`,
  )
  check('直接打 PUT /users/1 把自己降级被拒', selfDemote.body?.code === 40300, JSON.stringify(selfDemote))

  // 管理员会话必须仍然有效（上一步的拒绝不该把人踢下线）
  await gotoPage(send, '/users', '账号管理')
  check('被拒绝的操作没有误伤管理员自己的会话', (await path(send)) === '/users')
} finally {
  await shutdown(proc, undefined)
  console.log(`\n===== ${ok} 项通过，${failures.length} 项失败 =====`)
  failures.forEach((f) => console.log(`  - ${f}`))
  console.log(`截图目录：${SHOTS}`)
  process.exit(failures.length ? 1 : 0)
}
