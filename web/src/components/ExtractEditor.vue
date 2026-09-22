<script setup lang="ts">
/**
 * 提取配置器。
 *
 * 编译结果：`extract: { token: "body.token" }`，即 `<object>.<expression>`。
 * `status_code` / `proto` 是标量对象、没有子路径，编译器会直接用对象名，
 * 所以这两类的取值路径输入框要禁用（见 compiler.renderExtractExpr）。
 */
import { EXTRACT_OBJECTS, type ExtractItem } from '@/api'

const model = defineModel<ExtractItem[]>({ required: true })

function add() {
  model.value = [...model.value, { name: '', object: 'body', expression: '' }]
}

function remove(i: number) {
  const next = [...model.value]
  next.splice(i, 1)
  model.value = next
}

/** status_code / proto 不需要取值路径。 */
function needsExpression(object: string): boolean {
  return object !== 'status_code' && object !== 'proto'
}

/** 在 UI 上实时展示编译器会产出什么，减少"填了但不生效"的困惑。 */
function preview(item: ExtractItem): string {
  const obj = (item.object || '').trim()
  const expr = (item.expression || '').trim()
  if (!obj) return expr || '—'
  if (!needsExpression(obj)) return obj
  if (!expr) return obj
  return `${obj}.${expr.replace(/^\./, '')}`
}
</script>

<template>
  <div class="ex">
    <div v-if="model.length === 0" class="hrp-muted ex__empty">未配置提取</div>

    <div v-for="(item, i) in model" :key="i" class="ex__row">
      <el-input v-model="item.name" placeholder="变量名" class="ex__name hrp-mono" />
      <el-select v-model="item.object" class="ex__object" placeholder="对象">
        <el-option v-for="o in EXTRACT_OBJECTS" :key="o" :label="o" :value="o" />
      </el-select>
      <el-input
        v-model="item.expression"
        :disabled="!needsExpression(item.object)"
        :placeholder="needsExpression(item.object) ? '取值路径，如 token 或 args.token' : '（该对象无需路径）'"
        class="ex__expr hrp-mono"
      />
      <el-tooltip :content="`编译为 ${preview(item)}`" placement="top">
        <span class="ex__preview hrp-mono hrp-muted">{{ preview(item) }}</span>
      </el-tooltip>
      <el-button link type="danger" @click="remove(i)">
        <el-icon><Delete /></el-icon>
      </el-button>
    </div>

    <el-button link type="primary" @click="add">
      <el-icon><Plus /></el-icon>添加提取
    </el-button>
  </div>
</template>

<style scoped>
.ex__row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.ex__name {
  width: 160px;
  flex: none;
}

.ex__object {
  width: 130px;
  flex: none;
}

.ex__expr {
  flex: 1;
  min-width: 160px;
}

.ex__preview {
  width: 170px;
  flex: none;
  font-size: 11px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.ex__empty {
  font-size: 12px;
  padding-bottom: 6px;
}
</style>
