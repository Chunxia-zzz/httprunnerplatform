<script setup lang="ts">
/**
 * 单个步骤编辑器。
 *
 * 直接原地修改传入的 step 对象。Vue 只对「给 prop 本身重新赋值」告警，
 * 改动对象的属性是允许的；这里之所以这么做，是因为步骤内容嵌套很深，
 * 每层都 emit 一遍会让「表单改一个字要穿过 5 个事件」——那种代码无法维护。
 */
import { computed } from 'vue'

import { BODY_TYPES } from '@/api'
import AssertEditor from '@/components/AssertEditor.vue'
import ExtractEditor from '@/components/ExtractEditor.vue'
import KeyValueEditor from '@/components/KeyValueEditor.vue'
import { willAppendTrailingSlash, type EditorStep } from '@/types/editor'
import { HTTP_METHODS } from '@/utils/dict'

const props = defineProps<{
  step: EditorStep
  index: number
  total: number
}>()

const emit = defineEmits<{
  (e: 'remove', index: number): void
  (e: 'duplicate', index: number): void
  (e: 'move', payload: { from: number; to: number }): void
}>()

const slashWarning = computed(() => willAppendTrailingSlash(props.step.url, props.step.params))

/** requested 状态：JSON 文本是否可以解析。用来在保存前拦住低级错误。 */
const jsonError = computed(() => {
  if (props.step.bodyType !== 'json') return ''
  const t = props.step.bodyJson.trim()
  if (!t) return ''
  try {
    JSON.parse(t)
    return ''
  } catch (e) {
    return (e as Error).message
  }
})

const bodyTypeHint = computed(() => {
  switch (props.step.bodyType) {
    case 'json':
      return '编译器写成 `json:`，引擎自动补 Content-Type: application/json; charset=utf-8。'
    case 'form':
      return '编译器写成 `data:` 并强制补 Content-Type: application/x-www-form-urlencoded（除非你在请求头里显式指定了）。不给这个头，引擎会把 map 序列化成 JSON 而不是表单。'
    case 'raw':
      return '编译器写成 `data: "<字符串>"`。Content-Type 需要你自己在请求头里指定。'
    default:
      return '不带请求体。'
  }
})

function formatJson() {
  const t = props.step.bodyJson.trim()
  if (!t) return
  try {
    props.step.bodyJson = JSON.stringify(JSON.parse(t), null, 2)
  } catch {
    // 解析失败就保持原样，错误提示已经由 jsonError 展示
  }
}
</script>

<template>
  <el-card shadow="never" class="step" :class="{ 'step--off': !step.enabled }">
    <div class="step__head">
      <div class="step__title">
        <el-tag size="small" type="info" effect="plain">#{{ index + 1 }}</el-tag>
        <el-input v-model="step.name" placeholder="步骤名称（留空则由编译器生成「步骤 N」）" class="step__name" />
        <el-switch v-model="step.enabled" active-text="启用" inline-prompt />
        <span v-if="!step.enabled" class="hrp-muted step__off-hint">
          禁用步骤不会出现在编译产物里（引擎没有"跳过步骤"语法）
        </span>
      </div>
      <div class="step__actions">
        <el-button link :disabled="index === 0" @click="emit('move', { from: index, to: index - 1 })">
          <el-icon><Top /></el-icon>
        </el-button>
        <el-button link :disabled="index === total - 1" @click="emit('move', { from: index, to: index + 1 })">
          <el-icon><Bottom /></el-icon>
        </el-button>
        <el-button link @click="emit('duplicate', index)">复制</el-button>
        <el-button link type="danger" @click="emit('remove', index)">删除</el-button>
      </div>
    </div>

    <el-divider class="step__divider" />

    <div class="row">
      <el-select v-model="step.method" class="row__method">
        <el-option v-for="m in HTTP_METHODS" :key="m" :label="m" :value="m" />
      </el-select>
      <el-input v-model="step.url" placeholder="相对路径（如 /get）或完整 URL" class="row__url hrp-mono" />
      <el-input-number v-model="step.timeout" :min="0" :max="3600" :step="1" class="row__timeout" />
      <span class="hrp-muted row__hint">请求超时（秒，0 表示不设置）</span>
    </div>

    <el-alert v-if="slashWarning" type="warning" show-icon :closable="false" class="step__alert">
      <template #title>引擎会补结尾斜杠</template>
      URL 不带查询串时，引擎会在发出前补一个 <code>/</code>：`$base_url/get` 实际请求的是
      <code>GET /get/</code>。这个行为无法用改写 YAML 绕开（实测 A1），修法是让 URL 带上真实查询串
      （加一个参数即可）。最终生效地址以执行详情里的「最终 URL」为准。
    </el-alert>

    <el-tabs class="step__tabs">
      <el-tab-pane label="请求头">
        <KeyValueEditor v-model="step.headers" key-placeholder="Header 名" />
        <div class="hrp-muted note">
          请求头按「环境全局头 → 用例头 → 步骤头」合并，后者覆盖前者（大小写不敏感去重）。这里只需写本步骤特有的。
        </div>
      </el-tab-pane>

      <el-tab-pane label="查询参数">
        <KeyValueEditor v-model="step.params" key-placeholder="参数名" />
        <div class="hrp-muted note">参数为空时，引擎会给 URL 补结尾斜杠（见上方提示）。</div>
      </el-tab-pane>

      <el-tab-pane label="请求体">
        <el-radio-group v-model="step.bodyType" class="body-type">
          <el-radio-button v-for="t in BODY_TYPES" :key="t" :value="t">{{ t }}</el-radio-button>
        </el-radio-group>
        <div class="hrp-muted note">{{ bodyTypeHint }}</div>

        <template v-if="step.bodyType === 'json'">
          <div class="body-tools">
            <el-button size="small" @click="formatJson">格式化</el-button>
            <span v-if="jsonError" class="body-error">JSON 非法：{{ jsonError }}</span>
          </div>
          <el-input v-model="step.bodyJson" type="textarea" :rows="7" class="hrp-mono" placeholder='{"username": "tester"}' />
        </template>

        <template v-else-if="step.bodyType === 'form'">
          <KeyValueEditor v-model="step.bodyForm" key-placeholder="字段名" />
        </template>

        <template v-else-if="step.bodyType === 'raw'">
          <el-input v-model="step.bodyRaw" type="textarea" :rows="6" class="hrp-mono" placeholder="原始请求体文本" />
        </template>
      </el-tab-pane>

      <el-tab-pane label="变量">
        <KeyValueEditor v-model="step.variables" key-placeholder="变量名" />
        <div class="hrp-muted note">步骤级变量，只在本步骤可见。编译为 teststep 的 variables。</div>
      </el-tab-pane>

      <el-tab-pane label="提取（Extract）">
        <ExtractEditor v-model="step.extract" />
      </el-tab-pane>

      <el-tab-pane label="断言（Validate）">
        <AssertEditor v-model="step.validate" />
        <div class="hrp-muted note">
          断言可以在「引用接口」的用例中复用；M1 不支持自定义失败提示（该字段会被编译器丢弃）。
        </div>
      </el-tab-pane>
    </el-tabs>
  </el-card>
</template>

<style scoped>
.step {
  margin-bottom: 12px;
}

.step--off {
  opacity: 0.72;
  background: #fbfbfc;
}

.step__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  flex-wrap: wrap;
}

.step__title {
  display: flex;
  align-items: center;
  gap: 10px;
  flex: 1;
  min-width: 320px;
}

.step__name {
  max-width: 320px;
}

.step__off-hint {
  font-size: 12px;
}

.step__actions {
  flex: none;
}

.step__divider {
  margin: 10px 0 12px;
}

.row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.row__method {
  width: 110px;
  flex: none;
}

.row__url {
  flex: 1;
  min-width: 220px;
}

.row__timeout {
  width: 140px;
  flex: none;
}

.row__hint {
  font-size: 12px;
}

.step__alert {
  margin-bottom: 10px;
}

.step__tabs :deep(.el-tabs__header) {
  margin-bottom: 12px;
}

.note {
  font-size: 12px;
  line-height: 1.6;
  margin-top: 6px;
}

.body-type {
  margin-bottom: 4px;
}

.body-tools {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 8px 0;
}

.body-error {
  color: var(--el-color-danger);
  font-size: 12px;
}
</style>
