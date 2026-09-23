<script setup lang="ts">
/**
 * 用例基线对比对话框（M4-d）。
 *
 * 选定一条用例，并排展示它最近 N 次执行结果，回答「这条用例是从哪一次
 * 开始变坏的」。渲染逻辑刻意从后端拿「事实」（状态/耗时/步骤数），
 * 由前端把「变坏 / 恢复」的相邻差异信号可视化出来。
 */
import { computed, ref, watch } from 'vue'
import { baselineApi, type BaselineResult, type BaselineRun } from '@/api'
import { stepStatusMeta, attrTagType, triggerTypeMeta } from '@/utils/dict'
import { formatDuration, formatTime } from '@/utils/format'
import { notifyError } from '@/utils/error'
import { useProjectStore } from '@/stores/project'

const props = defineProps<{
  modelValue: boolean
  /** 要对比的用例 id。为 0 时不加载。 */
  caseId: number
  /** 用例名，用于标题。 */
  caseName?: string
  /** 用例标识，用于副标题。 */
  caseCode?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
}>()

const projects = useProjectStore()

const visible = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const limit = ref(5)
const loading = ref(false)
const result = ref<BaselineResult | null>(null)

async function load() {
  if (!props.caseId || !projects.currentId) return
  loading.value = true
  result.value = null
  try {
    result.value = await baselineApi.caseBaseline({
      project_id: projects.currentId,
      case_id: props.caseId,
      limit: limit.value,
    })
  } catch (e) {
    notifyError(e, '加载基线对比失败')
  } finally {
    loading.value = false
  }
}

/** 对话框打开或 caseId 变化时加载。 */
watch([() => props.modelValue, () => props.caseId], ([open]) => {
  if (open && props.caseId) void load()
})

function close() {
  visible.value = false
}

/** 单次执行的时长差显示：正数红（变慢），负数绿（变快）。 */
function deltaText(r: BaselineRun): string {
  const d = r.delta
  if (!d) return '—'
  const sign = d.dur_delta_ms > 0 ? '+' : ''
  return `${sign}${d.dur_delta_ms}ms`
}

function deltaClass(r: BaselineRun): string {
  const d = r.delta
  if (!d) return ''
  if (d.dur_delta_ms > 0) return 'delta--slower'
  if (d.dur_delta_ms < 0) return 'delta--faster'
  return ''
}
</script>

<template>
  <el-dialog
    v-model="visible"
    :title="`基线对比 · ${caseName || ''}`"
    width="860px"
    top="6vh"
    destroy-on-close
    @closed="result = null"
  >
    <div v-if="caseCode" class="baseline-sub">
      标识 <span class="hrp-mono">{{ caseCode }}</span>
      <el-select v-model="limit" size="small" style="width: 120px; margin-left: 12px" @change="load">
        <el-option :value="3" label="最近 3 次" />
        <el-option :value="5" label="最近 5 次" />
        <el-option :value="10" label="最近 10 次" />
        <el-option :value="20" label="最近 20 次" />
      </el-select>
      <el-button size="small" :loading="loading" style="margin-left: 8px" @click="load">刷新</el-button>
    </div>

    <el-empty v-if="!loading && result && result.runs.length === 0" description="该用例还没有执行历史" />

    <div v-else v-loading="loading" class="baseline-body">
      <p v-if="result" class="hrp-muted baseline-note">
        共执行 {{ result.total_runs }} 次，以下为最近 {{ result.runs.length }} 次（从左到右由旧到新）。
        红框 = 较上一次变坏，绿框 = 较上一次恢复。
      </p>

      <div v-if="result" class="baseline-grid">
        <div
          v-for="(r, i) in result.runs"
          :key="r.run_id"
          class="baseline-card"
          :class="{
            'card--broke': r.delta?.broke,
            'card--recovered': r.delta?.recovered,
          }"
        >
          <div class="card__head">
            <span class="card__seq">#{{ i + 1 }}</span>
            <el-tag size="small" :type="stepStatusMeta(r.status).type" effect="plain">
              {{ stepStatusMeta(r.status).label }}
            </el-tag>
          </div>

          <div class="card__row">
            <span class="hrp-muted">耗时</span>
            <span>{{ formatDuration(r.duration_ms) }}</span>
            <span v-if="r.delta" :class="['card__delta', deltaClass(r)]">({{ deltaText(r) }})</span>
          </div>

          <div class="card__row">
            <span class="hrp-muted">步骤</span>
            <span>
              {{ r.step_passed }}/{{ r.step_total }} 通过
              <template v-if="r.step_failed"> · <span class="text-danger">{{ r.step_failed }} 败</span></template>
              <template v-if="r.step_error"> · <span class="text-warning">{{ r.step_error }} 错</span></template>
            </span>
          </div>

          <div class="card__row">
            <span class="hrp-muted">归因</span>
            <el-tag v-if="r.attribution" size="small" :type="attrTagType(r.attribution)" effect="plain">
              {{ r.attribution }}
            </el-tag>
            <span v-else class="hrp-muted">—</span>
          </div>

          <div class="card__row">
            <span class="hrp-muted">触发</span>
            <span>{{ triggerTypeMeta(r.trigger_type).label }}</span>
          </div>

          <div class="card__row">
            <span class="hrp-muted">时间</span>
            <span class="card__time">{{ formatTime(r.finished_at) }}</span>
          </div>

          <div v-if="r.error_msg" class="card__err" :title="r.error_msg">{{ r.error_msg }}</div>
        </div>
      </div>
    </div>

    <template #footer>
      <el-button @click="close">关闭</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.baseline-sub {
  margin-bottom: 12px;
  font-size: 13px;
  display: flex;
  align-items: center;
}

.baseline-note {
  font-size: 12px;
  margin: 0 0 12px;
}

.baseline-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(210px, 1fr));
  gap: 10px;
}

.baseline-card {
  border: 1px solid var(--el-border-color-light);
  border-radius: 6px;
  padding: 10px;
  background: var(--el-fill-color-blank);
  transition: border-color 0.15s;
}

.card--broke {
  border-color: var(--el-color-danger);
  box-shadow: 0 0 0 1px var(--el-color-danger-light-7);
}

.card--recovered {
  border-color: var(--el-color-success);
  box-shadow: 0 0 0 1px var(--el-color-success-light-7);
}

.card__head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}

.card__seq {
  font-weight: 600;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}

.card__row {
  display: flex;
  align-items: baseline;
  gap: 6px;
  font-size: 12.5px;
  line-height: 1.9;
}

.card__row .hrp-muted {
  flex: none;
  width: 34px;
}

.card__delta {
  font-size: 11px;
}

.delta--slower {
  color: var(--el-color-danger);
}

.delta--faster {
  color: var(--el-color-success);
}

.card__time {
  font-size: 11.5px;
  color: var(--el-text-color-regular);
}

.card__err {
  margin-top: 8px;
  padding: 5px 7px;
  font-size: 11px;
  color: var(--el-color-danger);
  background: var(--el-color-danger-light-9);
  border-radius: 4px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.text-danger {
  color: var(--el-color-danger);
}

.text-warning {
  color: var(--el-color-warning);
}
</style>
