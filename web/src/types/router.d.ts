import 'vue-router'

/**
 * 路由 meta 的显式类型。
 *
 * 不加这个声明时 `RouteMeta` 是 `Record<string, unknown>`，
 * `to.meta.admni`（拼错）能通过编译、只在运行时静默失效 ——
 * 而路由守卫恰恰是"静默失效"代价最高的地方。
 */
declare module 'vue-router' {
  interface RouteMeta {
    /** 公开页面（登录页）：不要求会话。 */
    public?: boolean
    /** 仅管理员可访问；非管理员会被重定向回用例页并收到提示。 */
    admin?: boolean
    /** 浏览器标题。 */
    title?: string
  }
}
