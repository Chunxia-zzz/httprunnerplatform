import { defineStore } from 'pinia'

import { engineApi, type EngineStatus } from '@/api'

interface State {
  status: EngineStatus | null
  /** 探测失败（网络/服务不可达）与「引擎不可用」是两件事，分开存 */
  probeError: string
  loading: boolean
  probedAt: number
}

/**
 * 引擎可用性。
 *
 * ⚠️ 后端在引擎不可用时返回 **HTTP 200 + code=50001**，且 `req()` 会把它
 * 抛成 ApiError。因此判断"引擎能不能用"必须同时看两个来源：
 *   - 请求成功 → 读 `status.available`
 *   - 请求抛错且 code=50001 → 引擎不可用，data 里带 error 说明
 * 把后者当成"接口坏了"会导致告警条消失，而用户恰恰最需要看到它。
 */
export const useEngineStore = defineStore('engine', {
  state: (): State => ({
    status: null,
    probeError: '',
    loading: false,
    probedAt: 0,
  }),

  getters: {
    available: (s) => s.status?.available === true,
  },

  actions: {
    async probe() {
      this.loading = true
      this.probeError = ''
      try {
        this.status = await engineApi.engineStatus()
      } catch (e) {
        const err = e as { code?: number; message?: string; data?: unknown }
        if (err?.code === 50001) {
          // 引擎不可用：接口本身是成功的，data 里带 available=false
          const data = (err.data ?? {}) as Partial<EngineStatus>
          this.status = { ...data, available: false, error: err.message }
        } else {
          this.status = null
          this.probeError = err?.message || '无法连接后端服务'
        }
      } finally {
        this.probedAt = Date.now()
        this.loading = false
      }
    },
  },
})
