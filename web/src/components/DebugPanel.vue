<script setup lang="ts">
/**
 * 单步调试面板（M3）。
 *
 * 渲染后端 /cases/{id}/debug 返回的结构化结果。与执行详情页的 StepDetail 不同：
 * 调试结果用的是 parser 的**原始**字段名（`name` / `request` / `response`），
 * 而 StepDetail 用的是落库后的视图字段（`step_name` / `request_snapshot` / ...）。
 * 两者内容同源，但形状不同，所以这里单独渲染，不复用 StepDetail。
 *
 * 关键的产品语义（要如实呈现给用户）：
 *   - 单步调试实际执行了「前置步骤 + 目标步骤」的前缀，不是只跑一步。
 *     否则依赖上一步提取 token 的步骤会拿到空变量、断言必失败。
 *   - 目标步骤会被高亮标记（is_target）。
 */
import { computed } from 'vue'

import type { DebugStepResult } from '@/api'
import { stepStatusMeta, stepTypeLabel } from '@/utils/dict'
import { formatDuration, prettyValue } from '@/utils/format'

const props = defineProps<{
  result: DebugStepResult | null
  loading: boolean
  error: string
}>()

const statusMeta = computed(() =>
  props.result ? stepStatusMeta(props.result.status) : null,
)

const targetStep = computed(() =>
  props.result?.steps.find((s) => s.is_target) ?? null,
)

const rebuiltCount = computed(
  () => props.result?.steps.reduce((n, s) => n + (s.assertions ?? []).filter((a) => a.rebuilt).length, 0) ?? 0,
)
</script>

<template>
  <div class="dp">
    <!-- 未调试时的引导 -->
    <div v-if="!result && !loading && !error" class="dp__empty">
      <p>在左侧步骤卡片上点「调试此步」，平台会临时构造一个只含「前置步骤 + 该步骤」的最小用例并真实执行。</p>
      <p class="hrp-muted">
        不是只跑一步 —— 若该步骤引用了上一步提取的变量（如 <code>$token</code>），
        只跑它会拿到空值、断言必失败。所以平台会真实执行前缀步骤，拿到真实变量值。
      </p>
    </div>

    <div v-if="loading" class="dp__loading">正在执行调试用例…</div>
    <el-alert v-if="error" type="error" show-icon :closable="false" title="调试失败">{{ error }}</el-alert>

    <!-- 整体状态 -->
    <div v-if="result" class="dp__summary">
      <el-tag size="small" :type="statusMeta?.type" effect="dark">{{ statusMeta?.label }}</el-tag>
      <span class="hrp-muted dp__meta">耗时 {{ formatDuration(result.duration_ms) }}</span>
      <span class="hrp-muted dp__meta">实际执行 {{ result.actual_step_count }} 步（含前置）</span>
      <el-tag v-if="rebuiltCount > 0" size="small" type="warning" effect="dark">
        {{ rebuiltCount }} 条断言为平台重建
      </el-tag>
    </div>

    <el-alert
      v-if="result && result.panic"
      type="warning"
      show-icon
      :closable="false"
      class="dp__alert"
      title="引擎在断言阶段崩溃"
    >
      这是引擎 v4.3.6 的已知缺陷（断言不通过会 panic，而非正常返回失败）。平台已用原始报文重建了失败明细。
    </el-alert>

    <el-alert
      v-if="result && result.compile_error"
      type="error"
      show-icon
      :closable="false"
      class="dp__alert"
      title="临时用例编译失败"
    >
      {{ result.compile_error }}
    </el-alert>

    <el-alert
      v-if="result && result.error_msg"
      type="error"
      show-icon
      :closable="false"
      class="dp__alert"
      title="执行错误"
    >
      {{ result.error_msg }}
    </el-alert>

    <!-- 目标步骤高亮提示 -->
    <el-alert
      v-if="targetStep"
      type="info"
      :closable="false"
      class="dp__alert"
      :title="`目标步骤：#${targetStep.seq} ${targetStep.name || ''}`"
    >
      下方列表中标记「目标」的就是你要调试的那一步；它之前的步骤是为提供真实变量值而执行的前置。
    </el-alert>

    <!-- 步骤列表 -->
    <div v-if="result" class="dp__steps">
      <div
        v-for="s in result.steps"
        :key="s.seq"
        class="dp__step"
        :class="{ 'dp__step--target': s.is_target }"
      >
        <div class="dp__step-head">
          <el-tag size="small" type="info" effect="plain">#{{ s.seq }}</el-tag>
          <span class="dp__step-name">{{ s.name || `步骤 ${s.seq}` }}</span>
          <el-tag v-if="s.is_target" size="small" type="primary" effect="dark">目标</el-tag>
          <el-tag size="small" effect="plain">{{ stepTypeLabel(s.step_type) }}</el-tag>
          <el-tag size="small" :type="stepStatusMeta(s.status).type" effect="dark">
            {{ stepStatusMeta(s.status).label }}
          </el-tag>
          <span class="hrp-muted dp__meta">{{ formatDuration(s.elapsed_ms) }}</span>
        </div>

        <el-alert v-if="s.error_msg" type="error" show-icon :closable="false" class="dp__alert" title="步骤错误">
          {{ s.error_msg }}
        </el-alert>

        <div v-if="s.final_url" class="dp__url hrp-mono">{{ s.final_url }}</div>

        <el-row :gutter="12" class="dp__snapshots">
          <el-col :span="12">
            <div class="dp__block-title">请求报文</div>
            <template v-if="s.request">
              <div class="hrp-mono dp__kv">{{ s.request.method }} {{ s.request.url }}</div>
              <pre v-if="s.request.headers" class="hrp-pre">{{ prettyValue(s.request.headers) }}</pre>
              <pre v-if="s.request.body" class="hrp-pre">{{ prettyValue(s.request.body) }}</pre>
            </template>
            <div v-else class="hrp-muted dp__none">无请求报文快照</div>
          </el-col>
          <el-col :span="12">
            <div class="dp__block-title">响应报文</div>
            <template v-if="s.response">
              <div class="dp__kv">
                <el-tag
                  size="small"
                  :type="(s.response.status_code ?? 0) >= 400 ? 'danger' : 'success'"
                  effect="plain"
                >
                  {{ s.response.status_code ?? '—' }}
                </el-tag>
                <span class="hrp-muted hrp-mono">{{ s.response.proto }}</span>
              </div>
              <pre v-if="s.response.headers" class="hrp-pre">{{ prettyValue(s.response.headers) }}</pre>
              <pre v-if="s.response.body" class="hrp-pre">{{ prettyValue(s.response.body) }}</pre>
            </template>
            <div v-else class="hrp-muted dp__none">无响应报文快照（请求可能没发出去）</div>
          </el-col>
        </el-row>

        <template v-if="s.extract_result && Object.keys(s.extract_result).length">
          <div class="dp__block-title">提取的变量</div>
          <pre class="hrp-pre">{{ prettyValue(s.extract_result) }}</pre>
        </template>

        <div class="dp__block-title">断言明细</div>
        <el-table v-if="s.assertions?.length" :data="s.assertions" border size="small">
          <el-table-column prop="seq" label="#" width="46" />
          <el-table-column prop="check_expr" label="检查表达式" min-width="140">
            <template #default="{ row }"><span class="hrp-mono">{{ row.check_expr }}</span></template>
          </el-table-column>
          <el-table-column prop="assert_method" label="方法" width="96">
            <template #default="{ row }"><span class="hrp-mono">{{ row.assert_method }}</span></template>
          </el-table-column>
          <el-table-column label="期望值" min-width="110">
            <template #default="{ row }">
              <span class="hrp-mono">{{ row.expect_value }}</span>
              <span v-if="row.expect_value_type" class="hrp-muted dp__type">({{ row.expect_value_type }})</span>
            </template>
          </el-table-column>
          <el-table-column label="实际值" min-width="110">
            <template #default="{ row }">
              <span class="hrp-mono">{{ row.check_value }}</span>
              <span v-if="row.check_value_type" class="hrp-muted dp__type">({{ row.check_value_type }})</span>
            </template>
          </el-table-column>
          <el-table-column label="结论" width="82">
            <template #default="{ row }">
              <el-tag size="small" :type="row.passed ? 'success' : 'danger'" effect="plain">
                {{ row.passed ? '通过' : '未通过' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="来源" width="100">
            <template #default="{ row }">
              <el-tag v-if="row.rebuilt" size="small" type="warning" effect="dark">平台重建</el-tag>
              <span v-else class="hrp-muted">引擎</span>
            </template>
          </el-table-column>
        </el-table>
        <div v-else class="hrp-muted dp__none">无断言明细</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.dp {
  min-height: 160px;
}

.dp__empty,
.dp__loading {
  padding: 20px 4px;
  color: var(--hrp-muted);
  line-height: 1.8;
  font-size: 13px;
}

.dp__summary {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.dp__meta {
  font-size: 12px;
}

.dp__alert {
  margin: 8px 0;
}

.dp__steps {
  margin-top: 8px;
}

.dp__step {
  border: 1px solid var(--hrp-border);
  border-radius: 4px;
  padding: 10px 12px;
  margin-bottom: 10px;
  background: #fff;
}

.dp__step--target {
  border-color: var(--el-color-primary);
  border-left-width: 3px;
}

.dp__step-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}

.dp__step-name {
  font-weight: 600;
}

.dp__url {
  font-size: 12px;
  word-break: break-all;
  margin-bottom: 8px;
}

.dp__snapshots {
  margin-bottom: 4px;
}

.dp__block-title {
  font-weight: 600;
  font-size: 13px;
  margin: 8px 0 4px;
}

.dp__kv {
  font-size: 12px;
  word-break: break-all;
  margin-bottom: 6px;
}

.dp__none {
  font-size: 12px;
  line-height: 1.7;
  padding: 2px 0 6px;
}

.dp__type {
  font-size: 11px;
  margin-left: 4px;
}
</style>
