import { req } from './client'
import type { Principal } from './types'

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
