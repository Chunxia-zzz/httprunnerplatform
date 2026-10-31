import { describe, expect, it } from 'vitest'

import { attrTagType, ATTR_TAG_TYPE, caseStatusMeta, stepStatusMeta, stepTypeLabel } from '../dict'

/**
 * dict.ts 的字典测试。
 *
 * 这些字典是「前端不硬编码归因文案」约定的一部分：文案一律取服务端，
 * 前端只负责配色与兜底。字典一旦漂移（枚举拼错、兜底丢失），
 * 页面上会出现灰色未知标签或直接渲染出英文枚举值。
 */

describe('ATTR_TAG_TYPE 归因配色', () => {
  it('覆盖全部 8 个归因枚举，与 model.go 常量一一对应', () => {
    // 枚举清单以 internal/model/model.go 的归因常量为准。
    expect(Object.keys(ATTR_TAG_TYPE).sort()).toEqual(
      ['canceled', 'case_issue', 'environment', 'ops', 'pass', 'system_under_test', 'timeout', 'unknown'].sort(),
    )
  })

  it('配色语义正确：通过绿、被测系统红、其余警告/中性', () => {
    expect(ATTR_TAG_TYPE.pass).toBe('success')
    expect(ATTR_TAG_TYPE.system_under_test).toBe('danger')
    expect(ATTR_TAG_TYPE.environment).toBe('warning')
    expect(ATTR_TAG_TYPE.unknown).toBe('info')
  })

  it('attrTagType 对未知归因兜底为 info，不抛错', () => {
    expect(attrTagType('no_such_attr')).toBe('info')
    expect(attrTagType('')).toBe('info')
  })
})

describe('stepStatusMeta 步骤状态', () => {
  it('已知状态有中文标签', () => {
    for (const s of ['pass', 'fail', 'error', 'skipped']) {
      expect(stepStatusMeta(s).label).not.toBe(s) // 必须翻译过
    }
  })

  it('未知状态兜底显示原值且不抛错', () => {
    expect(stepStatusMeta('weird').label).toBe('weird')
    expect(stepStatusMeta('').label).toBe('未知')
  })
})

describe('stepTypeLabel 步骤类型', () => {
  it('request 是「请求」；M1 未开放的类型仍保留映射（裁剪口径）', () => {
    expect(stepTypeLabel('request')).toBe('请求')
    expect(stepTypeLabel('websocket')).toBe('WebSocket')
  })

  it('未知类型兜底显示原值', () => {
    expect(stepTypeLabel('future_type')).toBe('future_type')
  })
})

describe('caseStatusMeta 用例状态', () => {
  it('三档状态齐全，未知兜底', () => {
    expect(caseStatusMeta('active').label).toBe('启用')
    expect(caseStatusMeta('draft').label).toBe('草稿')
    expect(caseStatusMeta('disabled').label).toBe('禁用')
    expect(caseStatusMeta('nope').label).toBe('nope')
  })
})
