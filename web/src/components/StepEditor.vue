<script setup lang="ts">
/**
 * 单个步骤编辑器。
 *
 * 直接原地修改传入的 step 对象。Vue 只对「给 prop 本身重新赋值」告警，
 * 改动对象的属性是允许的；这里之所以这么做，是因为步骤内容嵌套很深，
 * 每层都 emit 一遍会让「表单改一个字要穿过 5 个事件」——那种代码无法维护。
 */
/* eslint-disable vue/no-mutating-props */
// ⬆ 本文件整文件豁免 vue/no-mutating-props：原地改共享 step 对象是上面的
// 设计决策（嵌套深、逐层 emit 不可维护），父组件 CaseEditorView 持有全部状态。
import { computed } from 'vue'

import { BODY_TYPES, type APIDef } from '@/api'
import AssertEditor from '@/components/AssertEditor.vue'
import ExtractEditor from '@/components/ExtractEditor.vue'
import KeyValueEditor from '@/components/KeyValueEditor.vue'
import { willAppendTrailingSlash, type EditorStep } from '@/types/editor'
import { HTTP_METHODS } from '@/utils/dict'
import { undefinedRefs } from '@/utils/variables'

const props = defineProps<{
  step: EditorStep
  index: number
  total: number
  /** 本步骤可见的已定义变量集合（M3 ③-c 4.3），由父组件跨步骤计算 */
  definedVars?: string[]
  /** 本项目接口库（F1）：引用接口时选择用 */
  apis?: APIDef[]
}>()

const emit = defineEmits<{
  (e: 'remove', index: number): void
  (e: 'duplicate', index: number): void
  (e: 'move', payload: { from: number; to: number }): void
  (e: 'debug', index: number): void
}>()

const slashWarning = computed(() =>
  props.step.stepType === 'api' ? false : willAppendTrailingSlash(props.step.url, props.step.params),
)

/** 当前引用的接口（引用型步骤的摘要展示）。 */
const selectedApi = computed(() => {
  if (props.step.stepType !== 'api' || !props.step.refApiId) return null
  return (props.apis ?? []).find((a) => a.id === props.step.refApiId) ?? null
})

/** 引用接口的选项按模块分组。 */
const apiGroups = computed(() => {
  const byModule = new Map<string, APIDef[]>()
  for (const a of props.apis ?? []) {
    const m = (a.module || '未分类').trim()
    if (!byModule.has(m)) byModule.set(m, [])
    byModule.get(m)!.push(a)
  }
  return [...byModule.entries()].sort((x, y) => x[0].localeCompare(y[0]))
})

/** 切换类型：切回直接请求时清掉引用，避免留下不一致的草稿。 */
function onTypeChange(t: string) {
  if (t === 'request') props.step.refApiId = null
}

/**
 * 本步骤引用了但未定义的变量（M3 ③-c 4.3）。
 *
 * 检查范围：URL、请求头值、查询参数值、请求体（json/raw 文本 + form 值）。
 * 步骤级变量（variables 的 value）里引用 $ 也算，但变量名本身不算引用。
 * 未定义 = 不在「前置步骤 extract + config.variables + 本步骤 variables」里。
 */
const undefinedVars = computed(() => {
  if (!props.definedVars) return []
  const defined = new Set(props.definedVars)

  const texts: string[] = [props.step.url]
  // 请求头 / 查询参数的 value 可能引用变量
  for (const r of props.step.headers) texts.push(r.value)
  for (const r of props.step.params) texts.push(r.value)
  for (const r of props.step.bodyForm) texts.push(r.value)
  for (const r of props.step.variables) texts.push(r.value)
  if (props.step.bodyType === 'json') texts.push(props.step.bodyJson)
  if (props.step.bodyType === 'raw') texts.push(props.step.bodyRaw)

  const found = new Set<string>()
  for (const t of texts) {
    for (const name of undefinedRefs(t, defined)) {
      found.add(name)
    }
  }
  return [...found]
})

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

const gqlVarsError = computed(() => {
  if (props.step.stepType !== 'graphql' || !props.step.gqlVariables.trim()) return ''
  try {
    JSON.parse(props.step.gqlVariables)
    return ''
  } catch (e) {
    return e instanceof Error ? e.message : '格式错误'
  }
})

function formatGqlVars() {
  if (!props.step.gqlVariables.trim()) return
  try {
    props.step.gqlVariables = JSON.stringify(JSON.parse(props.step.gqlVariables), null, 2)
  } catch {
    // 非法 JSON 时保持原样，错误信息已在模板里展示
  }
}

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
        <el-select v-model="step.stepType" class="step__type" @change="onTypeChange">
          <el-option label="直接请求" value="request" />
          <el-option label="引用接口" value="api" />
          <el-option label="WebSocket" value="websocket" />
          <el-option label="GraphQL" value="graphql" />
        </el-select>
        <el-switch v-model="step.enabled" active-text="启用" inline-prompt />
        <span v-if="!step.enabled" class="hrp-muted step__off-hint">
          禁用步骤不会出现在编译产物里（引擎没有"跳过步骤"语法）
        </span>
      </div>
      <div class="step__actions">
        <el-button link :disabled="!step.enabled" @click="emit('debug', index)">调试此步</el-button>
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

    <template v-if="step.stepType === 'api'">
      <div class="row row--api">
        <el-select
          v-model="step.refApiId"
          class="row__api"
          filterable
          placeholder="选择一个接口定义"
        >
          <el-option-group v-for="[mod, list] in apiGroups" :key="mod" :label="mod">
            <el-option
              v-for="a in list"
              :key="a.id"
              :label="`${a.method} ${a.path}`"
              :value="a.id"
            />
          </el-option-group>
        </el-select>
        <el-tag v-if="selectedApi" size="small" effect="plain">{{ selectedApi.method }}</el-tag>
        <span v-if="selectedApi" class="hrp-mono hrp-muted row__api-path">{{ selectedApi.path }}</span>
        <el-button link type="primary" @click="step.stepType = 'request'; step.refApiId = null">
          解除引用
        </el-button>
      </div>
      <div class="hrp-muted note">
        请求内容来自接口库里的定义（改接口定义，所有引用它的步骤下次执行自动生效）。
        断言、提取与变量仍属于本步骤，可在下方页签编辑。
      </div>
      <el-alert v-if="!step.refApiId" type="info" show-icon :closable="false" class="step__alert">
        <template #title>还没有选择接口</template>
        引用型步骤必须选择一个接口才能保存与执行；也可以点「解除引用」改为直接写请求。
      </el-alert>
    </template>

    <div v-else-if="step.stepType === 'request' || step.stepType === 'graphql'" class="row">
      <el-select v-if="step.stepType === 'request'" v-model="step.method" class="row__method">
        <el-option v-for="m in HTTP_METHODS" :key="m" :label="m" :value="m" />
      </el-select>
      <el-input v-model="step.url" placeholder="相对路径（如 /get）或完整 URL" class="row__url hrp-mono" />
      <el-input-number v-model="step.timeout" :min="0" :max="3600" :step="1" class="row__timeout" />
      <span class="hrp-muted row__hint">请求超时（秒，0 表示不设置）</span>
    </div>

    <template v-else-if="step.stepType === 'websocket'">
      <div class="row">
        <el-select v-model="step.wsOpType" class="row__method">
          <el-option label="open 建连接" value="open" />
          <el-option label="wr 发+收" value="wr" />
          <el-option label="w 只发" value="w" />
          <el-option label="r 只收" value="r" />
          <el-option label="ping 心跳" value="ping" />
          <el-option label="close 关连接" value="close" />
        </el-select>
        <el-input v-model="step.wsUrl" placeholder="ws://127.0.0.1:8900/ws" class="row__url hrp-mono" />
      </div>
      <el-input
        v-if="step.wsOpType === 'w' || step.wsOpType === 'wr'"
        v-model="step.wsPayload"
        type="textarea"
        :rows="4"
        class="hrp-mono ws-payload"
        placeholder="要发送的文本内容（引擎的 text 帧）"
      />
      <div class="hrp-muted note">
        WS 是**动作式**步骤：要先 open 再 wr/w/r，最后 close。每个动作都必须填同一个地址
        （引擎按 URL 查找连接，实测 A26）。引擎还会把地址末尾补成 <code>/</code>。
        断言与提取用 <code>body</code>（收到的消息）。
      </div>
    </template>

    <el-alert v-if="slashWarning && step.stepType === 'request'" type="warning" show-icon :closable="false" class="step__alert">
      <template #title>引擎会补结尾斜杠</template>
      URL 不带查询串时，引擎会在发出前补一个 <code>/</code>：`$base_url/get` 实际请求的是
      <code>GET /get/</code>。这个行为无法用改写 YAML 绕开（实测 A1），修法是让 URL 带上真实查询串
      （加一个参数即可）。最终生效地址以执行详情里的「最终 URL」为准。
    </el-alert>

    <el-alert v-if="undefinedVars.length" type="error" show-icon :closable="false" class="step__alert">
      <template #title>引用了未定义的变量</template>
      <template #default>
        本步骤引用了这些变量，但它们既不在「用例变量」里，也不是前面步骤提取的：
        <el-tag
          v-for="v in undefinedVars"
          :key="v"
          size="small"
          type="danger"
          effect="plain"
          class="undef-tag"
        >
          <code>${{ v }}</code>
        </el-tag>
        。未定义变量会导致引擎退出码 21（用例问题）。若它由「环境变量」提供，可忽略此提示（保存并校验会给权威结论）。
      </template>
    </el-alert>

    <template v-if="step.stepType === 'graphql'">
      <div class="gql-block">
        <el-input
          v-model="step.gqlQuery"
          type="textarea"
          :rows="7"
          class="hrp-mono"
          placeholder="query Q($id: ID!) { user(id: $id) { name } }"
        />
        <div class="gql-row">
          <el-input v-model="step.gqlOperation" placeholder="operationName（可选）" style="width: 240px" />
          <el-button size="small" @click="formatGqlVars">格式化 variables</el-button>
          <span v-if="gqlVarsError" class="body-error">JSON 非法：{{ gqlVarsError }}</span>
        </div>
        <el-input
          v-model="step.gqlVariables"
          type="textarea"
          :rows="3"
          class="hrp-mono"
          placeholder='variables，如 {"id": 1}'
        />
        <div class="hrp-muted note">
          GraphQL 在协议层就是「POST + JSON {query, variables, operationName}」，
          因此编译产物是普通 HTTP 请求：断言、提取、认证、重试全部照常可用（实测 A27）。
        </div>
      </div>
    </template>

    <el-tabs class="step__tabs">
      <el-tab-pane v-if="step.stepType === 'websocket'" label="请求头">
        <KeyValueEditor v-model="step.wsHeaders" key-placeholder="Header 名" />
        <div class="hrp-muted note">握手时带上；后续动作不重复发送头。</div>
      </el-tab-pane>
      <el-tab-pane v-if="step.stepType === 'request' || step.stepType === 'graphql'" label="请求头">
        <KeyValueEditor v-model="step.headers" key-placeholder="Header 名" />
        <div class="hrp-muted note">
          请求头按「环境全局头 → 用例头 → 步骤头」合并，后者覆盖前者（大小写不敏感去重）。这里只需写本步骤特有的。
        </div>
      </el-tab-pane>

      <el-tab-pane v-if="step.stepType === 'request'" label="查询参数">
        <KeyValueEditor v-model="step.params" key-placeholder="参数名" />
        <div class="hrp-muted note">参数为空时，引擎会给 URL 补结尾斜杠（见上方提示）。</div>
      </el-tab-pane>

      <el-tab-pane v-if="step.stepType === 'request'" label="请求体">
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

      <el-tab-pane label="认证">
        <div class="auth-row">
          <el-select v-model="step.auth.type" style="width: 150px">
            <el-option label="继承环境" value="inherit" />
            <el-option label="无认证" value="none" />
            <el-option label="Bearer Token" value="bearer" />
            <el-option label="Basic 认证" value="basic" />
            <el-option label="API Key" value="api_key" />
          </el-select>

          <template v-if="step.auth.type === 'bearer'">
            <el-input v-model="step.auth.token" style="width: 300px" placeholder="值，可写 $token 引用上一步提取的变量" />
          </template>
          <template v-else-if="step.auth.type === 'basic'">
            <el-input v-model="step.auth.username" style="width: 140px" placeholder="用户名" />
            <el-input v-model="step.auth.password" style="width: 160px" placeholder="密码" show-password />
          </template>
          <template v-else-if="step.auth.type === 'api_key'">
            <el-input v-model="step.auth.key_name" style="width: 150px" placeholder="键名，如 X-Api-Key" />
            <el-input v-model="step.auth.key_value" style="width: 200px" placeholder="键值" />
            <el-select v-model="step.auth.key_in" style="width: 110px">
              <el-option label="请求头" value="header" />
              <el-option label="查询参数" value="query" />
            </el-select>
          </template>
        </div>
        <div class="hrp-muted note">
          「继承环境」用环境里配的认证；这里配置会覆盖环境。手写请求头的优先级高于认证。
          <br />
          <b>token 自动传递</b>：在第 1 步「提取」出 <code>token</code>，第 2 步选 Bearer 并填 <code>$token</code> 即可。
          <br />
          <b>动态变量</b>：URL / 请求头 / 参数 / 请求体里可写
          <code v-pre>{{$timestamp}}</code>、<code v-pre>{{$timestampMs}}</code>、
          <code v-pre>{{$uuid}}</code>，编译期替换，每次执行都不同。
        </div>
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
.auth-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 8px;
}
.step__type {
  width: 110px;
  flex: none;
}
.row--api {
  display: flex;
  align-items: center;
  gap: 8px;
}
.row__api {
  width: 420px;
  flex: none;
}
.row__api-path {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
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

.undef-tag {
  margin: 0 4px;
}
</style>
