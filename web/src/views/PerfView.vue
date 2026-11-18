<script setup lang="ts">
/**
 * 性能回归（F11）。
 *
 * 读法：每个接口「最近一次耗时」对「最近 N 次执行的中位数基线」。
 * 结论用双阈值判定（相对 % + 绝对 ms），避免 2ms→3ms 这种噪音被报成回归。
 *
 * 支持从执行详情带 run_id 进来：只看那次执行涉及的接口。
 */
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { perfApi, type PerfItem, type PerfResult } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError } from '@/utils/error'

const route = useRoute()
const router = useRouter()
const projects = useProjectStore()

const loading = ref(false)
const result = ref<PerfResult | null>(null)

const q = reactive({
  run_id: route.query.run_id ? Number(route.query.run_id) : 0,
  days: 30,
  baseline_runs: 10,
  threshold_pct: 30,
  min_delta_ms: 50,
  min_samples: 3,
  verdict: '',
})

const VERDICT_META: Record<string, { label: string; type: 'danger' | 'success' | 'info' | 'warning' }> = {
  regressed: { label: '回归', type: 'danger' },
  improved: { label: '变快', type: 'success' },
  stable: { label: '稳定', type: 'info' },
  insufficient: { label: '样本不足', type: 'warning' },
  no_baseline: { label: '无基线', type: 'info' },
}

const summary = computed(() => result.value?.summary)

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    result.value = await perfApi.getPerf(projects.currentId, {
      run_id: q.run_id || undefined,
      days: q.days,
      baseline_runs: q.baseline_runs,
      threshold_pct: q.threshold_pct,
      min_delta_ms: q.min_delta_ms,
      min_samples: q.min_samples,
      verdict: q.verdict || undefined,
    })
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function search() {
  const query = { ...route.query }
  if (q.run_id) query.run_id = String(q.run_id)
  else delete query.run_id
  router.replace({ query })
  load()
}

function clearRun() {
  q.run_id = 0
  search()
}

function rowClass({ row }: { row: PerfItem }) {
  return row.verdict === 'regressed' ? 'row--regressed' : ''
}

/** 迷你趋势：按最大值归一成条形高度。 */
function bars(item: PerfItem): { h: number; ms: number; at: string }[] {
  const max = Math.max(...item.trend.map((p) => p.ms), 1)
  return item.trend.map((p) => ({ h: Math.max(8, Math.round((p.ms / max) * 100)), ms: p.ms, at: p.at }))
}

function fmtPct(v: number): string {
  if (!Number.isFinite(v)) return '—'
  return `${v > 0 ? '+' : ''}${v.toFixed(1)}%`
}

onMounted(load)
watch(() => projects.currentId, load)
</script>

<template>
  <div class="page">
    <div class="page__head">
      <div>
        <h2 class="page__title">
          性能回归
          <el-tag v-if="q.run_id" size="small" type="warning" effect="plain" class="run-tag">
            只看执行 #{{ q.run_id }}
            <el-button link size="small" @click="clearRun">清除</el-button>
          </el-tag>
        </h2>
        <p class="hrp-muted page__desc">
          按「方法 + 路径」聚合（数字/UUID 段归一为 <code>:id</code>）；基线取最近 N 次执行耗时的中位数。
          回归需同时满足相对阈值与绝对毫秒下限。通知出口（F2）未实现，本期为站内标红。
        </p>
      </div>
    </div>

    <div class="filters">
      <span class="hrp-muted">窗口</span>
      <el-input-number v-model="q.days" :min="1" :max="365" controls-position="right" style="width: 110px" />
      <span class="hrp-muted">基线次数</span>
      <el-input-number v-model="q.baseline_runs" :min="1" :max="100" controls-position="right" style="width: 110px" />
      <span class="hrp-muted">阈值 %</span>
      <el-input-number v-model="q.threshold_pct" :min="1" :max="1000" controls-position="right" style="width: 110px" />
      <span class="hrp-muted">最小差(ms)</span>
      <el-input-number v-model="q.min_delta_ms" :min="0" :max="60000" controls-position="right" style="width: 120px" />
      <el-select v-model="q.verdict" placeholder="全部结论" clearable style="width: 130px" @change="search">
        <el-option v-for="(m, k) in VERDICT_META" :key="k" :label="m.label" :value="k" />
      </el-select>
      <el-button type="primary" :loading="loading" @click="search">分析</el-button>
    </div>

    <div v-if="summary" class="summary">
      <el-tag effect="plain">接口 {{ summary.endpoints }}</el-tag>
      <el-tag type="danger" effect="plain">回归 {{ summary.regressed }}</el-tag>
      <el-tag type="success" effect="plain">变快 {{ summary.improved }}</el-tag>
      <el-tag type="info" effect="plain">稳定 {{ summary.stable }}</el-tag>
      <el-tag type="warning" effect="plain">样本不足 {{ summary.insufficient }}</el-tag>
      <span class="hrp-muted applied">
        生效参数：{{ result?.applied.days }} 天 / 基线 {{ result?.applied.baseline_runs }} 次 /
        阈值 {{ result?.applied.threshold_pct }}% 且 ≥{{ result?.applied.min_delta_ms }}ms /
        最少样本 {{ result?.applied.min_samples }}
      </span>
    </div>

    <el-table
      v-loading="loading"
      :data="result?.items ?? []"
      border
      stripe
      :row-class-name="rowClass"
      empty-text="还没有可分析的步骤耗时（执行过带 HTTP 请求的用例后就有了）"
    >
      <el-table-column label="接口" min-width="260">
        <template #default="{ row }">
          <el-tag size="small" effect="plain" class="method">{{ row.method || '—' }}</el-tag>
          <span class="hrp-mono">{{ row.path }}</span>
        </template>
      </el-table-column>
      <el-table-column label="当前" width="100" align="right">
        <template #default="{ row }">{{ row.latest_ms }} ms</template>
      </el-table-column>
      <el-table-column label="基线" width="100" align="right">
        <template #default="{ row }">
          <span v-if="row.samples">{{ row.baseline_ms }} ms</span>
          <span v-else class="hrp-muted">—</span>
        </template>
      </el-table-column>
      <el-table-column label="变化" width="110" align="right">
        <template #default="{ row }">
          <span v-if="row.samples" :class="row.delta_pct > 0 ? 'up' : row.delta_pct < 0 ? 'down' : ''">
            {{ fmtPct(row.delta_pct) }}
          </span>
          <span v-else class="hrp-muted">—</span>
        </template>
      </el-table-column>
      <el-table-column label="结论" width="100">
        <template #default="{ row }">
          <el-tag size="small" :type="VERDICT_META[row.verdict]?.type ?? 'info'" effect="plain">
            {{ VERDICT_META[row.verdict]?.label ?? row.verdict }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="样本" width="70" align="right">
        <template #default="{ row }">{{ row.samples }}</template>
      </el-table-column>
      <el-table-column label="趋势" min-width="140">
        <template #default="{ row }">
          <div class="spark">
            <el-tooltip v-for="(b, i) in bars(row as PerfItem)" :key="i" :content="`${b.ms} ms · ${b.at}`">
              <span
                class="spark__bar"
                :class="{ 'spark__bar--last': i === bars(row as PerfItem).length - 1 }"
                :style="{ height: b.h + '%' }"
              />
            </el-tooltip>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="最近执行" width="150">
        <template #default="{ row }">
          <el-link type="primary" @click="router.push(`/runs/${row.latest_run_id}`)">#{{ row.latest_run_id }}</el-link>
          <div class="hrp-muted at">{{ row.latest_at?.slice(5, 16) }}</div>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<style scoped>
.page__head {
  margin-bottom: 12px;
}
.page__title {
  margin: 0;
  font-size: 18px;
  display: flex;
  align-items: center;
  gap: 10px;
}
.run-tag {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.page__desc {
  margin: 4px 0 0;
  font-size: 12px;
}
.filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.summary {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}
.applied {
  font-size: 12px;
}
.method {
  margin-right: 8px;
}
.up {
  color: #d03050;
}
.down {
  color: #18a058;
}
.spark {
  display: flex;
  align-items: flex-end;
  gap: 2px;
  height: 26px;
}
.spark__bar {
  width: 6px;
  min-height: 4px;
  background: var(--el-color-info-light-5);
  border-radius: 2px 2px 0 0;
}
.spark__bar--last {
  background: var(--el-color-primary);
}
.at {
  font-size: 12px;
}
:deep(.row--regressed) {
  --el-table-tr-bg-color: var(--el-color-danger-light-9);
}
:deep(.row--regressed) .hrp-mono {
  color: var(--el-color-danger);
  font-weight: 600;
}
</style>
