import { req } from './client'
import type {
  CreateUserReq,
  ID,
  PageData,
  ResetPasswordReq,
  UpdateUserReq,
  User,
  UserListQuery,
} from './types'

/**
 * 账号管理接口。
 *
 * 整个模块的所有端点都由后端 `middleware.RequireAdmin()` 把关，
 * 非管理员一律 40300 —— 前端隐藏入口只是为了不给出"点了会失败"的按钮，
 * 真实的权限判定始终在后端。
 */

export function listUsers(q: UserListQuery = {}): Promise<PageData<User>> {
  return req<PageData<User>>({ method: 'GET', url: '/users', params: q })
}

/** 新建账号。用户名重复返回 40001，密码不合规返回 40000。 */
export function createUser(data: CreateUserReq): Promise<User> {
  return req<User>({ method: 'POST', url: '/users', data })
}

/**
 * 修改昵称 / 角色 / 启用状态。
 *
 * 改角色或启用状态会让后端**立即吊销该用户的全部会话**（不是因为
 * 这是修饰性改动，而是会话表里缓存着角色，不吊销的话被降级的管理员
 * 在 TTL 内仍然是管理员）。
 */
export function updateUser(id: ID, data: UpdateUserReq): Promise<User> {
  return req<User>({ method: 'PUT', url: `/users/${id}`, data })
}

/** 软删除账号，同时吊销其全部会话。不能删自己，也不能删掉最后一个可用管理员。 */
export function deleteUser(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/users/${id}` })
}

/** 管理员重置他人密码（无需旧密码），并吊销对方全部会话。不能作用于自己。 */
export function resetPassword(id: ID, data: ResetPasswordReq): Promise<null> {
  return req<null>({ method: 'POST', url: `/users/${id}/password`, data })
}
