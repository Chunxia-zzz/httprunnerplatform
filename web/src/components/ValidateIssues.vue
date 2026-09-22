<script setup lang="ts">
/**
 * 静态校验结果展示。
 *
 * 校验分两级：error 阻断执行/保存语义，warning 只是提醒。
 * 契约要求（接口契约 4 节）前端必须按 code 分支处理，
 * 所以这里对已知 code 额外补一段"为什么"—— 那些原因都来自引擎实测结论，
 * 光看一句 message 用户没法判断该不该改。
 */
import { computed } from 'vue'

import { ISSUE_CODE_HINT, type ValidateIssue, type ValidateOutcome } from '@/api'

const props = defineProps<{
  outcome: ValidateOutcome | null
  loading?: boolean
  error?: string
  /** 还没保存过（没有用例 ID）时无法校验 */
  disabledReason?: string
}>()

const errors = computed(() => (props.outcome?.issues ?? []).filter((i) => i.level === 'error'))
const warnings = computed(() => (props.outcome?.issues ?? []).filter((i) => i.level !== 'error'))

function scopeLabel(issue: ValidateIssue): string {
  if (issue.scope === 'step') return issue.seq > 0 ? `第 ${issue.seq} 步` : '步骤'
  if (issue.scope === 'case') return '用例'
  return issue.scope || '用例'
}

function fieldLabel(issue: ValidateIssue): string {
  return issue.field ? `${scopeLabel(issue)} · ${issue.field}` : scopeLabel(issue)
}
</script>

<template>
  <div v-loading="loading" class="vi">
    <el-alert v-if="error" type="error" show-icon :closable="false" :title="error" />

    <div v-else-if="disabledReason" class="hrp-empty-hint">{{ disabledReason }}</div>

    <template v-else-if="outcome">
      <el-alert
        :type="outcome.ok ? 'success' : 'error'"
        show-icon
        :closable="false"
        :title="
          outcome.ok
            ? `校验通过${warnings.length ? `（有 ${warnings.length} 条提醒）` : ''}`
            : `校验未通过：${errors.length} 个错误、${warnings.length} 个提醒`
        "
      >
        <template v-if="outcome.ok && warnings.length" #default>
          提醒不影响执行，但值得看一眼。
        </template>
      </el-alert>

      <div v-if="outcome.issues.length" class="vi__list">
        <div v-for="(issue, i) in outcome.issues" :key="i" class="vi__item">
          <div class="vi__head">
            <el-tag size="small" :type="issue.level === 'error' ? 'danger' : 'warning'" effect="dark">
              {{ issue.level === 'error' ? '错误' : '提醒' }}
            </el-tag>
            <span class="vi__code hrp-mono">{{ issue.code }}</span>
            <span class="hrp-muted vi__field">{{ fieldLabel(issue) }}</span>
          </div>
          <div class="vi__msg">{{ issue.message }}</div>
          <div v-if="issue.hint" class="vi__hint">建议：{{ issue.hint }}</div>
          <div v-if="ISSUE_CODE_HINT[issue.code]" class="vi__why">为什么：{{ ISSUE_CODE_HINT[issue.code] }}</div>
        </div>
      </div>

      <div v-else class="hrp-empty-hint">没有任何问题。</div>
    </template>

    <div v-else class="hrp-empty-hint">点「保存并校验」查看结果。</div>
  </div>
</template>

<style scoped>
.vi__list {
  margin-top: 10px;
}

.vi__item {
  border: 1px solid var(--hrp-border);
  border-radius: 4px;
  padding: 10px 12px;
  margin-bottom: 8px;
  background: #fff;
}

.vi__head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.vi__code {
  font-size: 12px;
  color: #303133;
}

.vi__field {
  font-size: 12px;
}

.vi__msg {
  margin-top: 6px;
  line-height: 1.6;
}

.vi__hint,
.vi__why {
  margin-top: 4px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--hrp-muted);
}
</style>
