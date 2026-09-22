<script setup lang="ts">
/**
 * 步骤级结果详情。
 *
 * 三条契约要求在这里落地（docs/接口契约.md 第 8 节）：
 *   1. **展示最终生效 URL，同时给出声明值**：引擎在 URL 不带查询串时会自动
 *      补结尾斜杠（实测 A1），只显示声明值会让用户拿着 404 完全看不出原因。
 *   2. **HTML 报告可能不存在** —— 由父组件处理，这里只负责把 has_report 透出。
 *   3. **`rebuilt=true` 的断言不是引擎结论**：断言失败时引擎 panic 且不产出任何
 *      明细（实测 F8），这些行是平台用「声明的断言 + stdout 报文快照」重建的，
 *      必须显式标注，否则用户会误以为引擎给出了该判定。
 */
import { computed } from 'vue'

import type { StepResult } from '@/api'
import { stepStatusMeta, stepTypeLabel } from '@/utils/dict'
import { formatDuration, prettyValue } from '@/utils/format'

const props = defineProps<{
  step: StepResult
  /** 用例里声明的 URL（来自用例定义），用于和最终 URL 对比 */
  declaredUrl?: string
}>()

const statusMeta = computed(() => stepStatusMeta(props.step.status))

/**
 * 判断"最终 URL"是否与声明值不同。
 *
 * 只做字符串比较：差异必然来自引擎的 URL 归一化（补斜杠），
 * 而不是平台的推断，所以这里不做任何猜测式的等价判断。
 */
const urlChanged = computed(() => {
  const declared = (props.declaredUrl ?? '').trim()
  const final = (props.step.final_url ?? '').trim()
  if (!declared || !final) return false
  // 声明的是相对路径，最终是绝对地址：只比较路径与查询串部分
  const tail = (u: string) => {
    try {
      const parsed = new URL(u)
      return `${parsed.pathname}${parsed.search}`
    } catch {
      return u
    }
  }
  return tail(declared) !== tail(final)
})

const hasRequest = computed(() => !!props.step.request_snapshot)
const hasResponse = computed(() => !!props.step.response_snapshot)
const extractEntries = computed(() => Object.entries(props.step.extract_result ?? {}))
const rebuiltCount = computed(() => (props.step.assertions ?? []).filter((a) => a.rebuilt).length)
</script>

<template>
  <div class="sd">
    <div class="sd__head">
      <el-tag size="small" type="info" effect="plain">#{{ step.seq }}</el-tag>
      <span class="sd__name">{{ step.step_name || `步骤 ${step.seq}` }}</span>
      <el-tag size="small" effect="plain">{{ stepTypeLabel(step.step_type) }}</el-tag>
      <el-tag size="small" :type="statusMeta.type" effect="dark">{{ statusMeta.label }}</el-tag>
      <span class="hrp-muted sd__elapsed">{{ formatDuration(step.elapsed_ms) }}</span>
    </div>

    <el-alert
      v-if="step.inferred_failed"
      type="warning"
      show-icon
      :closable="false"
      class="sd__alert"
      title="该步骤未正常结束（引擎在断言阶段崩溃）"
    >
      引擎的 stderr 里出现了这一步的 <code>run step start</code> 但没有配对的
      <code>run step end</code>，这是平台判定"失败发生在这一步"的依据。该步骤的报文快照仍然完整可用
      —— panic 发生在请求之后。
    </el-alert>

    <el-alert v-if="step.error_msg" type="error" show-icon :closable="false" class="sd__alert" title="步骤错误">
      {{ step.error_msg }}
    </el-alert>

    <div class="sd__urls">
      <div class="sd__url-row">
        <span class="sd__url-label">最终生效 URL</span>
        <span class="hrp-mono sd__url-value">{{ step.final_url || '（无）' }}</span>
      </div>
      <div v-if="declaredUrl" class="sd__url-row">
        <span class="sd__url-label">用例声明</span>
        <span class="hrp-mono hrp-muted sd__url-value">{{ declaredUrl }}</span>
      </div>
      <el-alert v-if="urlChanged" type="warning" show-icon :closable="false" class="sd__alert">
        <template #title>最终 URL 与声明值不同</template>
        最可能的原因是引擎给不带查询串的 URL 补了结尾斜杠（实测 A1）：声明
        <code>{{ declaredUrl }}</code> 实际请求 <code>{{ step.final_url }}</code>。
        路径多一个 <code>/</code> 很容易让服务端返回 404 —— 若响应是 404，先怀疑这里。
      </el-alert>
    </div>

    <el-row :gutter="12" class="sd__snapshots">
      <el-col :span="12">
        <div class="sd__block-title">
          请求报文<el-tag size="small" type="info" effect="plain" class="ml4">来自 stdout 快照</el-tag>
        </div>
        <template v-if="hasRequest">
          <div class="sd__kv">
            <span class="hrp-mono">{{ step.request_snapshot?.method }}</span>
            <span class="hrp-mono">{{ step.request_snapshot?.url }}</span>
          </div>
          <div v-if="step.request_snapshot?.headers" class="sd__sub">请求头</div>
          <pre v-if="step.request_snapshot?.headers" class="hrp-pre">{{ prettyValue(step.request_snapshot?.headers) }}</pre>
          <div v-if="step.request_snapshot?.body" class="sd__sub">请求体</div>
          <pre v-if="step.request_snapshot?.body" class="hrp-pre">{{ prettyValue(step.request_snapshot?.body) }}</pre>
        </template>
        <div v-else class="hrp-muted sd__none">该步骤没有请求报文快照</div>
      </el-col>

      <el-col :span="12">
        <div class="sd__block-title">
          响应报文<el-tag size="small" type="info" effect="plain" class="ml4">来自 stdout 快照</el-tag>
        </div>
        <template v-if="hasResponse">
          <div class="sd__kv">
            <el-tag
              size="small"
              :type="(step.response_snapshot?.status_code ?? 0) >= 400 ? 'danger' : 'success'"
              effect="plain"
            >
              {{ step.response_snapshot?.status_code ?? '—' }}
            </el-tag>
            <span class="hrp-muted hrp-mono">{{ step.response_snapshot?.proto }}</span>
          </div>
          <div v-if="step.response_snapshot?.headers" class="sd__sub">响应头</div>
          <pre v-if="step.response_snapshot?.headers" class="hrp-pre">{{ prettyValue(step.response_snapshot?.headers) }}</pre>
          <div v-if="step.response_snapshot?.body" class="sd__sub">响应体</div>
          <pre v-if="step.response_snapshot?.body" class="hrp-pre">{{ prettyValue(step.response_snapshot?.body) }}</pre>
        </template>
        <div v-else class="hrp-muted sd__none">该步骤没有响应报文快照（请求可能根本没发出去）</div>
      </el-col>
    </el-row>

    <template v-if="extractEntries.length">
      <div class="sd__block-title">提取结果</div>
      <pre class="hrp-pre">{{ prettyValue(Object.fromEntries(extractEntries)) }}</pre>
    </template>

    <div class="sd__block-title">
      断言明细
      <el-tag v-if="rebuiltCount > 0" size="small" type="warning" effect="dark" class="ml4">
        {{ rebuiltCount }} 条为平台重建
      </el-tag>
    </div>
    <el-table v-if="step.assertions?.length" :data="step.assertions" border size="small">
      <el-table-column prop="seq" label="#" width="50" />
      <el-table-column prop="check_expr" label="检查表达式" min-width="150">
        <template #default="{ row }"><span class="hrp-mono">{{ row.check_expr }}</span></template>
      </el-table-column>
      <el-table-column prop="assert_method" label="校验方法" width="100">
        <template #default="{ row }"><span class="hrp-mono">{{ row.assert_method }}</span></template>
      </el-table-column>
      <el-table-column label="期望值" min-width="120">
        <template #default="{ row }">
          <span class="hrp-mono">{{ row.expect_value }}</span>
          <span v-if="row.expect_value_type" class="hrp-muted sd__type">({{ row.expect_value_type }})</span>
        </template>
      </el-table-column>
      <el-table-column label="实际值" min-width="120">
        <template #default="{ row }">
          <span class="hrp-mono">{{ row.check_value }}</span>
          <span v-if="row.check_value_type" class="hrp-muted sd__type">({{ row.check_value_type }})</span>
        </template>
      </el-table-column>
      <el-table-column label="结论" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.passed ? 'success' : 'danger'" effect="plain">
            {{ row.passed ? '通过' : '未通过' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="来源" width="110">
        <template #default="{ row }">
          <el-tooltip
            v-if="row.rebuilt"
            content="该结论不是引擎给出的：断言失败时引擎 panic，不产出任何明细。这一行是平台用「用例声明的断言 + stdout 报文快照」自行比对重建的。"
            placement="top"
          >
            <el-tag size="small" type="warning" effect="dark">平台重建</el-tag>
          </el-tooltip>
          <span v-else class="hrp-muted">引擎</span>
        </template>
      </el-table-column>
    </el-table>
    <div v-else class="hrp-muted sd__none">
      该步骤没有断言明细。若整条用例通过，明细来自引擎的 validate 日志；若引擎崩在断言阶段，
      则由平台按声明重建（本步骤没有声明断言时为空）。
    </div>
  </div>
</template>

<style scoped>
.sd {
  border: 1px solid var(--hrp-border);
  border-radius: 4px;
  padding: 12px 14px;
  margin-bottom: 12px;
  background: #fff;
}

.sd__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.sd__name {
  font-weight: 600;
}

.sd__elapsed {
  font-size: 12px;
}

.sd__alert {
  margin: 8px 0;
}

.sd__urls {
  margin-bottom: 10px;
}

.sd__url-row {
  display: flex;
  gap: 8px;
  align-items: baseline;
  margin-bottom: 4px;
}

.sd__url-label {
  flex: none;
  width: 88px;
  font-size: 12px;
  color: var(--hrp-muted);
}

.sd__url-value {
  font-size: 12px;
  word-break: break-all;
}

.sd__snapshots {
  margin-bottom: 6px;
}

.sd__block-title {
  font-weight: 600;
  font-size: 13px;
  margin: 10px 0 6px;
}

.sd__kv {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 6px;
  font-size: 12px;
  word-break: break-all;
}

.sd__sub {
  font-size: 12px;
  color: var(--hrp-muted);
  margin: 6px 0 4px;
}

.sd__none {
  font-size: 12px;
  line-height: 1.7;
  padding: 4px 0 8px;
}

.sd__type {
  font-size: 11px;
  margin-left: 4px;
}

.ml4 {
  margin-left: 4px;
}
</style>
