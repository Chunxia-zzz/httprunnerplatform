import { defineStore } from 'pinia'

import { authApi, UNAUTHORIZED_EVENT, type Principal } from '@/api'

interface State {
  principal: Principal | null
  /** 是否已尝试过恢复会话。避免每次路由跳转都打一次 /auth/me */
  resolved: boolean
  loading: boolean
}

/**
 * 登录态。
 *
 * 会话本体是 HttpOnly Cookie，前端拿不到也不该拿；这里只缓存
 * 「当前是谁」用于渲染顶栏与做路由守卫，真实鉴权始终在后端。
 */
export const useAuthStore = defineStore('auth', {
  state: (): State => ({
    principal: null,
    resolved: false,
    loading: false,
  }),

  getters: {
    isLoggedIn: (s) => !!s.principal,
    isAdmin: (s) => s.principal?.role === 'admin',
    displayName: (s) => s.principal?.nickname || s.principal?.username || '',
  },

  actions: {
    async login(username: string, password: string) {
      this.loading = true
      try {
        this.principal = await authApi.login(username, password)
        this.resolved = true
      } finally {
        this.loading = false
      }
    },

    /**
     * 探测会话是否还有效。
     *
     * 401 属于预期结果（用户压根没登录），不往上抛 ——
     * 否则路由守卫每次冷启动都会收到一个未捕获的 rejection。
     */
    async resolve() {
      if (this.resolved) return
      this.loading = true
      try {
        this.principal = await authApi.me()
      } catch {
        this.principal = null
      } finally {
        this.resolved = true
        this.loading = false
      }
    },

    async logout() {
      try {
        await authApi.logout()
      } catch {
        // 后端可能已经把会话清掉了；本地状态照样要清干净。
      }
      this.principal = null
      this.resolved = true
    },

    clear() {
      this.principal = null
      this.resolved = true
    },
  },
})

/**
 * 注册全局 401 监听。
 *
 * http 层不 import store（避免循环依赖），改为广播事件，由这里落地。
 * 必须在 main.ts 里调用一次。
 */
export function initUnauthorizedListener(onUnauthorized: () => void) {
  window.addEventListener(UNAUTHORIZED_EVENT, () => {
    const store = useAuthStore()
    store.clear()
    onUnauthorized()
  })
}
