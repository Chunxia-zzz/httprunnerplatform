<script setup lang="ts">
/**
 * 键值对编辑器。
 *
 * 用表格而不是 textarea 让用户手写 `k=v`：环境的 environs 会被渲染成
 * `.env` 文件，值里出现换行或 `#` 会让整行被引擎当成注释/截断，
 * 而那种错误在页面上完全看不出来。结构化输入从源头避免这类问题。
 */
import type { KVRow } from '@/utils/format'

const model = defineModel<KVRow[]>({ required: true })

withDefaults(
  defineProps<{
    keyPlaceholder?: string
    valuePlaceholder?: string
    /** 敏感值（掩码显示）——提示用户可以留空不改 */
    maskedHint?: boolean
    disabled?: boolean
  }>(),
  {
    keyPlaceholder: '变量名',
    valuePlaceholder: '值',
    maskedHint: false,
    disabled: false,
  },
)

function add() {
  model.value = [...model.value, { key: '', value: '' }]
}

function remove(index: number) {
  const next = [...model.value]
  next.splice(index, 1)
  model.value = next
}
</script>

<template>
  <div class="kv">
    <div v-if="model.length === 0" class="kv__empty hrp-muted">暂无内容</div>

    <div v-for="(row, i) in model" :key="i" class="kv__row">
      <el-input
        v-model="row.key"
        :placeholder="keyPlaceholder"
        :disabled="disabled"
        class="kv__key hrp-mono"
      />
      <span class="kv__eq">=</span>
      <el-input
        v-model="row.value"
        :placeholder="valuePlaceholder"
        :disabled="disabled"
        class="kv__value hrp-mono"
      />
      <el-button link type="danger" :disabled="disabled" @click="remove(i)">
        <el-icon><Delete /></el-icon>
      </el-button>
    </div>

    <div class="kv__footer">
      <el-button link type="primary" :disabled="disabled" @click="add">
        <el-icon><Plus /></el-icon>添加一项
      </el-button>
      <span v-if="maskedHint" class="hrp-muted kv__hint">
        值为 <code>***</code> 表示当前账号无权查看原文；保持不动即可保留原值。
      </span>
    </div>
  </div>
</template>

<style scoped>
.kv__row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.kv__key {
  width: 220px;
  flex: none;
}

.kv__eq {
  color: var(--hrp-muted);
  font-family: var(--hrp-mono);
}

.kv__value {
  flex: 1;
}

.kv__empty {
  font-size: 12px;
  padding: 4px 0 8px;
}

.kv__footer {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.kv__hint {
  font-size: 12px;
}

.kv__hint code {
  font-family: var(--hrp-mono);
  background: #f2f3f5;
  border-radius: 3px;
  padding: 0 3px;
}
</style>
