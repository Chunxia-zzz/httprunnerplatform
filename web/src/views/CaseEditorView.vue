<script setup lang="ts">
/**
 * 用例编辑器（M1 核心页）。
 *
 * 设计要点：
 *   1. **表单为主，YAML 为辅**：右侧只读预览展示"编辑器看到的 = 实际要跑的"，
 *      两者共用后端同一条 compiler.Render 渲染链路，逐字节一致。
 *   2. **保存是编译的前置条件**：编译方向是 DB → YAML（单向），
 *      所以未落库的内容无法预览、无法校验。校验按钮因此要求先保存。
 *   3. **不暴露无效字段**：`request_timeout` 在编译器里从未被读取（死字段），
 *      与其给一个填了不生效的输入框，不如不显示。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'

import {
  ApiError,
  caseApi,
  runApi,
  type CaseDetail,
  type CaseReq,
  type ModuleCount,
  type StepReq,
  type ValidateOutcome,
  type YAMLPreview,
} from '@/api'
import KeyValueEditor from '@/components/KeyValueEditor.vue'
import StepEditor from '@/components/StepEditor.vue'
import ValidateIssues from '@/components/ValidateIssues.vue'
import YamlPreview from '@/components/YamlPreview.vue'
import { useProjectStore } from '@/stores/project'
import { cloneStep, newStep, type EditorStep } from '@/types/editor'
import { mapToRows, prettyValue, rowsToMap, type KVRow } from '@/utils/format'
import { notifyError, notifyOk } from '@/utils/error'

const route = useRoute()
const router = useRouter()
const projects = useProjectStore()

const caseId = ref(Number(route.params.id || 0))
const isEdit = computed(() => caseId.value > 0)

const loading = ref(false)
const saving = ref(false)
const running = ref(false)
const formRef = ref<FormInstance>()

const form = reactive({
  code: '',
  name: '',
  module: '',
  priority: 'P1',
  tags: '',
  status: 'active',
  description: '',
  caseTimeout: 0,
  configVariables: [] as KVRow[],
  configHeaders: [] as KVRow[],
  configExport: [] as string[],
  /** '' = 不写 verify 键（跟随环境）；'true' / 'false' = 显式覆盖 */
  verifyOverride: '' as '' | 'true' | 'false',
})

const steps = ref<EditorStep[]>([])
const modules = ref<ModuleCount[]>([])

const preview = ref<YAMLPreview | null>(null)
const previewLoading = ref(false)
const previewError = ref('')

const validateOutcome = ref<ValidateOutcome | null>(null)
const validateLoading = ref(false)
const validateError = ref('')

const activeTab = ref<'validate' | 'yaml'>('validate')

/** 编辑器默认落在「校验」页；跑通之后用户更常看 YAML。 */

const rules: FormRules = {
  code: [
    { required: true, message: '请输入用例标识', trigger: 'blur' },
    {
      pattern: /^[a-zA-Z0-9_-]{1,64}$/,
      message: '只能包含字母、数字、下划线、连字符，长度 1–64',
      trigger: 'blur',
    },
  ],
  name: [{ required: true, message: '请输入用例名称', trigger: 'blur' }],
}

const enabledStepCount = computed(() => steps.value.filter((s) => s.enabled).length)

const validateDisabledReason = computed(() => {
  if (!isEdit.value) return '用例尚未保存。编译方向是「DB → YAML」单向的，未落库的内容无法校验 —— 先点保存。'
  return ''
})

// ---------------------------------------------------------------------------
// 加载
// ---------------------------------------------------------------------------

async function load() {
  if (!isEdit.value) {
    // 新建：给一个可用的初始步骤，否则用户面对空白的"步骤"区不知从何下手
    steps.value = [newStep()]
    return
  }
  loading.value = true
  try {
    const detail = await caseApi.getCase(caseId.value)
    applyDetail(detail)
    await refreshPreview()
  } catch (e) {
    notifyError(e, '加载用例失败')
  } finally {
    loading.value = false
  }
}

function applyDetail(detail: CaseDetail) {
  form.code = detail.code
  form.name = detail.name
  form.module = detail.module
  form.priority = detail.priority
  form.tags = detail.tags
  form.status = detail.status
  form.description = detail.description
  form.caseTimeout = detail.case_timeout

  const cfg = detail.config ?? {}
  form.configVariables = mapToRows(cfg.variables)
  form.configHeaders = mapToRows(cfg.headers)
  form.configExport = Array.isArray(cfg.export) ? [...cfg.export] : []
  // config.verify 只在**显式出现**时才覆盖环境设置（见 compiler.applyCaseConfig），
  // 所以这里必须区分"没写"与"写了 false"。
  const raw = cfg as Record<string, unknown>
  form.verifyOverride =
    Object.prototype.hasOwnProperty.call(raw, 'verify') && typeof raw.verify === 'boolean'
      ? String(raw.verify) === 'true'
        ? 'true'
        : 'false'
      : ''

  steps.value = detail.steps.map(toEditorStep)
  if (steps.value.length === 0) steps.value = [newStep()]
}

function toEditorStep(s: CaseDetail['steps'][number]): EditorStep {
  const step = newStep()
  const req = s.request ?? {}
  step.name = s.name
  step.enabled = s.enabled
  step.method = (req.method || 'GET').toUpperCase()
  step.url = req.url || ''
  step.headers = mapToRows(req.headers)
  step.params = mapToRows(req.params)
  step.timeout = req.timeout ?? 0
  step.variables = mapToRows(s.variables)
  step.extract = (s.extract ?? []).map((e) => ({ ...e }))
  step.validate = (s.validate ?? []).map((a) => ({ ...a }))

  const bt = (req.body_type || 'none') as string
  const hasBody = req.body !== null && req.body !== undefined
  if (bt === 'form') {
    step.bodyType = 'form'
    step.bodyForm = mapToRows(req.body as Record<string, unknown>)
  } else if (bt === 'raw') {
    step.bodyType = 'raw'
    step.bodyRaw = typeof req.body === 'string' ? req.body : prettyValue(req.body)
  } else if (bt === 'json' || (bt === '' && hasBody)) {
    step.bodyType = 'json'
    step.bodyJson = prettyValue(req.body)
  } else {
    step.bodyType = 'none'
  }
  return step
}

// ---------------------------------------------------------------------------
// 表单 → 请求体
// ---------------------------------------------------------------------------

/** 收集提交前的结构性错误。返回空数组表示可以提交。 */
function collectBlockers(): string[] {
  const out: string[] = []
  steps.value.forEach((s, i) => {
    const label = `第 ${i + 1} 步${s.name ? `（${s.name}）` : ''}`
    if (!s.enabled) return
    if (!s.url.trim()) {
      out.push(`${label}：缺少 URL`)
    }
    if (s.bodyType === 'json') {
      const t = s.bodyJson.trim()
      if (t) {
        try {
          JSON.parse(t)
        } catch (e) {
          out.push(`${label}：请求体不是合法 JSON（${(e as Error).message}）`)
        }
      }
    }
    s.validate.forEach((a, j) => {
      if (!a.check.trim()) out.push(`${label}：第 ${j + 1} 条断言缺少检查表达式`)
      if (!a.assert.trim()) out.push(`${label}：第 ${j + 1} 条断言缺少校验方法`)
      if (a.expect_type === 'json' && typeof a.expect === 'string') {
        try {
          JSON.parse(a.expect)
        } catch {
          out.push(`${label}：第 ${j + 1} 条断言的期望值声明为 JSON，但内容不是合法 JSON`)
        }
      }
    })
    s.extract.forEach((ex, j) => {
      if (!ex.name.trim()) out.push(`${label}：第 ${j + 1} 个提取项没有变量名`)
    })
  })
  return out
}

function toStepReq(s: EditorStep): StepReq {
  const req: Record<string, unknown> = {
    method: s.method,
    url: s.url.trim(),
  }
  const headers = rowsToMap(s.headers)
  if (headers) req.headers = headers
  const params = rowsToMap(s.params)
  if (params) req.params = params
  if (s.timeout > 0) req.timeout = s.timeout

  switch (s.bodyType) {
    case 'json': {
      const t = s.bodyJson.trim()
      if (t) {
        req.body = JSON.parse(t)
        req.body_type = 'json'
      }
      break
    }
    case 'form': {
      const m = rowsToMap(s.bodyForm)
      if (m) {
        req.body = m
        req.body_type = 'form'
      }
      break
    }
    case 'raw': {
      if (s.bodyRaw !== '') {
        req.body = s.bodyRaw
        req.body_type = 'raw'
      }
      break
    }
    default:
      req.body_type = 'none'
  }

  return {
    name: s.name.trim(),
    enabled: s.enabled,
    step_type: 'request',
    request: req as StepReq['request'],
    variables: rowsToMap(s.variables),
    extract: s.extract.filter((e) => e.name.trim() !== '' || e.expression.trim() !== ''),
    validate: s.validate.filter((a) => a.check.trim() !== '' || a.assert.trim() !== ''),
  }
}

function toCaseReq(): CaseReq {
  const config: Record<string, unknown> = {}
  const vars = rowsToMap(form.configVariables)
  if (vars) config.variables = vars
  const headers = rowsToMap(form.configHeaders)
  if (headers) config.headers = headers
  if (form.configExport.length) config.export = [...form.configExport]
  // 只在用户显式选择时才写 verify 键：写了 false 会覆盖环境的 verify_ssl
  if (form.verifyOverride !== '') config.verify = form.verifyOverride === 'true'

  return {
    code: form.code.trim(),
    name: form.name.trim(),
    module: form.module.trim(),
    priority: form.priority,
    tags: form.tags.trim(),
    status: form.status,
    description: form.description.trim(),
    config: Object.keys(config).length ? config : null,
    request_timeout: 0,
    case_timeout: form.caseTimeout,
    steps: steps.value.map(toStepReq),
  }
}

// ---------------------------------------------------------------------------
// 保存 / 校验 / 预览
// ---------------------------------------------------------------------------

async function save(options: { silent?: boolean } = {}): Promise<boolean> {
  const ok = await formRef.value?.validate().catch(() => false)
  if (ok === false) return false

  const blockers = collectBlockers()
  if (blockers.length) {
    ElMessageBox.alert(
      `<div style="line-height:1.9">${blockers.map((b) => `· ${b}`).join('<br/>')}</div>`,
      '提交前请先修正',
      { dangerouslyUseHTMLString: true, confirmButtonText: '知道了' },
    )
    return false
  }

  saving.value = true
  try {
    const payload = toCaseReq()
    const detail = isEdit.value
      ? await caseApi.updateCase(caseId.value, payload)
      : await caseApi.createCase(projects.currentId, payload)

    if (!isEdit.value) {
      caseId.value = detail.id
      // 换成编辑路由，让刷新页面、复制链接都能回到这条用例。
      await router.replace({ name: 'case-edit', params: { id: detail.id } })
    }
    applyDetail(detail)
    if (!options.silent) notifyOk('用例已保存')
    await refreshPreview()
    return true
  } catch (e) {
    notifyError(e, isEdit.value ? '保存用例失败' : '创建用例失败')
    return false
  } finally {
    saving.value = false
  }
}

async function saveAndValidate() {
  const ok = await save({ silent: true })
  if (!ok) return
  notifyOk('用例已保存')
  activeTab.value = 'validate'
  await runValidate()
}

async function runValidate() {
  if (!isEdit.value) return
  validateLoading.value = true
  validateError.value = ''
  try {
    validateOutcome.value = await caseApi.validateCase(caseId.value)
  } catch (e) {
    if (e instanceof ApiError && e.isValidateFail) {
      // HTTP 200 + code=50004：不是接口错误，而是"内容有问题"。
      // 问题列表在 data 里，正常渲染即可。
      validateOutcome.value = (e.data as ValidateOutcome) ?? null
    } else {
      validateOutcome.value = null
      validateError.value = e instanceof ApiError ? e.message : '校验失败'
    }
  } finally {
    validateLoading.value = false
  }
}

async function refreshPreview() {
  if (!isEdit.value) {
    preview.value = null
    return
  }
  previewLoading.value = true
  previewError.value = ''
  try {
    preview.value = await caseApi.caseYaml(caseId.value)
  } catch (e) {
    preview.value = null
    previewError.value =
      e instanceof ApiError && e.code === 50003
        ? `编译失败：${e.message}`
        : e instanceof ApiError
          ? e.message
          : '加载 YAML 预览失败'
  } finally {
    previewLoading.value = false
  }
}

/** 保存并执行：保证"跑的就是你看到的"，不接受未保存的改动被执行。 */
async function saveAndRun() {
  const ok = await save({ silent: true })
  if (!ok) return

  running.value = true
  try {
    const resp = await runApi.startRun({
      project_id: projects.currentId,
      target_type: 'case',
      target_id: caseId.value,
      env_id: 0, // 0 = 用项目默认环境
      options: { gen_html_report: true },
    })
    notifyOk('已开始执行')
    await router.push({ name: 'run-detail', params: { id: resp.run_id } })
  } catch (e) {
    notifyError(e, '启动执行失败')
  } finally {
    running.value = false
  }
}

// ---------------------------------------------------------------------------
// 步骤操作
// ---------------------------------------------------------------------------

function addStep() {
  steps.value.push(newStep())
}

function removeStep(index: number) {
  if (steps.value.length === 1) {
    ElMessage.warning('至少保留一个步骤（引擎执行空用例会静默丢弃，退出码 0 但一条都没跑）')
    return
  }
  steps.value.splice(index, 1)
}

function duplicateStep(index: number) {
  const src = steps.value[index]
  if (!src) return
  steps.value.splice(index + 1, 0, cloneStep(src))
}

function moveStep({ from, to }: { from: number; to: number }) {
  if (to < 0 || to >= steps.value.length) return
  const [item] = steps.value.splice(from, 1)
  if (item) steps.value.splice(to, 0, item)
}

async function loadModules() {
  if (!projects.currentId) return
  try {
    modules.value = await caseApi.caseTree(projects.currentId)
  } catch {
    // 模块只是输入建议，拿不到不影响编辑
  }
}

onMounted(async () => {
  if (!projects.loaded) await projects.fetchList()
  await Promise.all([load(), loadModules()])
})
</script>

<template>
  <div v-loading="loading" class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">{{ isEdit ? '编辑用例' : '新建用例' }}</h2>
        <p class="hrp-page__sub">
          {{ projects.current?.name || '未选择项目' }}
          <template v-if="isEdit"> · ID {{ caseId }}</template>
        </p>
      </div>
      <div class="head-actions">
        <el-button @click="router.push({ name: 'cases' })">返回列表</el-button>
        <el-button :loading="saving" @click="save()">保存</el-button>
        <el-button :loading="validateLoading || saving" @click="saveAndValidate">保存并校验</el-button>
        <el-button type="primary" :loading="running" @click="saveAndRun">保存并执行</el-button>
      </div>
    </div>

    <div class="grid">
      <div class="grid__main">
        <el-card shadow="never" class="hrp-card">
          <template #header><span class="card-title">基本信息</span></template>
          <el-form ref="formRef" :model="form" :rules="rules" label-width="110px">
            <el-row :gutter="16">
              <el-col :span="12">
                <el-form-item label="用例标识" prop="code">
                  <el-input v-model="form.code" :disabled="isEdit" placeholder="例如 tc_login" class="hrp-mono" />
                  <div v-if="isEdit" class="hrp-muted tip">标识决定编译后的文件名，且是历史结果的追溯依据，创建后不可修改。</div>
                </el-form-item>
              </el-col>
              <el-col :span="12">
                <el-form-item label="用例名称" prop="name">
                  <el-input v-model="form.name" placeholder="例如 登录成功" />
                  <div class="hrp-muted tip">同项目内必须唯一 —— 引擎以用例名作 summary.json 的唯一标识，重名会导致结果无法区分。</div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-row :gutter="16">
              <el-col :span="8">
                <el-form-item label="模块">
                  <el-select
                    v-model="form.module"
                    filterable
                    allow-create
                    default-first-option
                    clearable
                    placeholder="选择或输入新模块"
                    style="width: 100%"
                  >
                    <el-option v-for="m in modules" :key="m.module" :label="m.module || '（未分组）'" :value="m.module" />
                  </el-select>
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="优先级">
                  <el-select v-model="form.priority" style="width: 100%">
                    <el-option v-for="p in ['P0', 'P1', 'P2', 'P3']" :key="p" :label="p" :value="p" />
                  </el-select>
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="状态">
                  <el-select v-model="form.status" style="width: 100%">
                    <el-option label="启用" value="active" />
                    <el-option label="草稿" value="draft" />
                    <el-option label="禁用" value="disabled" />
                  </el-select>
                  <div class="hrp-muted tip">禁用后无法执行（执行前会被拒绝）。</div>
                </el-form-item>
              </el-col>
            </el-row>

            <el-row :gutter="16">
              <el-col :span="8">
                <el-form-item label="标签">
                  <el-input v-model="form.tags" placeholder="逗号分隔，如 smoke,regression" />
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="用例超时">
                  <el-input-number v-model="form.caseTimeout" :min="0" :max="86400" style="width: 100%" />
                  <div class="hrp-muted tip">秒。平台侧的子进程超时，0 = 用平台默认（配置里的 default_case_timeout）。</div>
                </el-form-item>
              </el-col>
              <el-col :span="8">
                <el-form-item label="描述">
                  <el-input v-model="form.description" placeholder="可选" />
                </el-form-item>
              </el-col>
            </el-row>
          </el-form>
        </el-card>

        <el-card shadow="never" class="hrp-card">
          <template #header>
            <span class="card-title">用例配置（config）</span>
          </template>
          <el-tabs>
            <el-tab-pane label="变量">
              <KeyValueEditor v-model="form.configVariables" key-placeholder="变量名" />
              <div class="hrp-muted note">
                用例级变量，所有步骤可见。引用时写 <code>$变量名</code>。变量未定义是引擎退出码 21（用例问题）。
              </div>
            </el-tab-pane>

            <el-tab-pane label="导出">
              <el-select
                v-model="form.configExport"
                multiple
                filterable
                allow-create
                default-first-option
                placeholder="输入变量名后回车"
                style="width: 100%"
              >
                <el-option v-for="e in form.configExport" :key="e" :label="e" :value="e" />
              </el-select>
              <div class="hrp-muted note">导出到后续用例集执行时可见的变量名。M1 单用例执行用不到，留作准备。</div>
            </el-tab-pane>

            <el-tab-pane label="请求头">
              <KeyValueEditor v-model="form.configHeaders" key-placeholder="Header 名" />
              <div class="hrp-muted note">
                用例级请求头，会被合并进每个步骤（优先级：环境全局头 &lt; 用例头 &lt; 步骤头）。
                编译器刻意不写 <code>config.headers</code>，而是三层合并后下沉到步骤，避免同一请求头因大小写不同被重复发送。
              </div>
            </el-tab-pane>

            <el-tab-pane label="SSL 校验">
              <el-radio-group v-model="form.verifyOverride">
                <el-radio value="">跟随环境（推荐）</el-radio>
                <el-radio value="true">强制校验</el-radio>
                <el-radio value="false">强制不校验</el-radio>
              </el-radio-group>
              <div class="hrp-muted note">
                引擎默认**开启** TLS 校验；本地 HTTP 环境需要关掉，否则会报
                <code>x509: certificate signed by unknown authority</code>。默认值取自环境的 verify_ssl 设置。
              </div>
            </el-tab-pane>
          </el-tabs>
        </el-card>

        <el-card shadow="never" class="hrp-card">
          <template #header>
            <div class="steps-head">
              <span class="card-title">步骤（{{ enabledStepCount }} / {{ steps.length }} 启用）</span>
              <el-button size="small" type="primary" @click="addStep">
                <el-icon><Plus /></el-icon>添加步骤
              </el-button>
            </div>
          </template>

          <el-alert
            v-if="enabledStepCount === 0"
            type="error"
            show-icon
            :closable="false"
            class="steps-alert"
            title="没有任何启用的步骤，用例无法编译"
          >
            引擎没有"跳过步骤"语法，禁用步骤在编译期就被剔除；全部禁用等于空用例，
            会被引擎静默丢弃（退出码 0、零告警，却在 summary.json 里报 success），因此编译器会直接拒绝。
          </el-alert>

          <StepEditor
            v-for="(step, i) in steps"
            :key="step.uid"
            :step="step"
            :index="i"
            :total="steps.length"
            @remove="removeStep"
            @duplicate="duplicateStep"
            @move="moveStep"
          />
        </el-card>
      </div>

      <div class="grid__side">
        <el-card shadow="never" class="hrp-card side-card">
          <el-tabs v-model="activeTab">
            <el-tab-pane label="校验结果" name="validate">
              <ValidateIssues
                :outcome="validateOutcome"
                :loading="validateLoading"
                :error="validateError"
                :disabled-reason="validateDisabledReason"
              />
              <el-button
                v-if="isEdit"
                class="side-btn"
                size="small"
                :loading="validateLoading"
                @click="runValidate"
              >
                重新校验
              </el-button>
            </el-tab-pane>

            <el-tab-pane label="编译后 YAML" name="yaml">
              <YamlPreview :preview="preview" :loading="previewLoading" :error="previewError" />
              <el-button v-if="isEdit" class="side-btn" size="small" :loading="previewLoading" @click="refreshPreview">
                重新编译
              </el-button>
            </el-tab-pane>
          </el-tabs>
        </el-card>
      </div>
    </div>
  </div>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.grid {
  display: flex;
  gap: 14px;
  align-items: flex-start;
}

.grid__main {
  flex: 1;
  min-width: 0;
}

.grid__side {
  width: 480px;
  flex: none;
  position: sticky;
  top: 0;
}

.card-title {
  font-weight: 600;
  font-size: 13px;
}

.tip,
.note {
  font-size: 12px;
  line-height: 1.7;
  color: var(--hrp-muted);
}

.tip {
  margin-top: 4px;
}

.note {
  margin-top: 6px;
}

.note code,
.hrp-muted code {
  font-family: var(--hrp-mono);
  background: #f2f3f5;
  border-radius: 3px;
  padding: 0 3px;
}

.steps-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.steps-alert {
  margin-bottom: 12px;
}

.side-card {
  max-height: calc(100vh - 120px);
  overflow: auto;
}

.side-btn {
  margin-top: 8px;
}
</style>
