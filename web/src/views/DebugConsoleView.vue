<script setup lang="ts">
/**
 * 调试台（F3）：不建用例也能发请求，满意后沉淀为接口定义或用例。
 *
 * 两条设计约定：
 *   1. 发送走的是**真正的引擎**（后端合成单步骤用例跑 hrp），不是前端的 fetch。
 *      这样「调试台看到的结果」与「用例执行的结果」天然一致，包括 $base_url、
 *      变量替换与尾斜杠行为。
 *   2. 不落库：刷新即失；需要留存的走「存为接口 / 存为用例」。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'

import {
  apiDefApi,
  caseApi,
  debugApi,
  environmentApi,
  type Environment,
  type DebugStepResult,
  type StepReq,
} from '@/api'
import KeyValueEditor from '@/components/KeyValueEditor.vue'
import { useProjectStore } from '@/stores/project'
import { rowsToMap, type KVRow } from '@/utils/format'
import { notifyError, notifyOk } from '@/utils/error'
import { HTTP_METHODS } from '@/utils/dict'

const projects = useProjectStore()
const router = useRouter()

const envs = ref<Environment[]>([])
const envId = ref<number>(0)

const req = reactive({
  method: 'GET',
  url: '',
  headers: [] as KVRow[],
  params: [] as KVRow[],
  bodyType: 'none' as string,
  bodyJson: '',
  bodyRaw: '',
})

const formRows = ref<KVRow[]>([])
const sending = ref(false)
const result = ref<DebugStepResult | null>(null)

const step = computed(() => result.value?.steps?.[0] ?? null)
const resp = computed(() => step.value?.response ?? null)

const urlError = computed(() => (req.url.trim() ? '' : '请填写请求地址'))
const jsonError = computed(() => {
  if (req.bodyType !== 'json' || !req.bodyJson.trim()) return ''
  try {
    JSON.parse(req.bodyJson)
    return ''
  } catch (e) {
    return `JSON 非法：${(e as Error).message}`
  }
})
const canSend = computed(() => !!req.url.trim() && !jsonError.value && !sending.value)

function buildRequest() {
  const headers = rowsToMap(req.headers)
  const params = rowsToMap(req.params)
  const r: Record<string, unknown> = {
    method: req.method,
    url: req.url.trim(),
    body_type: req.bodyType,
  }
  if (headers) r.headers = headers
  if (params) r.params = params
  if (req.bodyType === 'json' && req.bodyJson.trim()) r.body = JSON.parse(req.bodyJson)
  if (req.bodyType === 'raw' && req.bodyRaw !== '') r.body = req.bodyRaw
  if (req.bodyType === 'form') {
    const m = rowsToMap(formRows.value)
    if (m) r.body = m
  }
  return r
}

async function send() {
  if (!projects.currentId || !canSend.value) return
  sending.value = true
  result.value = null
  try {
    result.value = await debugApi.request(projects.currentId, {
      env_id: envId.value || undefined,
      request: buildRequest() as never,
    })
  } catch (e) {
    notifyError(e)
  } finally {
    sending.value = false
  }
}

/** 把响应体尽量格式化展示（JSON 就缩进，否则原样）。 */
const prettyBody = computed(() => {
  const raw = resp.value?.body
  const body = typeof raw === 'string' ? raw : raw == null ? '' : JSON.stringify(raw)
  const t = body.trim()
  if (!t || (!t.startsWith('{') && !t.startsWith('['))) return body
  try {
    return JSON.stringify(JSON.parse(t), null, 2)
  } catch {
    return body
  }
})

// ---- 存为接口 ----
const apiDialog = ref(false)
const apiForm = reactive({ module: '', code: '', name: '' })

function openSaveApi() {
  apiForm.module = ''
  apiForm.code = ''
  apiForm.name = ''
  apiDialog.value = true
}

async function saveAsApi() {
  if (!projects.currentId) return
  try {
    const r = buildRequest() as Record<string, unknown>
    await apiDefApi.createApi(projects.currentId, {
      module: apiForm.module.trim(),
      code: apiForm.code.trim(),
      name: apiForm.name.trim(),
      method: String(r.method || 'GET'),
      path: String(r.url || ''),
      headers: (r.headers as never) ?? null,
      params: (r.params as never) ?? null,
      body: r.body ?? null,
      body_type: String(r.body_type || 'none'),
    })
    notifyOk('已存入接口库')
    apiDialog.value = false
  } catch (e) {
    notifyError(e)
  }
}

// ---- 存为用例 ----
const caseDialog = ref(false)
const caseForm = reactive({ code: '', name: '' })

function openSaveCase() {
  caseForm.code = ''
  caseForm.name = ''
  caseDialog.value = true
}

async function saveAsCase() {
  if (!projects.currentId) return
  try {
    const r = buildRequest()
    const steps: StepReq[] = [
      {
        seq: 1,
        step_type: 'request',
        name: '请求',
        request: r as never,
        variables: null,
        extract: [],
        validate: [],
      } as unknown as StepReq,
    ]
    const created = await caseApi.createCase(projects.currentId, {
      code: caseForm.code.trim(),
      name: caseForm.name.trim() || caseForm.code.trim(),
      priority: 'P2',
      status: 'active',
      steps,
    } as never)
    notifyOk('已创建用例，去补断言吧')
    caseDialog.value = false
    router.push({ name: 'case-edit', params: { id: created.id } })
  } catch (e) {
    notifyError(e)
  }
}

onMounted(async () => {
  if (!projects.loaded) await projects.fetchList()
  if (!projects.currentId) return
  try {
    const page = await environmentApi.listEnvironments(projects.currentId)
    envs.value = page.list ?? []
    envId.value = envs.value.find((e) => e.is_default)?.id ?? 0
  } catch {
    envs.value = []
  }
})
</script>

<template>
  <div class="page">
    <div class="page__head">
      <div>
        <h2 class="page__title">调试台</h2>
        <p class="hrp-muted page__desc">
          随手发一个请求，看真实引擎返回的响应；满意后一键存为接口定义或用例。请求不会进入执行记录。
        </p>
      </div>
    </div>

    <el-card shadow="never" class="card">
      <div class="row">
        <el-select v-model="envId" placeholder="默认环境" class="row__env">
          <el-option :value="0" label="默认环境" />
          <el-option v-for="e in envs" :key="e.id" :value="e.id" :label="e.name" />
        </el-select>
        <el-select v-model="req.method" class="row__method">
          <el-option v-for="m in HTTP_METHODS" :key="m" :label="m" :value="m" />
        </el-select>
        <el-input
          v-model="req.url"
          class="row__url hrp-mono"
          placeholder="$base_url/users/1 或完整 URL"
          @keyup.enter="send"
        />
        <el-button type="primary" :loading="sending" :disabled="!canSend" @click="send">发送</el-button>
      </div>
      <div v-if="urlError" class="hrp-muted hint">{{ urlError }}</div>

      <el-tabs class="tabs">
        <el-tab-pane label="请求头">
          <KeyValueEditor v-model="req.headers" key-placeholder="Header 名" />
        </el-tab-pane>
        <el-tab-pane label="查询参数">
          <KeyValueEditor v-model="req.params" key-placeholder="参数名" />
        </el-tab-pane>
        <el-tab-pane label="请求体">
          <el-radio-group v-model="req.bodyType" class="body-type">
            <el-radio-button value="none">none</el-radio-button>
            <el-radio-button value="json">json</el-radio-button>
            <el-radio-button value="form">form</el-radio-button>
            <el-radio-button value="raw">raw</el-radio-button>
          </el-radio-group>
          <template v-if="req.bodyType === 'json'">
            <el-input v-model="req.bodyJson" type="textarea" :rows="6" class="hrp-mono" placeholder='{"username": "tester"}' />
            <div v-if="jsonError" class="err">{{ jsonError }}</div>
          </template>
          <KeyValueEditor v-else-if="req.bodyType === 'form'" v-model="formRows" key-placeholder="字段名" />
          <el-input v-else-if="req.bodyType === 'raw'" v-model="req.bodyRaw" type="textarea" :rows="5" class="hrp-mono" placeholder="原始请求体" />
        </el-tab-pane>
      </el-tabs>
    </el-card>

    <el-card v-if="result" shadow="never" class="card">
      <div class="resp-head">
        <el-tag :type="result.status === 'pass' ? 'success' : result.status === 'error' ? 'danger' : 'warning'" effect="dark">
          {{ result.status }}
        </el-tag>
        <span v-if="resp" class="resp-status hrp-mono">{{ resp.proto }} {{ resp.status_code }}</span>
        <span class="hrp-muted">{{ result.duration_ms }} ms</span>
        <span v-if="step?.final_url" class="hrp-mono hrp-muted resp-url">{{ step.final_url }}</span>
        <div class="resp-actions">
          <el-button size="small" @click="openSaveApi">存为接口</el-button>
          <el-button size="small" @click="openSaveCase">存为用例</el-button>
        </div>
      </div>

      <el-alert v-if="result.compile_error" type="error" show-icon :closable="false" :title="`渲染失败：${result.compile_error}`" />
      <el-alert v-else-if="result.error_msg" type="warning" show-icon :closable="false" :title="result.error_msg" />

      <el-tabs v-if="resp" class="tabs">
        <el-tab-pane label="响应体">
          <pre class="body hrp-mono">{{ prettyBody }}</pre>
        </el-tab-pane>
        <el-tab-pane label="响应头">
          <pre class="body hrp-mono">{{ JSON.stringify(resp.headers ?? {}, null, 2) }}</pre>
        </el-tab-pane>
        <el-tab-pane label="请求回放">
          <pre class="body hrp-mono">{{ step?.request ? `${step.request.method} ${step.request.url}\n\n${step.request.body ?? ''}` : '—' }}</pre>
        </el-tab-pane>
      </el-tabs>
      <p v-else class="hrp-muted">没有拿到响应快照；上面的错误信息是可用的唯一线索。</p>
    </el-card>

    <el-dialog v-model="apiDialog" title="存为接口定义" width="480px">
      <el-form label-width="80px">
        <el-form-item label="服务模块">
          <el-input v-model="apiForm.module" placeholder="如 user，留空为未分类" />
        </el-form-item>
        <el-form-item label="标识" required>
          <el-input v-model="apiForm.code" placeholder="如 user_get" />
        </el-form-item>
        <el-form-item label="名称">
          <el-input v-model="apiForm.name" placeholder="留空用标识" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="apiDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!apiForm.code.trim()" @click="saveAsApi">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="caseDialog" title="存为用例" width="480px">
      <el-form label-width="80px">
        <el-form-item label="标识" required>
          <el-input v-model="caseForm.code" placeholder="如 adhoc_demo" />
        </el-form-item>
        <el-form-item label="名称">
          <el-input v-model="caseForm.name" placeholder="留空用标识" />
        </el-form-item>
      </el-form>
      <p class="hrp-muted">创建后跳到编辑器：断言、提取与参数化在那里继续补。</p>
      <template #footer>
        <el-button @click="caseDialog = false">取消</el-button>
        <el-button type="primary" :disabled="!caseForm.code.trim()" @click="saveAsCase">创建并编辑</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page__head {
  margin-bottom: 16px;
}
.page__title {
  margin: 0;
  font-size: 18px;
}
.page__desc {
  margin: 4px 0 0;
  font-size: 12px;
}
.card {
  margin-bottom: 14px;
}
.row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.row__env {
  width: 140px;
  flex: none;
}
.row__method {
  width: 110px;
  flex: none;
}
.row__url {
  flex: 1;
}
.hint {
  font-size: 12px;
  margin-top: 6px;
}
.tabs {
  margin-top: 12px;
}
.body-type {
  margin-bottom: 8px;
}
.err {
  color: var(--el-color-danger);
  font-size: 12px;
  margin-top: 4px;
}
.resp-head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin-bottom: 10px;
  flex-wrap: wrap;
}
.resp-status {
  font-size: 13px;
  font-weight: 500;
}
.resp-url {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 320px;
}
.resp-actions {
  margin-left: auto;
}
.body {
  margin: 0;
  padding: 10px;
  background: var(--el-fill-color-light);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.5;
  max-height: 420px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
