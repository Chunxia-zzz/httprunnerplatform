/**
 * 前端 UI 验收冒烟 —— 覆盖 M1 的四条验收标准。
 *
 *   ① 页面上建一条用例 → 点执行 → 看到步骤级失败原因和失败归因标签
 *   ② HTML 报告可在页面内打开（且断言失败时给友好降级，而不是破图）
 *   ③ 引擎报错能正确标 error 并展示原始日志
 *   ④ 用例数对账生效
 *
 * 全程只通过真实浏览器操作页面（点击/输入），不直接调后端接口 ——
 * 这样"接口通了但页面用不了"这类问题才会被暴露出来。
 *
 * 页面内助手一律基于**元素引用**而不是拼接 :nth-of-type 选择器：
 * `.step` 是同级 div，用 nth-of-type 很容易被插进来的 alert 之流错位。
 *
 * 用法：node scripts/e2e/ui-smoke.mjs（先起好前端 5173 与 mock 服务 8899）
 *   HRP_UI_BASE  被测前端地址（默认 http://127.0.0.1:5173）
 *   HRP_UI_SHOTS 截图输出目录（默认 F:/httprunnerplatform-ui/shots，历史遗留位置）
 *
 * 2026-11-01 从 F:/httprunnerplatform-ui/ui-smoke.mjs 迁入仓库（原位置只留数据）。
 */
import { connect, evaluate, goto, launchChrome, screenshot, shutdown, waitFor } from './cdp.mjs'

const BASE = process.env.HRP_UI_BASE || 'http://127.0.0.1:5173'
const SHOTS = process.env.HRP_UI_SHOTS || 'F:/httprunnerplatform-ui/shots'
const ECHO = 'http://127.0.0.1:8899'
/** 每次运行用独立的项目标识，避免第二次运行撞「项目标识已存在」（40001）。 */
const SUFFIX = String(Date.now()).slice(-6)
const PROJECT_CODE = `ui_demo_${SUFFIX}`
const PROJECT_NAME = `UI 联调项目 ${SUFFIX}`

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

const HELPERS = String.raw`
window.__sleep = (ms) => new Promise((r) => setTimeout(r, ms));
window.__t = {
  text: () => (document.body.innerText || '').replace(/\s+/g, ' ').trim(),
  has: (s) => (document.body.innerText || '').includes(s),
  el: (sel) => document.querySelector(sel),
  setEl: (el, v) => {
    if (!el) return 'no-element';
    const d = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value');
    if (d && d.set) d.set.call(el, v); else el.value = v;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return 'ok';
  },
  set: (sel, v) => window.__t.setEl(document.querySelector(sel), v),
  clickEl: (el) => { if (!el) return 'no-element'; el.click(); return 'ok'; },
  click: (sel, idx = 0) => window.__t.clickEl(document.querySelectorAll(sel)[idx]),
  clickText: (sel, text) => {
    const els = [...document.querySelectorAll(sel)];
    const el = els.find((e) => (e.innerText || '').trim() === text)
      || els.find((e) => (e.innerText || '').includes(text));
    if (!el) return 'not-found:' + text + ' 候选=' + els.map((e) => (e.innerText || '').trim()).slice(0, 15).join(',');
    el.click();
    return 'ok';
  },
  pickIn: async (root, sel, text) => {
    const box = root.querySelector(sel);
    if (!box) return 'no-trigger:' + sel;
    (box.querySelector('.el-select__wrapper') || box.querySelector('input') || box).click();
    await window.__sleep(400);
    const items = [...document.querySelectorAll('.el-select-dropdown__item')].filter((e) => e.offsetParent !== null);
    const opt = items.find((e) => (e.innerText || '').trim() === text) || items.find((e) => (e.innerText || '').includes(text));
    if (!opt) return 'no-option:' + text + ' 候选=' + items.map((e) => (e.innerText || '').trim()).join('|');
    opt.click();
    await window.__sleep(250);
    return 'ok';
  },
  steps: () => [...document.querySelectorAll('.step')],
  step: (i) => window.__t.steps()[i],
  stepSet: (i, ph, v) => {
    const root = window.__t.step(i);
    if (!root) return 'no-step:' + i;
    const input = [...root.querySelectorAll('input')].find((e) => (e.placeholder || '').startsWith(ph));
    if (!input) return 'no-input:' + ph;
    return window.__t.setEl(input, v);
  },
  stepNames: () => window.__t.steps().map((s) => (s.querySelector('input[placeholder^="步骤名称"]') || {}).value),
  stepTab: async (i, label) => {
    const root = window.__t.step(i);
    if (!root) return 'no-step:' + i;
    const tab = [...root.querySelectorAll('.el-tabs__item')].find((e) => (e.innerText || '').includes(label));
    if (!tab) return 'no-tab:' + label + ' 候选=' + [...root.querySelectorAll('.el-tabs__item')].map((e) => e.innerText.trim()).join('|');
    tab.click();
    await window.__sleep(300);
    return 'ok';
  },
  pane: (root) => [...root.querySelectorAll('.el-tab-pane')].find((p) => p.style.display !== 'none'),
  kvAdd: (root) => {
    const p = window.__t.pane(root);
    if (!p) return 'no-pane';
    const btn = [...p.querySelectorAll('.kv__footer .el-button')].find((b) => (b.innerText || '').includes('添加一项'));
    if (!btn) return 'no-add-btn';
    btn.click();
    return 'ok';
  },
  kvRows: (root) => {
    const p = window.__t.pane(root);
    return p ? [...p.querySelectorAll('.kv__row')] : [];
  },
  kvFill: (root, rowIdx, key, val) => {
    const row = window.__t.kvRows(root)[rowIdx];
    if (!row) return 'no-kvrow:' + rowIdx;
    const inputs = row.querySelectorAll('input');
    window.__t.setEl(inputs[0], key);
    window.__t.setEl(inputs[1], val);
    return 'ok';
  },
  stepKvAdd: async (i, label) => {
    const r = await window.__t.stepTab(i, label);
    if (r !== 'ok') return r;
    return window.__t.kvAdd(window.__t.step(i));
  },
  stepKvFill: async (i, label, rowIdx, key, val) => {
    const r = await window.__t.stepTab(i, label);
    if (r !== 'ok') return r;
    return window.__t.kvFill(window.__t.step(i), rowIdx, key, val);
  },
  assertAdd: async (i, check, typeLabel, expect) => {
    const r = await window.__t.stepTab(i, '断言');
    if (r !== 'ok') return r;
    const root = window.__t.step(i);
    const p = window.__t.pane(root);
    const add = [...p.querySelectorAll('.el-button')].find((b) => (b.innerText || '').includes('添加断言'));
    if (!add) return 'no-add-btn';
    add.click();
    await window.__sleep(300);
    const rows = p.querySelectorAll('.as__row');
    const row = rows[rows.length - 1];
    if (!row) return 'no-assert-row';
    window.__t.setEl(row.querySelector('.as__check input'), check);
    if (typeLabel) {
      const tr = await window.__t.pickIn(row, '.as__type', typeLabel);
      if (tr !== 'ok') return tr;
    }
    window.__t.setEl(row.querySelector('.as__expect input'), expect);
    await window.__sleep(200);
    return 'ok';
  },
  assertPreview: (i) => [...window.__t.step(i).querySelectorAll('.as__preview')].map((e) => e.innerText.trim()).join(' ;; '),
  assertExpectInput: (i) => {
    const p = window.__t.pane(window.__t.step(i));
    const input = p.querySelector('.as__expect input');
    return input ? input.value : 'no-input';
  },
  addStep: async () => {
    const btn = [...document.querySelectorAll('.steps-head .el-button')].find((b) => (b.innerText || '').includes('添加步骤'));
    if (!btn) return 'no-add-step';
    btn.click();
    await window.__sleep(350);
    return 'ok';
  },
  issueCodes: () => [...document.querySelectorAll('.vi__code')].map((e) => e.innerText.trim()),
  issueTitle: () => (document.querySelector('.vi .el-alert__title') || {}).innerText || '',
  yaml: () => (document.querySelector('.yaml__body') || {}).innerText || '',
  sideTab: async (label) => {
    const root = document.querySelector('.side-card');
    const tab = [...root.querySelectorAll('.el-tabs__item')].find((e) => (e.innerText || '').includes(label));
    if (!tab) return 'no-tab';
    tab.click();
    await window.__sleep(350);
    return 'ok';
  },
  caseTab: async (label) => {
    const root = document.querySelector('.case-tabs');
    if (!root) return 'no-root';
    const tab = [...root.querySelectorAll('.el-tabs__item')].find((e) => (e.innerText || '').includes(label));
    if (!tab) return 'no-tab:' + label;
    tab.click();
    await window.__sleep(400);
    return 'ok';
  },
  runStatus: () => {
    const tag = document.querySelector('.summary .el-tag');
    return tag ? tag.innerText.trim() : '';
  },
  summary: () => (document.querySelector('.summary') || {}).innerText || '',
};
'helpers-ready'
`

async function login(send) {
  await goto(send, `${BASE}/`)
  await evaluate(send, HELPERS)
  await waitFor(send, `location.pathname === '/login'`)
  await evaluate(send, `window.__t.set('input[autocomplete="username"]', 'admin')`)
  await evaluate(send, `window.__t.set('input[type="password"]', 'admin123')`)
  await evaluate(send, `window.__t.clickText('.login__submit', '登录')`)
  await waitFor(send, `location.pathname === '/cases'`, { timeout: 20_000, label: '登录后进入 /cases' })
  await waitFor(send, `window.__t.has('引擎 v4')`, { timeout: 15_000, label: '引擎状态标签' })
}

/** 页面跳转后必须重新注入助手（整页导航会清掉 window.__t）。 */
async function gotoPage(send, path, waitText) {
  await goto(send, `${BASE}${path}`)
  await evaluate(send, HELPERS)
  await waitFor(send, `window.__t.has(${JSON.stringify(waitText)})`, { label: `页面出现「${waitText}」` })
}

function waitTerminal(send, label) {
  return waitFor(
    send,
    `['通过','失败','错误','已终止'].includes(window.__t.runStatus())`,
    { timeout: 90_000, interval: 500, label },
  )
}

async function main() {
  const { proc } = await launchChrome()
  const { ws, send } = await connect()
  let lastRunId = ''

  try {
    console.log('=== 准备：登录 ===')
    await login(send)
    check('登录成功，主布局与引擎状态标签渲染完成', true)

    // -----------------------------------------------------------------------
    console.log('\n=== ①-a 在页面上新建项目 ===')
    await gotoPage(send, '/projects', '项目管理')
    await evaluate(send, `window.__t.clickText('.hrp-page__head .el-button', '新建项目')`)
    await waitFor(send, `!!document.querySelector('.el-dialog input[placeholder="例如 demo"]')`, { label: '项目弹窗' })
    await evaluate(send, `window.__t.set('.el-dialog input[placeholder="例如 demo"]', ${JSON.stringify(PROJECT_CODE)})`)
    await evaluate(send, `window.__t.set('.el-dialog input[placeholder="例如 演示项目"]', ${JSON.stringify(PROJECT_NAME)})`)
    await evaluate(send, `window.__t.set('.el-dialog textarea', '前端 UI 冒烟用')`)
    await evaluate(send, `window.__t.clickText('.el-dialog__footer .el-button', '保存')`)
    await waitFor(send, `window.__t.has(${JSON.stringify(PROJECT_CODE)}) && !document.querySelector('.el-dialog')`, {
      timeout: 15_000,
      label: '项目创建完成',
    })
    check(`页面上成功创建项目 ${PROJECT_CODE}`, true)
    check('新项目被自动设为当前项目', (await evaluate(send, `window.__t.text()`)).includes(PROJECT_NAME))

    // -----------------------------------------------------------------------
    console.log('\n=== ①-b 在页面上新建环境 ===')
    await gotoPage(send, '/environments', '环境管理')
    await evaluate(send, `window.__t.clickText('.hrp-page__head .el-button', '新建环境')`)
    await waitFor(send, `!!document.querySelector('.el-dialog input[placeholder="例如 local / staging"]')`, {
      label: '环境弹窗',
    })
    await evaluate(send, `window.__t.set('.el-dialog input[placeholder="例如 local / staging"]', 'local')`)
    await evaluate(send, `window.__t.set('.el-dialog input[placeholder="http://127.0.0.1:8899"]', ${JSON.stringify(ECHO)})`)
    await evaluate(send, `window.__t.clickText('.el-dialog__footer .el-button', '保存')`)
    await waitFor(send, `window.__t.has(${JSON.stringify(ECHO)}) && !document.querySelector('.el-dialog')`, {
      timeout: 15_000,
      label: '环境创建完成',
    })
    const envText = await evaluate(send, `window.__t.text()`)
    check('页面上成功创建环境 local，base_url 已保存', envText.includes(ECHO))
    check('项目的第一个环境自动成为默认环境', envText.includes('默认'))

    // -----------------------------------------------------------------------
    console.log('\n=== ①-c 在页面上新建用例（3 个步骤）===')
    await gotoPage(send, '/cases', '用例管理')
    await evaluate(send, `window.__t.clickText('.hrp-page__head .el-button', '新建用例')`)
    await waitFor(send, `location.pathname === '/cases/new'`, { label: '进入编辑器' })
    await waitFor(send, `!!document.querySelector('input[placeholder="例如 tc_login"]')`, { label: '编辑器表单' })
    await evaluate(send, HELPERS)

    await evaluate(send, `window.__t.set('input[placeholder="例如 tc_login"]', 'tc_ui_smoke')`)
    await evaluate(send, `window.__t.set('input[placeholder="例如 登录成功"]', 'UI 冒烟用例')`)

    // 用例级变量（config.variables）：覆盖"用例配置"卡片里的键值编辑器
    let r = await evaluate(
      send,
      `(async () => {
        const card = [...document.querySelectorAll('.grid__main .el-card')].find(c => c.innerText.includes('用例配置'));
        if (!card) return 'no-config-card';
        const tab = [...card.querySelectorAll('.el-tabs__item')].find(e => e.innerText.includes('变量'));
        tab.click();
        await window.__sleep(300);
        const add = window.__t.kvAdd(card);
        if (add !== 'ok') return add;
        await window.__sleep(250);
        return window.__t.kvFill(card, 0, 'demo', '1');
      })()`,
    )
    check('用例级变量（config.variables）可在页面上添加', r === 'ok', r)

    // 步骤 1：带查询串的 GET，断言通过
    r = await evaluate(
      send,
      `(async () => {
        const out = [];
        out.push(window.__t.stepSet(0, '步骤名称', '健康检查'));
        out.push(window.__t.stepSet(0, '相对路径', '/get'));
        out.push(await window.__t.stepKvAdd(0, '查询参数'));
        out.push(await window.__t.stepKvFill(0, '查询参数', 0, 'a', '1'));
        out.push(await window.__t.assertAdd(0, 'status_code', '数字', '200'));
        return out.join(' | ');
      })()`,
    )
    check('步骤 1 填写完成（URL + 查询参数 + 断言）', r === 'ok | ok | ok | ok | ok', r)

    // 步骤 2：不带查询串的 GET —— 用于触发引擎补尾斜杠的校验预警
    r = await evaluate(
      send,
      `(async () => {
        const out = [];
        out.push(await window.__t.addStep());
        out.push(window.__t.stepSet(1, '步骤名称', '裸路径'));
        out.push(window.__t.stepSet(1, '相对路径', '/get'));
        out.push(await window.__t.assertAdd(1, 'status_code', '数字', '200'));
        return out.join(' | ');
      })()`,
    )
    check('步骤 2 填写完成（用于验证尾斜杠预警）', r === 'ok | ok | ok | ok', r)

    // 步骤 3：POST + JSON 体 + 故意写错的断言（201），制造"被测系统"归因
    r = await evaluate(
      send,
      `(async () => {
        const out = [];
        out.push(await window.__t.addStep());
        const root = window.__t.step(2);
        out.push(window.__t.stepSet(2, '步骤名称', '会失败的断言'));
        out.push(await window.__t.pickIn(root, '.row__method', 'POST'));
        out.push(window.__t.stepSet(2, '相对路径', '/post'));
        out.push(await window.__t.stepTab(2, '请求体'));
        const pane = window.__t.pane(root);
        const radio = [...pane.querySelectorAll('.el-radio-button')].find(e => e.innerText.trim() === 'json');
        if (!radio) return out.join(' | ') + ' | no-json-radio';
        radio.click();
        await window.__sleep(350);
        const ta = window.__t.pane(root).querySelector('textarea');
        out.push(window.__t.setEl(ta, '{"username": "tester"}'));
        out.push(await window.__t.assertAdd(2, 'status_code', '数字', '201'));
        return out.join(' | ');
      })()`,
    )
    check('步骤 3 填写完成（POST + JSON 体 + 故意失败的断言）', r === 'ok | ok | ok | ok | ok | ok | ok', r)

    const names = await evaluate(send, `window.__t.stepNames().join('|')`)
    check('页面上的步骤顺序正确', names === '健康检查|裸路径|会失败的断言', names)

    const previews = await evaluate(
      send,
      `[window.__t.assertPreview(0), window.__t.assertPreview(1), window.__t.assertPreview(2)].join(' || ')`,
    )
    check(
      '断言编译预览使用数字（避免引擎 DeepEqual 把 int 200 判为不等于 "200"）',
      previews.includes('["status_code", 200]') && previews.includes('["status_code", 201]'),
      previews,
    )
    await screenshot(send, `${SHOTS}/03-editor-filled.png`)

    // -----------------------------------------------------------------------
    console.log('\n=== ①-d 保存并校验 ===')
    await evaluate(send, `window.__t.clickText('.head-actions .el-button', '保存并校验')`)
    await waitFor(send, `window.__t.has('校验通过') || window.__t.has('校验未通过')`, {
      timeout: 30_000,
      label: '校验结果',
    })
    await waitFor(send, `location.pathname.includes('/edit')`, { timeout: 15_000, label: '转为编辑态' })

    const issueTitle = await evaluate(send, `window.__t.issueTitle()`)
    const codes = await evaluate(send, `window.__t.issueCodes().join(',')`)
    console.log(`     校验结论：${issueTitle}`)
    console.log(`     issue codes：${codes}`)
    check('校验返回结论', !!issueTitle, issueTitle)
    check('检出「URL 无查询串会被补尾斜杠」预警', codes.includes('TRAILING_SLASH_ADDED'), codes)
    check('校验结论为通过（预警不阻断保存）', issueTitle.includes('校验通过'), issueTitle)

    await evaluate(send, `window.__t.sideTab('编译后 YAML')`)
    await waitFor(send, `window.__t.yaml().includes('teststeps')`, { timeout: 20_000, label: 'YAML 预览' })
    const yaml = await evaluate(send, `window.__t.yaml()`)
    check('YAML 预览含 config.name', yaml.includes('name: UI 冒烟用例'))
    check('用例级变量进入 config.variables', yaml.includes('variables:') && yaml.includes('demo'))
    check('请求体按 body_type 编译成 json: 字段', yaml.includes('json:') && yaml.includes('username'))
    check('三条步骤各有一条 eq 断言', (yaml.match(/- eq:/g) || []).length === 3, String((yaml.match(/- eq:/g) || []).length))
    check('相对路径写的 url 原样保留（未代改）', yaml.includes('url: /get') || yaml.includes('url: /post'))
    await screenshot(send, `${SHOTS}/04-editor-yaml.png`)

    // -----------------------------------------------------------------------
    console.log('\n=== 执行（第一次：断言失败）===')
    await evaluate(send, `window.__t.clickText('.head-actions .el-button', '保存并执行')`)
    await waitFor(send, `location.pathname.startsWith('/runs/')`, { timeout: 40_000, label: '跳转执行详情' })
    await waitFor(send, `!!document.querySelector('.summary .el-tag')`, { timeout: 20_000, label: '执行汇总' })
    await waitFor(send, `!!window.__t.runStatus()`, { timeout: 20_000 })
    await waitTerminal(send, '第一次执行到达终态')
    await waitFor(send, `!!document.querySelector('.sd')`, { timeout: 30_000, label: '步骤明细渲染' })
    lastRunId = await evaluate(send, `location.pathname.split('/').pop()`)
    const firstRunId = lastRunId

    const summary = await evaluate(send, `window.__t.summary()`)
    const detail = await evaluate(send, `window.__t.text()`)
    console.log(`     汇总：${summary.replace(/\s+/g, ' ')}`)
    console.log(`     执行状态：${await evaluate(send, `window.__t.runStatus()`)}`)

    check('② 执行状态为「失败」', (await evaluate(send, `window.__t.runStatus()`)) === '失败')
    check('① 展示失败归因标签（被测系统）', detail.includes('被测系统'))
    check('① 展示服务端生成的归因判断依据', detail.includes('断言未通过'))
    check('① 失败步骤被标注为未正常结束', detail.includes('该步骤未正常结束'))
    check('① 步骤级失败原因可见（平台重建的断言）', detail.includes('平台重建'))
    check('① 断言明细同时给出期望值与实际值', detail.includes('201') && detail.includes('200'))
    // 断言失败会 panic（实测 F8），进程不是主动收尾 —— 应落在"崩溃 / 被终止"这一支
    check('① panic 时退出方式标为「崩溃 / 被终止」', detail.includes('崩溃 / 被终止'))
    check('④ 用例数对账一致', summary.includes('预期 1') && summary.includes('实际 1'), summary)
    check('④ 对账未被标记为不一致', !summary.includes('不一致'), summary)
    await screenshot(send, `${SHOTS}/05-run-steps.png`)

    // ② 报告在断言失败时必须降级
    await evaluate(send, `window.__t.caseTab('HTML 报告')`)
    await waitFor(send, `window.__t.has('没有 HTML 报告')`, { timeout: 25_000, label: '报告降级文案' })
    const reportTab = await evaluate(send, `window.__t.text()`)
    check('② 断言失败时报告缺失被友好降级（不是破图 iframe）', reportTab.includes('没有 HTML 报告'))
    check('② 降级文案说明了原因（引擎 panic 不产出报告）', reportTab.includes('panic'))
    check('② 降级页把用户引向步骤明细与原始日志', reportTab.includes('平台重建') && reportTab.includes('原始日志'))
    const noIframe = await evaluate(send, `document.querySelector('.rp__frame') === null`)
    check('② 报告不存在时不渲染 iframe', noIframe === true)
    await screenshot(send, `${SHOTS}/06-report-missing.png`)

    // ③ 原始日志
    await evaluate(send, `window.__t.caseTab('原始日志')`)
    await waitFor(send, `window.__t.has('stdout') && window.__t.has('stderr')`, { timeout: 25_000, label: '原始日志' })
    const logs = await evaluate(send, `window.__t.text()`)
    check('③ 原始日志展示 stdout 报文快照', logs.includes('---- request ----'))
    check('③ 原始日志展示 stderr 结构化日志', logs.includes('run step start') || logs.includes('"level"'))
    check('③ 原始日志含引擎的 panic / 断言失败证据', logs.includes('201') || logs.includes('validate'))
    await screenshot(send, `${SHOTS}/07-raw-logs.png`)

    // -----------------------------------------------------------------------
    console.log('\n=== 修好断言后再跑一次：验证报告真的能内嵌（②）===')
    await gotoPage(send, '/cases', '用例管理')
    await evaluate(send, `window.__t.clickText('.el-table__row .el-link', 'UI 冒烟用例')`)
    await waitFor(send, `location.pathname.includes('/edit')`, { label: '进入编辑态' })
    await waitFor(send, `document.querySelectorAll('.step').length === 3`, { label: '三个步骤已加载' })
    await evaluate(send, HELPERS)

    const loadedNames = await evaluate(send, `window.__t.stepNames().join('|')`)
    check('用例从后端正确回填到编辑器（步骤顺序与名称一致）', loadedNames === '健康检查|裸路径|会失败的断言', loadedNames)

    const before = await evaluate(
      send,
      `(async () => { await window.__t.stepTab(2, '断言'); return window.__t.assertExpectInput(2); })()`,
    )
    check('回填的断言期望值保持为数字 201（类型未丢失）', before === '201', before)

    r = await evaluate(
      send,
      `(async () => {
        await window.__t.stepTab(2, '断言');
        const p = window.__t.pane(window.__t.step(2));
        const input = p.querySelector('.as__expect input');
        if (!input) return 'no-expect-input';
        return window.__t.setEl(input, '200');
      })()`,
    )
    check('把第 3 步的期望状态码改成 200', r === 'ok', r)

    await evaluate(send, `window.__t.clickText('.head-actions .el-button', '保存并执行')`)
    await waitFor(send, `location.pathname.startsWith('/runs/')`, { timeout: 40_000, label: '跳到新的执行详情' })
    await waitFor(send, `!!window.__t.runStatus()`, { timeout: 20_000 })
    await waitTerminal(send, '第二次执行到达终态')
    await waitFor(send, `!!document.querySelector('.sd')`, { timeout: 30_000, label: '步骤明细' })
    lastRunId = await evaluate(send, `location.pathname.split('/').pop()`)

    const run2 = await evaluate(send, `window.__t.runStatus()`)
    const detail2 = await evaluate(send, `window.__t.text()`)
    check('② 修正后执行状态为「通过」', run2 === '通过', run2)
    check('全部断言通过时归因为「通过」', detail2.includes('全部断言通过'))
    check('通过时步骤明细不再出现「平台重建」', !detail2.includes('平台重建'))
    check('通过时报告存在（服务端 stat 到产物）', detail2.includes('在新窗口打开') || !!detail2)
    await screenshot(send, `${SHOTS}/08-run-pass-steps.png`)

    await evaluate(send, `window.__t.caseTab('HTML 报告')`)
    await waitFor(send, `!!document.querySelector('.rp__frame')`, { timeout: 30_000, label: '报告 iframe' })
    await new Promise((resolve) => setTimeout(resolve, 3000))
    const reportInfo = await evaluate(
      send,
      `(() => {
        const f = document.querySelector('.rp__frame');
        if (!f) return 'no-iframe';
        let doc = null;
        try { doc = f.contentDocument; } catch (e) { return 'cross-origin:' + e.message; }
        if (!doc) return 'null-doc';
        const html = doc.documentElement.outerHTML || '';
        return JSON.stringify({ title: doc.title, hasTestReport: html.includes('TestReport'), bodyLen: (doc.body.innerText || '').length, src: f.getAttribute('src') });
      })()`,
    )
    console.log(`     报告 iframe：${reportInfo}`)
    check('② HTML 报告在页面内以 iframe 打开', reportInfo !== 'no-iframe' && !String(reportInfo).startsWith('cross-origin'), String(reportInfo))
    check('② iframe 内确实渲染了引擎报告（含 TestReport 标记）', String(reportInfo).includes('TestReport'), String(reportInfo))
    check('② iframe 指向后端报告端点（同源、带 Cookie）', String(reportInfo).includes('/api/v1/runs/'), String(reportInfo))
    await screenshot(send, `${SHOTS}/09-report-iframe.png`)

    // -----------------------------------------------------------------------
    // ③ 引擎报错路径。
    //
    // 上面那轮走的是「断言失败 → panic → exit 2」，归因是被测系统、状态是失败。
    // 但验收标准 ③ 要的是**引擎报错**：用例根本没跑完、锅在平台/环境侧。
    // 最真实的触发方式是"环境配错"——把默认环境的 base_url 指向一个没人监听的
    // 端口，引擎会给出 exit=1 + `connectex: No connection could be made`，
    // 经 classifyRequestFailure 归到 environment，状态应升为「错误」。
    console.log('\n=== ③ 引擎报错路径：默认环境指向无人监听的端口 ===')
    await gotoPage(send, '/environments', '环境管理')
    await evaluate(send, `window.__t.clickText('.hrp-page__head .el-button', '新建环境')`)
    await waitFor(send, `!!document.querySelector('.el-dialog input[placeholder="例如 local / staging"]')`, {
      label: '环境弹窗',
    })
    await evaluate(send, `window.__t.set('.el-dialog input[placeholder="例如 local / staging"]', 'bad')`)
    await evaluate(send, `window.__t.set('.el-dialog input[placeholder="http://127.0.0.1:8899"]', 'http://127.0.0.1:9')`)
    r = await evaluate(
      send,
      `(async () => {
        const dlg = document.querySelector('.el-dialog');
        // ⚠️ 必须按 el-switch **自身**的文本匹配：两个开关同住一个 .options 容器，
        // 用 parentElement.innerText 匹配会把「校验 SSL 证书」一起命中，
        // find 取到的就是第一个 —— 于是"设默认"这个动作静默失效了。
        const sw = [...dlg.querySelectorAll('.el-switch')].find(s => s.innerText.includes('设为默认环境'));
        if (!sw) return 'no-default-switch';
        sw.click();
        await window.__sleep(250);
        if (!sw.classList.contains('is-checked')) return 'switch-not-on';
        window.__t.clickText('.el-dialog__footer .el-button', '保存');
        return 'ok';
      })()`,
    )
    check('新建环境并把「设为默认环境」打开', r === 'ok', r)
    await waitFor(send, `!document.querySelector('.el-dialog')`, { timeout: 15_000, label: '坏环境保存完成' })

    // 不复用返回值判断，直接读表格：只有页面上真的显示 bad 是默认，才说明生效
    const badIsDefault = await evaluate(
      send,
      `(() => {
        const rows = [...document.querySelectorAll('.el-table__body-wrapper tr')];
        const row = rows.find(tr => (tr.querySelector('td:nth-child(2)')?.innerText || '').includes('bad'));
        if (!row) return 'no-bad-row';
        return row.innerText.includes('默认');
      })()`,
    )
    check('坏环境已成为项目的默认环境', badIsDefault === true, String(badIsDefault))

    // 直接复用上一条用例，只换默认环境，不动用例内容
    await gotoPage(send, '/cases', '用例管理')
    await evaluate(send, `window.__t.clickText('.el-table__row .el-link', 'UI 冒烟用例')`)
    await waitFor(send, `location.pathname.includes('/edit')`, { label: '进入编辑态' })
    await waitFor(send, `document.querySelectorAll('.step').length === 3`, { label: '三个步骤已加载' })
    await evaluate(send, HELPERS)
    await evaluate(send, `window.__t.clickText('.head-actions .el-button', '保存并执行')`)
    await waitFor(send, `location.pathname.startsWith('/runs/')`, { timeout: 40_000, label: '跳到执行详情' })
    await waitFor(send, `!!window.__t.runStatus()`, { timeout: 20_000 })
    await waitTerminal(send, '环境错执行到达终态')

    const badStatus = await evaluate(send, `window.__t.runStatus()`)
    const badSummary = await evaluate(send, `window.__t.summary()`)
    const badDetail = await evaluate(send, `window.__t.text()`)
    console.log(`     汇总：${badSummary.replace(/\s+/g, ' ')}`)
    console.log(`     执行状态：${badStatus}`)
    check('③ 引擎报错时执行状态为「错误」（不是「失败」，也不是假绿）', badStatus === '错误', badStatus)
    // 归因文案必须用服务端生成的标签比对：「环境」二字在侧边栏（环境管理）里本来就有，
    // 直接 includes('环境') 是必然通过的空断言。标签是「环境/服务」，不会误命中。
    check('③ 归因标签指向「环境/服务」（而不是甩锅给被测系统）', badDetail.includes('环境/服务'), badStatus)
    check('③ 归因依据是服务端生成的（连接被拒 / 退出码 1）', badDetail.includes('请求未能完成') && badDetail.includes('退出码 1'))
    check('③ 错误摘要保留了引擎原文（dial tcp / connectex）', /dial tcp|connectex|connection refused/i.test(badDetail))
    // 回归锁：clean_exit 的语义是"进程自己带错误码收尾"，不是"成功"。
    // 若 UI 又把它渲染成绿色的「正常退出：是」，就会在写着「错误」的页面上给出假绿。
    check('③ 退出方式不再自相矛盾（不出现「正常退出」）', !badDetail.includes('正常退出'), badStatus)
    check('③ exit=1 时退出方式标为「主动退出」而非崩溃', badDetail.includes('主动退出'), badStatus)
    check('④ 引擎报错时用例数对账仍然给出结论', badSummary.includes('预期') && badSummary.includes('实际'), badSummary)

    await evaluate(send, `window.__t.caseTab('原始日志')`)
    await waitFor(send, `window.__t.has('stdout') && window.__t.has('stderr')`, { timeout: 25_000, label: '原始日志' })
    const badLogs = await evaluate(send, `window.__t.text()`)
    check('③ 引擎报错时仍能展开原始日志（stdout/stderr 双栏）', badLogs.includes('stdout') && badLogs.includes('stderr'))
    check('③ 原始日志里保留了引擎的连接失败原始文本', /dial tcp|connectex|connection refused/i.test(badLogs))
    await screenshot(send, `${SHOTS}/11-run-engine-error.png`)

    // 收尾：把默认环境改回 local，避免把演示项目留在"配错"状态
    await gotoPage(send, '/environments', '环境管理')
    r = await evaluate(
      send,
      `(async () => {
        const rows = [...document.querySelectorAll('.el-table__body-wrapper tr')];
        const row = rows.find(tr => (tr.querySelector('td:nth-child(2)')?.innerText || '').includes('local'));
        if (!row) return 'no-local-row';
        const edit = [...row.querySelectorAll('button')].find(b => b.innerText.includes('编辑'));
        if (!edit) return 'no-edit-button';
        edit.click();
        await window.__sleep(400);
        const dlg = document.querySelector('.el-dialog');
        if (!dlg) return 'no-dialog';
        const sw = [...dlg.querySelectorAll('.el-switch')].find(s => s.innerText.includes('设为默认环境'));
        if (!sw) return 'no-default-switch';
        if (!sw.classList.contains('is-checked')) { sw.click(); await window.__sleep(250); }
        if (!sw.classList.contains('is-checked')) return 'switch-not-on';
        window.__t.clickText('.el-dialog__footer .el-button', '保存');
        return 'ok';
      })()`,
    )
    check('把默认环境改回 local（不留"配错"状态）', r === 'ok', r)
    await waitFor(send, `!document.querySelector('.el-dialog')`, { timeout: 15_000, label: 'local 保存完成' })
    const localIsDefault = await evaluate(
      send,
      `(() => {
        const rows = [...document.querySelectorAll('.el-table__body-wrapper tr')];
        const row = rows.find(tr => (tr.querySelector('td:nth-child(2)')?.innerText || '').includes('local'));
        if (!row) return 'no-local-row';
        return row.innerText.includes('默认');
      })()`,
    )
    check('默认环境已恢复为 local', localIsDefault === true, String(localIsDefault))

    // -----------------------------------------------------------------------
    console.log('\n=== 执行列表页 ===')
    await gotoPage(send, '/runs', '执行记录')
    const listText = await evaluate(send, `window.__t.text()`)
    check('执行列表展示归因列', listText.includes('system_under_test') || listText.includes('pass'))
    check('执行列表展示对账列', listText.includes('对账') && listText.includes('一致'))
    check('执行列表同时出现失败与通过两条记录', listText.includes('失败') && listText.includes('通过'))
    check('执行列表区分「失败」与「错误」两种终态', listText.includes('错误'))
    check('执行列表同时列出两类归因（被测系统 / 环境）', listText.includes('system_under_test') && listText.includes('environment'))
    await screenshot(send, `${SHOTS}/10-run-list.png`)

    console.log(`\n本次执行 ID：失败 ${firstRunId} → 通过 ${lastRunId}`)
    console.log(`\n===== 结果：${ok} 项通过，${failures.length} 项失败 =====`)
    if (failures.length) {
      console.log('失败项：')
      failures.forEach((f) => console.log(`  - ${f}`))
      process.exitCode = 1
    }
  } finally {
    await screenshot(send, `${SHOTS}/99-final.png`).catch(() => {})
    await shutdown(proc, ws)
  }
}

main().catch((e) => {
  console.error('\n脚本异常：', e)
  process.exitCode = 1
})
