<script setup lang="ts">
/**
 * 断言配置器。
 *
 * 编译结果固定是「单键映射：校验方法 → [检查表达式, 期望值]」：
 *
 *     validate:
 *         - eq: ["status_code", 200]
 *
 * 两个必须让用户知道的事实：
 *   1. **期望值必须带类型**。引擎内部用 reflect.DeepEqual 比较，期望值写成
 *      字符串 "200" 而实际是数字 200 会**假失败**。所以这里显式让用户选类型，
 *      而不是一律当字符串（编译器另有 normalizeScalar 兜底整数，但那只能
 *      修 JSON 数字被解成 float64 的问题，修不了"用户本来就想填字符串"的歧义）。
 *   2. **M1 不支持自定义失败提示**（AssertItem.Msg 被编译器丢弃）。
 *      与其给一个填了不生效的输入框，不如不显示。
 */
import { ASSERT_METHODS, type AssertItem } from '@/api'

const model = defineModel<AssertItem[]>({ required: true })

const EXPECT_TYPES = [
  { value: 'string', label: '字符串' },
  { value: 'number', label: '数字' },
  { value: 'boolean', label: '布尔' },
  { value: 'null', label: 'null' },
  { value: 'json', label: 'JSON（数组/对象）' },
] as const

const METHOD_HINTS: Record<string, string> = {
  eq: '相等（深比较）',
  equals: '相等',
  equal: '相等',
  ne: '不相等',
  gt: '大于',
  ge: '大于等于',
  lt: '小于',
  le: '小于等于',
  str_eq: '转成字符串后相等',
  len_eq: '长度等于',
  len_gt: '长度大于',
  len_ge: '长度大于等于',
  len_lt: '长度小于',
  len_le: '长度小于等于',
  contains: '实际值包含期望值',
  contained_by: '实际值被期望值包含',
  type_match: '类型匹配',
  regex_match: '正则匹配',
  startswith: '以…开头',
  endswith: '以…结尾',
}

function add() {
  model.value = [...model.value, { check: '', assert: 'eq', expect: '', expect_type: 'string' }]
}

function remove(i: number) {
  const next = [...model.value]
  next.splice(i, 1)
  model.value = next
}

/** 由期望值推断类型，用于回显后端已有的断言（它不带 expect_type）。 */
function inferType(v: unknown): string {
  if (v === null || v === undefined) return 'null'
  if (typeof v === 'number') return 'number'
  if (typeof v === 'boolean') return 'boolean'
  if (typeof v === 'object') return 'json'
  return 'string'
}

function typeOf(item: AssertItem): string {
  return item.expect_type || inferType(item.expect)
}

function expectText(item: AssertItem): string {
  if (item.expect === null || item.expect === undefined) return ''
  if (typeof item.expect === 'string') return item.expect
  if (typeof item.expect === 'object') {
    try {
      return JSON.stringify(item.expect)
    } catch {
      return String(item.expect)
    }
  }
  return String(item.expect)
}

function coerce(type: string, text: string): unknown {
  if (type === 'null') return null
  if (type === 'number') {
    const n = Number(text)
    return text.trim() === '' || Number.isNaN(n) ? text : n
  }
  if (type === 'boolean') return text.trim().toLowerCase() === 'true'
  if (type === 'json') {
    try {
      return JSON.parse(text)
    } catch {
      // 暂时留着原始文本：用户可能正打到一半，报错交给校验器/保存前检查
      return text
    }
  }
  return text
}

function onTypeChange(item: AssertItem, type: string) {
  const text = expectText(item)
  item.expect_type = type
  item.expect = coerce(type, text)
}

function onExpectInput(item: AssertItem, text: string) {
  item.expect = coerce(typeOf(item), text)
}

/** 实时展示编译结果，让"类型选错了"在保存前就能看出来。 */
function preview(item: AssertItem): string {
  const method = (item.assert || '').trim() || '?'
  const check = (item.check || '').trim() || '?'
  const raw = expectText(item)
  const t = typeOf(item)
  let shown: string
  if (t === 'number') shown = raw === '' ? 'NaN' : raw
  else if (t === 'null') shown = 'null'
  else if (t === 'json') shown = raw || 'null'
  else if (t === 'boolean') shown = raw.trim().toLowerCase() === 'true' ? 'true' : 'false'
  else shown = JSON.stringify(raw)
  return `${method}: ["${check}", ${shown}]`
}
</script>

<template>
  <div class="as">
    <div v-if="model.length === 0" class="hrp-muted as__empty">未配置断言（只能验证"请求发得出去"）</div>

    <div v-for="(item, i) in model" :key="i" class="as__row">
      <el-input v-model="item.check" placeholder="检查表达式，如 status_code" class="as__check hrp-mono" />

      <el-select v-model="item.assert" class="as__method" placeholder="校验方法">
        <el-option v-for="m in ASSERT_METHODS" :key="m" :label="m" :value="m">
          <span>{{ m }}</span>
          <span class="hrp-muted as__opt-hint">{{ METHOD_HINTS[m] }}</span>
        </el-option>
      </el-select>

      <el-select
        :model-value="typeOf(item)"
        class="as__type"
        @update:model-value="(t: string) => onTypeChange(item, t)"
      >
        <el-option v-for="t in EXPECT_TYPES" :key="t.value" :label="t.label" :value="t.value" />
      </el-select>

      <el-input
        :model-value="expectText(item)"
        :disabled="typeOf(item) === 'null'"
        placeholder="期望值"
        class="as__expect hrp-mono"
        @update:model-value="(v: string) => onExpectInput(item, v)"
      />

      <el-button link type="danger" @click="remove(i)">
        <el-icon><Delete /></el-icon>
      </el-button>

      <div class="as__preview hrp-mono">编译为 {{ preview(item) }}</div>
    </div>

    <el-button link type="primary" @click="add">
      <el-icon><Plus /></el-icon>添加断言
    </el-button>
  </div>
</template>

<style scoped>
.as__row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding-bottom: 8px;
  margin-bottom: 8px;
  border-bottom: 1px dashed #ebeef5;
}

.as__check {
  width: 220px;
  flex: none;
}

.as__method {
  width: 150px;
  flex: none;
}

.as__type {
  width: 150px;
  flex: none;
}

.as__expect {
  width: 200px;
  flex: none;
}

.as__opt-hint {
  margin-left: 8px;
  font-size: 11px;
}

.as__preview {
  flex-basis: 100%;
  font-size: 11px;
  color: var(--hrp-muted);
  padding-left: 2px;
}

.as__empty {
  font-size: 12px;
  padding-bottom: 6px;
}
</style>
