import { mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { describe, expect, it } from 'vitest'

import type { StepResult } from '@/api'
import StepDetail from '../StepDetail.vue'

/**
 * StepDetail 的「不假绿」展示契约（docs/接口契约.md 第 8 节 / BUG-003）：
 *
 *   - final_url_source='reconstructed' → URL 旁出现橙色「平台重建」标注；
 *   - 'summary' → 灰色「引擎」标注；
 *   - 空（未知来源）→ **不标注也不推测**（存量数据没有该列值）。
 *
 * 配色/文案属于「不假绿」口径的一部分：重建值绝不能看起来和引擎权威值一样可信。
 */

function mountStep(over: Partial<StepResult>) {
  const step = {
    id: 1,
    run_id: 1,
    case_result_id: 1,
    case_code: 'tc_1',
    seq: 1,
    step_name: '登录',
    step_type: 'request',
    status: 'pass',
    inferred_failed: false,
    final_url: 'https://api.example.com/v2/listings/',
    final_url_source: '',
    request_snapshot: null,
    response_snapshot: null,
    elapsed_ms: 123,
    extract_result: {},
    error_msg: '',
    assertions: [],
    ...over,
  } as StepResult
  return mount(StepDetail, {
    props: { step },
    global: { plugins: [ElementPlus] },
  })
}

describe('StepDetail 最终 URL 来源标注', () => {
  it('reconstructed → 显示橙色「平台重建」', () => {
    const w = mountStep({ final_url_source: 'reconstructed' })
    // el-tag 被 transition 包裹，自定义类在外层；配色断言直接找修饰类。
    const tag = w.find('.el-tag--warning')
    expect(tag.exists()).toBe(true)
    expect(tag.text()).toBe('平台重建')
    expect(tag.classes().join(' ')).toContain('el-tag--dark') // effect=dark：视觉上与引擎值可区分
  })

  it('summary → 显示「引擎」（普通样式，与重建值视觉可区分）', () => {
    const w = mountStep({ final_url_source: 'summary' })
    expect(w.find('.el-tag--warning').exists()).toBe(false)
    const tag = w.findAll('.el-tag').find((t) => t.text() === '引擎')
    expect(tag).toBeDefined()
    expect(tag!.classes().join(' ')).toContain('el-tag--plain') // effect=plain
  })

  it('来源为空（存量数据）→ 不标注也不推测', () => {
    const w = mountStep({ final_url_source: '' })
    expect(w.find('.sd__url-src').exists()).toBe(false)
    // URL 本身仍要展示
    expect(w.text()).toContain('https://api.example.com/v2/listings/')
  })

  it('没有 URL 的步骤不出现标注位', () => {
    const w = mountStep({ final_url: '', final_url_source: 'summary' })
    expect(w.find('.sd__url-src').exists()).toBe(false)
  })
})

describe('StepDetail 步骤头部', () => {
  it('展示序号、名称、耗时', () => {
    const w = mountStep({ seq: 3, step_name: '查询标的', elapsed_ms: 1842 })
    expect(w.text()).toContain('#3')
    expect(w.text()).toContain('查询标的')
    expect(w.text()).toContain('1.8') // formatDuration 的输出（秒级显示）
  })

  it('inferred_failed=true 出现引擎崩溃解释告警', () => {
    const w = mountStep({ inferred_failed: true })
    expect(w.text()).toContain('未正常结束')
  })
})
