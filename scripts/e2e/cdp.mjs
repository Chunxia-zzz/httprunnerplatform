/**
 * 极简 CDP 驱动（零依赖）。
 *
 * 为什么不装 agent-browser / playwright：
 *   本机已有 Chrome，而 Node 22 自带全局 WebSocket。
 *   直接用 Chrome DevTools Protocol 就能完成"打开页面 → 读 DOM → 点击/输入 → 截图"，
 *   省掉 ~500MB 的 Chromium 下载与一次耗时十几分钟的 npm 安装。
 *
 * 用法：作为模块被 ui-smoke.mjs 引入（launchChrome / connect / goto / evaluate / waitFor / screenshot / shutdown）。
 */
import { spawn } from 'node:child_process'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const CHROME = 'C:/Program Files/Google/Chrome/Application/chrome.exe'
const PORT = 9333

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms))
}

/** 启动 headless Chrome 并等待调试端口就绪。 */
export async function launchChrome() {
  const profile = mkdtempSync(join(tmpdir(), 'hrp-cdp-'))
  const proc = spawn(
    CHROME,
    [
      '--headless=new',
      '--disable-gpu',
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-extensions',
      '--window-size=1600,1000',
      `--remote-debugging-port=${PORT}`,
      `--user-data-dir=${profile}`,
      'about:blank',
    ],
    { stdio: 'ignore', detached: false },
  )

  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    try {
      const resp = await fetch(`http://127.0.0.1:${PORT}/json/version`)
      if (resp.ok) return { proc, profile }
    } catch {
      // 端口还没起来
    }
    await sleep(200)
  }
  proc.kill()
  throw new Error('Chrome 调试端口在 30 秒内没有就绪')
}

/** 连到第一个 page target。 */
export async function connect() {
  const resp = await fetch(`http://127.0.0.1:${PORT}/json/list`)
  const targets = await resp.json()
  const page = targets.find((t) => t.type === 'page')
  if (!page) throw new Error('没有可用的 page target')

  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((resolve, reject) => {
    ws.onopen = resolve
    ws.onerror = (e) => reject(new Error(`WebSocket 连接失败: ${e?.message ?? e}`))
  })

  let id = 0
  const pending = new Map()
  ws.onmessage = (ev) => {
    const msg = JSON.parse(ev.data)
    if (msg.id && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id)
      pending.delete(msg.id)
      if (msg.error) reject(new Error(`${msg.error.message} (${JSON.stringify(msg.error)})`))
      else resolve(msg.result)
    }
  }

  const send = (method, params = {}) =>
    new Promise((resolve, reject) => {
      id += 1
      pending.set(id, { resolve, reject })
      ws.send(JSON.stringify({ id, method, params }))
    })

  await send('Page.enable')
  await send('Runtime.enable')
  await send('Emulation.setDeviceMetricsOverride', {
    width: 1600,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false,
  })

  return { ws, send }
}

/** 在页面里求值并取回 JSON 结果。 */
export async function evaluate(send, expression) {
  const res = await send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  })
  if (res.exceptionDetails) {
    const d = res.exceptionDetails
    throw new Error(`页面内求值异常：${d.exception?.description ?? d.text}`)
  }
  return res.result.value
}

/** 轮询等待条件成立。 */
export async function waitFor(send, expression, { timeout = 15_000, interval = 200, label = '' } = {}) {
  const deadline = Date.now() + timeout
  let last
  while (Date.now() < deadline) {
    try {
      last = await evaluate(send, expression)
      if (last) return last
    } catch (e) {
      last = e.message
    }
    await sleep(interval)
  }
  throw new Error(`等待超时${label ? `（${label}）` : ''}：${expression}；最后一次结果=${JSON.stringify(last)}`)
}

export async function goto(send, url) {
  await send('Page.navigate', { url })
  // Vue SPA：等 #app 挂载完成即可，不用等 networkidle（长轮询页面永远不 idle）
  await waitFor(send, `document.querySelector('#app')?.children.length > 0`, {
    timeout: 20_000,
    label: `挂载 ${url}`,
  })
}

export async function screenshot(send, path) {
  const res = await send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true })
  writeFileSync(path, Buffer.from(res.data, 'base64'))
  return path
}

/** 关闭浏览器。 */
export async function shutdown(proc, ws) {
  try {
    ws?.close()
  } catch {
    /* ignore */
  }
  try {
    await fetch(`http://127.0.0.1:${PORT}/json/close/__none__`).catch(() => {})
  } catch {
    /* ignore */
  }
  proc?.kill()
}
