---
name: cdp-ui-smoke
description: 用本机 Chrome + Node 自带 WebSocket 做零依赖的浏览器端到端验收（CDP 驱动）。当需要验证一个 Web 应用"页面上真的能用"（不是接口通）——建/改/删的完整操作流、断言页面上的文案与状态、截图存证——且不想下载 Chromium（playwright / agent-browser 的 ~500MB）时使用。含 Element Plus 场景下已踩过的坑。
agent_created: true
---

# 零依赖 CDP 浏览器验收

## 何时用

- 需要**通过真实页面操作**验证功能（点击、输入、切页签），而不是直接打接口。
  接口全绿但页面不能用的情况，只有这条路能发现。
- 需要**截图存证**。
- 需要验证**登录态 / 权限 / 会话吊销**这类"只在页面上才看得见"的行为
  （如：被禁用的人是否立刻退回登录页、改密码后当前会话是否还活着）——
  接口全绿但用户被莫名登出，只有这条路能发现。见坑 12。
- 环境里已经有 Chrome / Edge，但**没有** playwright / agent-browser 的浏览器二进制，
  或者不愿意为一次验收下载几百 MB。
- 不适合：需要复杂选择器引擎、自动等待、录像、多浏览器矩阵的场景 —— 那就装 playwright。

## 为什么能零依赖

Node ≥ 22 提供**全局** `WebSocket` 和 `fetch`，而 Chrome DevTools Protocol 就是一个 WebSocket
上的 JSON-RPC。于是"启动 Chrome → 读 DOM → 点击 → 截图"全部靠内置能力完成。

`assets/cdp.mjs` 是可直接复用的驱动，导出：

| 函数 | 说明 |
|:---|:---|
| `launchChrome()` | headless=new + `--remote-debugging-port=9333` + 临时 user-data-dir，轮询 `/json/version` 就绪 |
| `connect()` | 取第一个 `type==='page'` 的 target，连 ws，开 `Page`/`Runtime`，返回 `{ ws, send }` |
| `evaluate(send, expr)` | `Runtime.evaluate` + `awaitPromise` + `returnByValue`，异常会带上页面内堆栈 |
| `waitFor(send, expr, {timeout,interval,label})` | 轮询直到真值，超时把**最后一次结果**打进错误里 |
| `goto(send, url)` | `Page.navigate`，然后等 `#app` 有子节点（SPA 别等 networkidle：长轮询页面永远不 idle） |
| `screenshot(send, path)` | `captureBeyondViewport: true`，整页而不只是视口 |
| `shutdown(proc, ws?)` | 关浏览器；`ws` 可省（只传 `proc` 也能跑） |

依赖：只 `import` 上面的模块即可，`node script.mjs` 直接跑。

## 关键坑（都是实际踩出来的，按出现频率排序）

### 1. 整页导航会清掉注入的页面内助手

`goto()` 之后 `window.__t` 就没了（整页导航换了执行上下文）。
把"注入助手"包进 `gotoPage()` 里，**每次导航后重新注入**：

```js
async function gotoPage(send, path, waitText) {
  await goto(send, `${BASE}${path}`)
  await evaluate(send, HELPERS)          // ← 必须重新注入
  await waitFor(send, `window.__t.has(${JSON.stringify(waitText)})`)
}
```

### 2. Element Plus 的 `.el-switch` 会误命中：必须按开关**自身**文本匹配

同一容器里的多个开关共享父元素：

```html
<div class="options">
  <div class="el-switch">…校验 SSL 证书</div>
  <div class="el-switch">…设为默认环境</div>
</div>
```

`find(s => s.parentElement.innerText.includes('设为默认环境'))` 会**同时命中两个**，
`find` 取到第一个 → 点的是"校验 SSL"，而"设为默认"这个动作**静默失效**，
脚本还返回 `ok`（因为读的是第一个开关的 `is-checked`）。

```js
// ✅ 文本就在 .el-switch 根节点内部
const sw = [...dlg.querySelectorAll('.el-switch')].find(s => s.innerText.includes('设为默认环境'))
```

并且**不要用返回值判断成功**，要回读表格/接口确认副作用真的落地：
```js
const row = rows.find(tr => tr.innerText.includes('bad'))
check(row?.includes('默认'))
```

### 3. 断言别用"页面里本来就有"的子串

`pageText.includes('环境')` 在侧边栏写着「环境管理」的应用里**必然通过**，是个空断言。
用**服务端生成的完整文案**（如标签 `环境/服务`、依据 `请求未能完成`）比对。

同理 `includes('失败')` 之类在长页面里几乎恒真。优先：
- 限定容器（`.summary__item` / 具体卡片）再取文本；
- 比对唯一的完整短语。

⭐ 变体：**"限定容器"之前，先确认这个 class 有没有被复用**。
`.hrp-card` 这类通用卡片类往往被"执行概要"和"用例结果"同时用上，
`document.querySelectorAll('.hrp-card')[0]` 会稳定地取到概要卡（它渲染在前面），
于是所有针对用例卡片的断言**全失败，且失败得很像"功能没做"**。
用卡片内部**独有**的元素反查：

```js
// ✅ 只留真正的用例卡片（内部有 .case-head），而不是靠下标猜
caseCards: () => [...document.querySelectorAll('.hrp-card')]
  .filter(c => c.querySelector('.case-head'))
```

判别信号：取到的 `alertText` 是**空串**。空串会让 `!includes('X')` 这类**否定断言假通过**，
所以别看到"否定断言过了"就以为修复生效——对关键字段要**同时**断言它非空：

### 4. 断言可能跑在异步状态落地之前

探针类 UI（引擎状态、连接状态）是异步请求回来的。登录后立刻断言标签存在会偶发失败。
在 `login()` 里补一句等待：`waitFor(send, "window.__t.has('引擎 v4')")`。

⭐ 变体：**成功提示是"异步操作开始"的信号，不是"完成"的信号**。
保存后 `notifyOk()` 先弹、列表 `load()` 后到 —— 等 message 再读表格读到的是旧数据
（曾把 `case_count=0` 误判成保存失败）。断言要等**数据本身**变化：

```js
// ❌ 等 success message 后立刻读列表 → 读到旧值
await waitFor(send, `!!document.querySelector('.el-message--success')`)
check(rowCells(...).includes('3'))
// ✅ 直接等目标状态
await waitFor(send, `window.__t.rowCells('su1')?.includes('3')`)
```

### 5. 脚本要能重复运行：唯一性约束会咬人

第二次运行常撞 `40001 已存在`（项目标识、用例编码、环境名）。
给所有唯一字段挂时间戳后缀：

```js
const SUFFIX = String(Date.now()).slice(-6)
const PROJECT_CODE = `ui_demo_${SUFFIX}`
```

⚠️ 定义了常量要**真的用上** —— 只写在文件顶部的 `const` 不算（曾因此白跑一轮）。

### 5b. 被测地址从环境变量取，别写死

```js
const BASE = process.env.HRP_UI_BASE || 'http://127.0.0.1:5173'
```

同一套脚本既能打前端 dev server，也能打**后端内嵌前端后的单文件二进制**。
这不是洁癖：像 `go:embed` / SPA fallback / 静态托管这类改动，
"只证明 dev server 还能用"等于没验证。能一键换靶子，这类改动才算真验过。

### 5c. 后台服务必须用工具的 run_in_background 起

用 `(cmd &)` 或 `nohup ... &` 起服务，**命令一结束进程就被回收**。
失败表现极具误导性：后续 `curl` 拿到
`502 upstream connect failed`（沙箱出网代理在目标端口无监听时的报错），
看起来像"服务返回了 502"，实际是服务早死了 —— 会让人去查一个不存在的后端 bug。

关掉占端口的进程也不要 `taskkill /IM node.exe` / `pkill node`
（会连带杀掉 MCP 等其它 node 进程）。按端口找 PID：

```bash
PID=$(netstat -ano | grep -E "TCP.*:5173.*LISTENING" | head -1 | awk '{print $NF}')
taskkill //F //PID "$PID"      # Git Bash 下 / 要写成 //
```

### 6. 助手用元素引用，不要拼 `:nth-of-type`

同级 div 里插进来一个 alert，`nth-of-type` 就整体错位。
把 `step(i)` / `pane(root)` / `setEl(el, v)` 这类**基于元素引用**的助手注入页面。

### 7. Element Plus 表格作用域插槽把 row 擦成索引签名对象

在 TS 里会报 `DefaultRow is not assignable to XXX`。别把签名放宽成 `any`
（等于关掉类型检查），加一个收窄入口：
```ts
function asRow(row: unknown): Xxx { return row as Xxx }
```

⚠️ **模板里的 `as` 断言不管用**。`@click="openEdit(row as User)"` 与
`v-if="pred(row)"` 都会报同一个 `DefaultRow` 错误 —— `as` 在模板表达式里
不会被 vue-tsc 应用，所以**每一个传参点**都要用 helper 包一层：

```vue
<el-button @click="openEdit(asRow(row))">编辑</el-button>
<el-tooltip v-if="isLastAdmin(asRow(row))">…</el-tooltip>
```

不要把被调用方的形参放宽成 `unknown` 来"一次解决"——那等于把类型检查
从函数体里挪走，而且以后每个调用方都白拿一张通行证。

### 8. 截图缩放后**不要靠肉眼读小标签**

1600px 的截图在预览里缩小后，`system_under_test ×1` 会看成 `pass ×1`，
接着就会去查一个不存在的 bug。**以 DOM 文本为准**：写个单点诊断脚本（见下），
把目标容器的 `innerText` 原样打印出来。数据库/SQLite 是分歧时的最终仲裁。

### 9. `el-alert` 不能同时用默认插槽和 `description` prop

会报 `Default slot already specified`。二选一。

### 10. `window.open(url)` 别写在模板内联箭头函数里

抽成方法，否则某些场景下不生效。

### 11. 截图前必须等动画落地

Element Plus 的 `el-dialog` / `el-message-box` 有约 0.3s 淡入。
`waitFor` 只等到"元素存在"，此时截出来是一张**半透明、什么都读不出来**的图 ——
看着像 UI 坏了，实际只是拍早了。

```js
async function shot(send, path) { await sleep(600); await screenshot(send, path) }
```

判断依据：如果截图上文字若隐若现、弹窗边缘发虚，那是动画没结束，
**不要去查 CSS/主题的 bug**。

### 12. 单浏览器模拟多设备：靠 CDP 换 Cookie

验证"吊销会话 / 改密码踢掉其它设备"这类功能时，两个标签页共用一份 Cookie，
天然只能同时存在一个会话。用 CDP 手动搬运 Cookie，就能在一个浏览器里
拿到 N 份互不干扰的会话：

```js
await send('Network.enable')                        // ← getCookies/setCookie 必须先开这个域

const getCookie = async () => {
  const { cookies } = await send('Network.getCookies', { urls: [BASE] })
  return cookies.find(c => c.name === 'hrp_session')?.value ?? null
}
const useCookie = async (v) => {
  await send('Network.clearBrowserCookies')
  if (v) await send('Network.setCookie', { name: 'hrp_session', value: v, url: BASE, path: '/', httpOnly: true })
}
```

流程：`登录 A → 存 cookieA → 换 cookieB 登录 B → 存 cookieB →
换回 cookieA 断言仍可用 → 用 B 触发吊销 → 换回 cookieA 断言已被踢`。

⭐ 关键细节：**别用 UI 的"退出登录"来切换账号** —— 那会在服务端删掉上一个会话，
后面就分不清"是被吊销了"还是"是自己登出了"。用 `setCookie` 直接换。

`HttpOnly` 的 Cookie JS 读不到（`document.cookie` 拿不到），所以必须走 CDP。

### 13. 弹窗/表格行的点击套路

```js
// ElMessageBox.confirm 的确认框
const box = document.querySelector('.el-message-box')
;[...box.querySelectorAll('button')].find(b => b.innerText.trim() === '确认变更')
  || box.querySelector('.el-button--primary')     // 兜底：文案变了也能点对

// 下拉菜单项（el-dropdown 的菜单被 teleport 到 body，不在触发元素里）
[...document.querySelectorAll('.el-dropdown-menu__item')]
  .find(i => i.innerText.trim() === '修改密码').click()

// el-switch 的开关态读 class，不要读 aria
const on = document.querySelector('.el-switch').classList.contains('is-checked')

// 表单按 label 找输入框（比 nth-of-type 稳；必填星号是伪元素，不在 innerText 里）
[...root.querySelectorAll('.el-form-item')]
  .find(i => i.querySelector('.el-form-item__label')?.innerText.replace(/\*/g,'').trim() === '原密码')
  ?.querySelector('input')
```

Vue 的 `v-model` 要派发 `input` 事件才生效（直接赋 `el.value` 不触发）：

```js
Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(el, v)
el.dispatchEvent(new Event('input', { bubbles: true }))
```

### 14. 切换"全局上下文"（当前项目等）：走 localStorage + reload，别点 el-select

顶部项目选择器这类全局状态，点下拉切换有三个坑：
dropdown 是 teleport 的，`.el-select-dropdown__item` 会**同时命中页面上其它 select**
（分页、筛选）的选项节点；当前值可能已经是目标值（点了等于没切，后续断言全歪）；
列表异步加载时下拉里根本没有目标项。

而这类状态通常持久化在 localStorage —— 直接写它再刷新，store 初始化时就会选中：

```js
await evaluate(send, `(() => {
  localStorage.setItem('hrp.currentProjectId', String(${projectId}))
  location.reload()
  return 'ok'
})()`)
// 刷新后等顶栏渲染出目标项目再继续
await waitFor(send, `(document.querySelector('.layout__header-left')?.innerText || '').includes('目标项目code')`)
```

### 15. `goto` 的 waitText 不能用与侧边栏菜单同名的词

SPA 的侧边栏**永远**有「测试计划」「CI 令牌」这类菜单项，`waitText: '测试计划'`
在页面还没加载时就已通过 —— 下一步找页面上的按钮必然 `undefined.click()`。
waitText 用**页面副标题等正文内容**（如「计划 = 一组用例集」），或干脆显式等操作按钮就绪：

```js
await waitFor(send, `!!window.__t.btn(document, '新建计划')`, { label: '按钮就绪' })
```

### 16. headless 里 `navigator.clipboard` 默认被拒 → stub 掉

验收"复制成功后行为变化"（如复制按钮变「已复制」、关窗不再确认）时，
headless Chrome 没有剪贴板权限，`writeText` 走 catch，`copied` 永远是 false ——
这不是产品 bug，是环境。注入 stub 验证**产品逻辑**（剪贴板权限本身不是被测对象）：

```js
await evaluate(send, `(() => {
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: () => Promise.resolve() }, configurable: true,
  })
  return 'ok'
})()`)
```

### 17. try/finally + `process.exit` 会吞异常：必须显式 catch

骨架里只有 `finally` 没有 `catch` 时，脚本中途抛的异常会被 `finally` 里的
`process.exit(...)` 在传播前截断 —— 表现是"打印了 N 项通过就静默结束，
exit code 还是 0，什么错误信息都没有"（比失败更糟：看起来像正常跑完）。
try 后面**永远**补上：

```js
} catch (e) {
  failures.push(`脚本异常：${e.message}`)
  console.log(`\n[异常] ${e.stack}`)
} finally {
  ...
  process.exit(failures.length ? 1 : 0)
}
```

## 单点诊断脚本

排查"页面到底渲染成什么了"时，比反复重跑全量脚本快得多：

```js
const out = await evaluate(send, `JSON.stringify(
  [...document.querySelectorAll('.summary__item')].map(el => ({
    label: el.querySelector('.summary__label')?.innerText.trim(),
    value: el.querySelector('.summary__value')?.innerText.replace(/\\s+/g,' ').trim(),
  })), null, 2)`)
console.log(out)
```

注意：某个字段为空**不一定是 bug** —— 先确认模板里那一项有没有 `.summary__value` 包裹层。

## 脚本骨架

```js
import { connect, evaluate, goto, launchChrome, screenshot, shutdown, waitFor } from './cdp.mjs'

let ok = 0
const failures = []
function check(name, cond, extra = '') {
  if (cond) { ok += 1; console.log(`  ✅ ${name}`) }
  else { failures.push(name); console.log(`  ❌ ${name}${extra ? ` — ${extra}` : ''}`) }
}

const { proc } = await launchChrome()
const { send } = await connect()
try {
  // 1) 登录（含等待异步状态落地）
  // 2) 按业务流程逐步操作，每步 check + 关键节点 screenshot
  // 3) 只读的最终核对：回到列表页确认副作用可见
} catch (e) {
  failures.push(`脚本异常：${e.message}`)
  console.log(`\n[异常] ${e.stack}`)
} finally {
  await shutdown(proc)
  console.log(`\n===== ${ok} 项通过，${failures.length} 项失败 =====`)
  failures.forEach((f) => console.log(`  - ${f}`))
  process.exit(failures.length ? 1 : 0)   // ← 让 CI 能感知失败
}
```

## 收尾

- **失败要 exit 非 0**，否则自动化里永远是绿的。
- 打印每个关键中间结果（汇总行、iframe 的 `src`、真实执行的 ID），
  出问题时不用重跑就能定位。
- 落库核对：UI 说的和 DB 里的是否一致，用 SQLite 直查一次（字段名与 API 的 JSON
  不一定同名，表里也可能**只有派生字段的原料**，如只有 `step_total`/`step_passed`
  而 `step_failed`/`step_error` 是 API 层算出来的）。
