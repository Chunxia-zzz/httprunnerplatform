<script setup lang="ts">
/**
 * 执行详情。
 *
 * 这是 M1 验收标准的主要落点：
 *   ① 看到步骤级失败原因和失败归因标签
 *   ② HTML 报告可在页面内打开（不存在时给友好文案，见 ReportPanel）
 *   ③ 引擎报错能正确标 error 并展示原始日志
 *   ④ 用例数对账生效（count_mismatch）
 *
 * 轮询策略：只有非终态才轮询（1s）。终态后再拉一次步骤明细与日志 ——
 * 步骤明细包含几百 KB 的报文快照，边轮询边拉会让"看进度"变得很贵。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'

import {
  caseApi,
  isTerminalRun,
  runApi,
  type CaseSteps,
  type RunDetail,
  type RunLogs,
} from '@/api'
import ReportPanel from '@/components/ReportPanel.vue'
import StepDetail from '@/components/StepDetail.vue'
import { attrTagType, runStatusMeta, stepStatusMeta } from '@/utils/dict'
import { formatDuration, formatTime } from '@/utils/format'
import { notifyError, notifyOk } from '@/utils/error'

const route = useRoute()
const router = useRouter()

const runId = computed(() => Number(route.params.id))

const loading = ref(false)
const detail = ref<RunDetail | null>(null)
const stepsDetail = ref<CaseSteps[]>([])
const logs = ref<RunLogs | null>(null)
const logsLoading = ref(false)
const cancelling = ref(false)
const failed = ref('')

const activeTab = ref<'steps' | 'report' | 'logs'>('steps')

/** 声明 URL 对照表，key = `${case_code}#${seq}`。 */
const declaredUrls = ref<Record<string, string>>({})

const run = computed(() => detail.value?.run ?? null)
const terminal = computed(() => (run.value ? isTerminalRun(run.value.status) : true))

let timer: number | undefined

async function loadDetail(silent = false) {
  if (!silent) loading.value = true
  try {
    detail.value = await runApi.getRun(runId.value)
    failed.value = ''
    if (terminal.value) {
      stopPoll()
      await loadFinalArtifacts()
    } else {
      startPoll()
    }
  } catch (e) {
    failed.value = e instanceof Error ? e.message : '加载执行详情失败'
    stopPoll()
  } finally {
    loading.value = false
  }
}

async function loadFinalArtifacts() {
  try {
    stepsDetail.value = await runApi.getRunCases(runId.value)
    await loadDeclaredUrls()
  } catch (e) {
    notifyError(e, '加载步骤明细失败')
  }
}

/**
 * 取每条用例的"声明 URL"，用于和 final_url 对照。
 *
 * 用例可能已被删除 —— 那只是少了一处对照信息，不该让整个详情页报错。
 */
async function loadDeclaredUrls() {
  const map: Record<string, string> = {}
  const cases = detail.value?.cases ?? []
  const seen = new Set<number>()
  for (const c of cases) {
    if (!c.case_id || seen.has(c.case_id)) continue
    seen.add(c.case_id)
    try {
      const cs = await caseApi.getCase(c.case_id)
      for (const s of cs.steps) {
        const url = s.request?.url ?? ''
        if (url) map[`${cs.code}#${s.seq}`] = url
      }
    } catch {
      // 忽略：用例被删除或无权访问时不给对照信息
    }
  }
  declaredUrls.value = map
}

function declaredUrlOf(item: CaseSteps, seq: number): string {
  return declaredUrls.value[`${item.case_code}#${seq}`] ?? ''
}

function startPoll() {
  if (timer !== undefined) return
  timer = window.setInterval(() => void loadDetail(true), 1000)
}

function stopPoll() {
  if (timer !== undefined) {
    window.clearInterval(timer)
    timer = undefined
  }
}

async function cancel() {
  try {
    await ElMessageBox.confirm(
      '终止执行会杀掉 hrp 子进程树。已产生的报文快照仍会保留在日志里。',
      '终止执行',
      { type: 'warning', confirmButtonText: '终止', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  cancelling.value = true
  try {
    await runApi.cancelRun(runId.value)
    notifyOk('已发送终止请求')
    await loadDetail(true)
  } catch (e) {
    notifyError(e, '终止执行失败')
  } finally {
    cancelling.value = false
  }
}

async function loadLogs() {
  if (logs.value) return
  logsLoading.value = true
  try {
    logs.value = await runApi.getRunLogs(runId.value)
  } catch (e) {
    notifyError(e, '加载原始日志失败')
  } finally {
    logsLoading.value = false
  }
}

/** 归因计数渲染成「标签 + 数量」。 */
function attrEntries(): Array<{ key: string; count: number }> {
  const m = run.value?.attribution
  if (!m) return []
  return Object.entries(m).map(([key, count]) => ({ key, count: Number(count) }))
}

watch(activeTab, (t) => {
  if (t === 'logs') void loadLogs()
})

watch(runId, () => {
  stopPoll()
  stepsDetail.value = []
  logs.value = null
  declaredUrls.value = {}
  void loadDetail()
})

onMounted(() => void loadDetail())
onBeforeUnmount(stopPoll)
</script>

<template>
  <div v-loading="loading" class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">
          执行详情
          <span class="hrp-mono hrp-muted">#{{ runId }}</span>
        </h2>
        <p class="hrp-page__sub">
          {{ run?.target_name || '—' }}
          <template v-if="run?.env_name"> · 环境 {{ run.env_name }}</template>
        </p>
      </div>
      <div class="head-actions">
        <el-button @click="router.push({ name: 'runs' })">返回列表</el-button>
        <el-button :loading="loading" @click="loadDetail()">
          <el-icon><Refresh /></el-icon>刷新
        </el-button>
        <el-button v-if="!terminal" type="danger" :loading="cancelling" @click="cancel">终止执行</el-button>
      </div>
    </div>

    <el-alert v-if="failed" type="error" show-icon :closable="false" :title="failed" class="hrp-card" />

    <template v-if="run">
      <el-card shadow="never" class="hrp-card">
        <div class="summary">
          <div class="summary__item">
            <div class="summary__label">状态</div>
            <el-tag :type="runStatusMeta(run.status).type" effect="dark">{{ runStatusMeta(run.status).label }}</el-tag>
            <span v-if="!terminal" class="hrp-muted summary__note">执行中，每秒刷新</span>
          </div>
          <div class="summary__item">
            <div class="summary__label">耗时</div>
            <div class="summary__value">{{ formatDuration(run.duration_ms) }}</div>
          </div>
          <div class="summary__item">
            <div class="summary__label">用例</div>
            <div class="summary__value">
              通过 {{ run.passed }} · 失败 {{ run.failed }} · 错误 {{ run.error }} · 跳过 {{ run.skipped }}
            </div>
          </div>
          <div class="summary__item">
            <div class="summary__label">用例数对账</div>
            <div class="summary__value">
              预期 {{ run.expected_case_count }} / 实际 {{ run.actual_case_count }}
              <el-tag v-if="run.count_mismatch" size="small" type="danger" effect="dark">不一致</el-tag>
              <el-tag v-else size="small" type="success" effect="plain">一致</el-tag>
            </div>
          </div>
          <div class="summary__item">
            <div class="summary__label">归因</div>
            <div class="summary__value">
              <template v-if="attrEntries().length">
                <el-tag
                  v-for="a in attrEntries()"
                  :key="a.key"
                  size="small"
                  :type="attrTagType(a.key)"
                  effect="plain"
                  class="attr-tag"
                >
                  {{ a.key }} ×{{ a.count }}
                </el-tag>
              </template>
              <span v-else class="hrp-muted">—</span>
            </div>
          </div>
          <div class="summary__item">
            <div class="summary__label">开始 / 结束</div>
            <div class="summary__value">
              {{ formatTime(run.started_at, '未开始') }} → {{ formatTime(run.finished_at, '未结束') }}
            </div>
          </div>
        </div>

        <el-alert
          v-if="run.count_mismatch"
          type="error"
          show-icon
          :closable="false"
          class="summary__alert"
          title="用例数对账不一致：这次执行没有被真正跑起来"
        >
          预期执行 {{ run.expected_case_count }} 条用例，实际只有 {{ run.actual_case_count }} 条被执行。
          这种情况会被强制标为 error —— 因为退出码再好看也不代表跑到了：畸形用例会被引擎
          <strong>静默丢弃</strong>（退出码 0、零告警、summary.json 里 success 还是 true），
          只看退出码就会把它记成一次"全绿"的成功。
        </el-alert>

        <el-alert
          v-if="run.error_msg"
          type="warning"
          show-icon
          :closable="false"
          class="summary__alert"
          title="平台记录的错误信息"
        >
          {{ run.error_msg }}
        </el-alert>

        <div class="meta">
          <span class="hrp-muted">工作区</span>
          <span class="hrp-mono meta__path">{{ run.workspace_path || '—' }}</span>
        </div>
      </el-card>

      <el-card v-for="c in detail?.cases ?? []" :key="c.id" shadow="never" class="hrp-card">
        <template #header>
          <div class="case-head">
            <span class="case-head__name">{{ c.config_name || c.case_code }}</span>
            <span class="hrp-mono hrp-muted">{{ c.case_code }}</span>
            <el-tag size="small" :type="stepStatusMeta(c.status).type" effect="dark">
              {{ stepStatusMeta(c.status).label }}
            </el-tag>
            <el-tag size="small" :type="attrTagType(c.attribution)" effect="plain">
              归因：{{ c.attribution_label }}
            </el-tag>
            <span class="hrp-muted case-head__ms">{{ formatDuration(c.duration_ms) }}</span>
          </div>
        </template>

        <el-descriptions :column="4" size="small" border class="case-desc">
          <el-descriptions-item label="步骤">
            共 {{ c.step_total }} · 通过 {{ c.step_passed }}
            <template v-if="c.step_failed"> · 失败 {{ c.step_failed }}</template>
            <template v-if="c.step_error"> · 错误 {{ c.step_error }}</template>
          </el-descriptions-item>
          <el-descriptions-item label="退出码">{{ c.exit_code }}</el-descriptions-item>
          <el-descriptions-item label="引擎 panic">
            <el-tag size="small" :type="c.panic ? 'danger' : 'success'" effect="plain">
              {{ c.panic ? '是' : '否' }}
            </el-tag>
          </el-descriptions-item>
          <!--
            ⚠️ 不要把这个字段叫「正常退出」，也不要用 success 配色。
            clean_exit 的语义是「进程是主动退出并给出了明确错误码」，**不是"是否成功"**
            （判据见 parser.Parse：exit=0 或 stdout 出现 `hrp exit N`，而后者只在 N≠0 时打印）。
            于是 exit=1 的连接失败会得到 clean_exit=true —— 若标成绿色的"正常退出：是"，
            用户会在一个写着「错误」的页面上读到"一切正常"，这正是本系统最该避免的假绿。
          -->
          <el-descriptions-item label="退出方式">
            <el-tooltip
              placement="top"
              :content="
                c.clean_exit
                  ? '进程自己带着错误码收尾（退出码非 0 也算这一类）。只说明它没被强杀，不代表用例成功。'
                  : '进程没走到收尾：panic 崩溃，或被平台终止 / 超时强杀。'
              "
            >
              <el-tag size="small" :type="c.clean_exit ? 'info' : 'warning'" effect="plain">
                {{ c.clean_exit ? '主动退出' : '崩溃 / 被终止' }}
              </el-tag>
            </el-tooltip>
          </el-descriptions-item>
        </el-descriptions>

        <el-alert type="info" show-icon :closable="false" class="case-why" :title="`归因：${c.attribution_label}`">
          {{ c.attribution_reason }}
        </el-alert>

        <el-alert
          v-if="c.error_msg"
          type="error"
          show-icon
          :closable="false"
          class="case-why"
          title="用例错误信息"
        >
          <pre class="hrp-pre case-why__pre">{{ c.error_msg }}</pre>
        </el-alert>

        <el-tabs v-model="activeTab" class="case-tabs">
          <el-tab-pane label="步骤明细" name="steps">
            <template v-if="stepsDetail.length === 0">
              <div class="hrp-empty-hint">
                {{ terminal ? '没有步骤结果（用例可能一条都没跑起来，看上方对账信息）' : '执行中，完成后展示步骤明细…' }}
              </div>
            </template>
            <template v-else>
              <template v-for="item in stepsDetail" :key="item.case_result_id">
                <StepDetail
                  v-for="s in item.steps"
                  :key="s.id"
                  :step="s"
                  :declared-url="declaredUrlOf(item, s.seq)"
                />
              </template>
            </template>
          </el-tab-pane>

          <el-tab-pane label="HTML 报告" name="report" lazy>
            <ReportPanel :run-id="runId" :case-result-id="c.id" :has-report="c.has_report" />
          </el-tab-pane>

          <el-tab-pane label="原始日志" name="logs" lazy>
            <div v-loading="logsLoading">
              <el-alert
                v-if="logs?.truncated"
                type="warning"
                show-icon
                :closable="false"
                class="logs-alert"
                title="日志过大，已截断展示"
              >
                完整内容在服务器上：<span class="hrp-mono">{{ run.log_path || '见工作区目录' }}</span>。
                原始日志永久保留，用于引擎输出格式漂移时离线重放修复历史数据。
              </el-alert>

              <div class="logs-block">
                <div class="logs-title">
                  stdout — 请求/响应报文快照
                  <span class="hrp-muted logs-hint">断言失败时这是唯一可用的失败线索（引擎 panic 不产出 summary.json）</span>
                </div>
                <pre class="hrp-pre hrp-pre--tall">{{ logs?.stdout || '（空）' }}</pre>
              </div>

              <div class="logs-block">
                <div class="logs-title">
                  stderr — 引擎结构化日志（JSON Lines）
                  <span class="hrp-muted logs-hint">含可能的 Go 栈回溯</span>
                </div>
                <pre class="hrp-pre hrp-pre--tall">{{ logs?.stderr || '（空）' }}</pre>
              </div>
            </div>
          </el-tab-pane>
        </el-tabs>
      </el-card>
    </template>
  </div>
</template>

<style scoped>
.head-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
}

.summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px 20px;
}

.summary__label {
  font-size: 12px;
  color: var(--hrp-muted);
  margin-bottom: 4px;
}

.summary__value {
  line-height: 1.7;
}

.summary__note {
  font-size: 12px;
  margin-left: 8px;
}

.summary__alert {
  margin-top: 14px;
}

.attr-tag {
  margin-right: 4px;
}

.meta {
  display: flex;
  gap: 8px;
  align-items: baseline;
  margin-top: 14px;
  font-size: 12px;
}

.meta__path {
  word-break: break-all;
}

.case-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.case-head__name {
  font-weight: 600;
  font-size: 14px;
}

.case-head__ms {
  font-size: 12px;
}

.case-desc {
  margin-bottom: 12px;
}

.case-why {
  margin-bottom: 12px;
}

.case-why__pre {
  margin-top: 6px;
  max-height: 200px;
}

.case-tabs :deep(.el-tabs__header) {
  margin-bottom: 12px;
}

.logs-alert {
  margin-bottom: 12px;
}

.logs-block + .logs-block {
  margin-top: 14px;
}

.logs-title {
  font-weight: 600;
  font-size: 13px;
  margin-bottom: 6px;
  display: flex;
  align-items: baseline;
  gap: 8px;
  flex-wrap: wrap;
}

.logs-hint {
  font-weight: 400;
  font-size: 12px;
}
</style>
