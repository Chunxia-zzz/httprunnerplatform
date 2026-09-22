import { req } from './client'
import type { ID, PageData, Suite, SuiteMember, SuiteReq } from './types'

/**
 * 用例集 API。
 *
 * 成员是**全量替换**语义（PUT 一个有序 ID 数组），不是"单独增删某一条"：
 * 数组顺序就是执行顺序，单独增删的接口表达不了顺序，最终一定会演化成
 * "先删光再加回来"，不如一开始就这么定义。
 */
export function listSuites(
  projectId: ID,
  params?: { keyword?: string; page?: number; page_size?: number },
): Promise<PageData<Suite>> {
  return req<PageData<Suite>>({
    method: 'GET',
    url: `/projects/${projectId}/suites`,
    params,
  })
}

export function getSuite(id: ID): Promise<Suite> {
  return req<Suite>({ method: 'GET', url: `/suites/${id}` })
}

export function createSuite(projectId: ID, data: SuiteReq): Promise<Suite> {
  return req<Suite>({ method: 'POST', url: `/projects/${projectId}/suites`, data })
}

/** 更新用例集。code 不可改（会进入执行历史与产物文件名）。 */
export function updateSuite(id: ID, data: SuiteReq): Promise<Suite> {
  return req<Suite>({ method: 'PUT', url: `/suites/${id}`, data })
}

/** 删除用例集。被测试计划引用时后端返回 40003。 */
export function deleteSuite(id: ID): Promise<null> {
  return req<null>({ method: 'DELETE', url: `/suites/${id}` })
}

/** 成员列表（含"这条现在能不能跑"的标注）。 */
export function listSuiteMembers(id: ID): Promise<SuiteMember[]> {
  return req<SuiteMember[]>({ method: 'GET', url: `/suites/${id}/cases` })
}

/**
 * 全量替换成员。传空数组表示清空 —— 因此不能把"空数组"当成"没传"。
 */
export function setSuiteMembers(id: ID, caseIds: ID[]): Promise<SuiteMember[]> {
  return req<SuiteMember[]>({ method: 'PUT', url: `/suites/${id}/cases`, data: { case_ids: caseIds } })
}
