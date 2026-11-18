/**
 * 执行进度 WebSocket 客户端（F9）。
 *
 * 只做三件事：连上、把事件转出去、断线时告诉上层（由上层决定降级策略）。
 * 不在这里做重连：重连时机与轮询降级是同一个决策，放在页面里更好读。
 */
export interface RunEventPayload {
  run_id: number
  type: 'status' | 'case' | 'finished' | string
  status?: string
  total?: number
  case?: {
    code: string
    name: string
    status: string
    seq: number
    duration_ms: number
  }
  at: string
  finished: boolean
}

export interface RunSocketHandlers {
  onOpen?: () => void
  onEvent?: (ev: RunEventPayload) => void
  onClose?: () => void
}

/** 建立执行进度连接，返回关闭函数。同源 cookie 会被浏览器自动带上。 */
export function connectRunEvents(runId: number, handlers: RunSocketHandlers): () => void {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const ws = new WebSocket(`${proto}://${window.location.host}/api/v1/ws/runs/${runId}`)

  ws.onopen = () => handlers.onOpen?.()
  ws.onmessage = (msg) => {
    try {
      handlers.onEvent?.(JSON.parse(String(msg.data)) as RunEventPayload)
    } catch {
      // 非 JSON 帧直接忽略：服务端只发 JSON，出现异常帧说明链路有问题，
      // 但不能因此让页面抛错（下一次事件/轮询仍能对齐）。
    }
  }
  ws.onclose = () => handlers.onClose?.()
  ws.onerror = () => {
    // onerror 之后一定会有 onclose，这里不重复通知，避免双触发重连。
  }

  return () => {
    ws.onopen = null
    ws.onmessage = null
    ws.onclose = null
    ws.onerror = null
    if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) {
      ws.close()
    }
  }
}
