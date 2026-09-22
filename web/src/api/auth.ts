import { req } from './client'
import type { ChangePasswordReq, Principal } from './types'

/** 登录。会话以 HttpOnly Cookie（hrp_session）下发。 */
export function login(username: string, password: string): Promise<Principal> {
  return req<Principal>({ method: 'POST', url: '/auth/login', data: { username, password } })
}

export function logout(): Promise<null> {
  return req<null>({ method: 'POST', url: '/auth/logout' })
}

/** 取当前登录主体。未登录时后端返回 HTTP 401。 */
export function me(): Promise<Principal> {
  return req<Principal>({ method: 'GET', url: '/auth/me' })
}

/**
 * 本人修改密码（需验旧密码）。
 *
 * 成功后**当前会话保留**、其它设备上的会话被吊销 —— 否则用户改完密码
 * 立刻被登出，会以为把账号改坏了。旧密码错误返回 40000。
 */
export function changePassword(data: ChangePasswordReq): Promise<null> {
  return req<null>({ method: 'POST', url: '/auth/password', data })
}
