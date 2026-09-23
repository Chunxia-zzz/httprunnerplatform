<script setup lang="ts">
/**
 * 统计看板（M4）。
 *
 * 三个图，都基于 run_record / case_result 的历史聚合，无新增表：
 *   1. 通过率趋势 —— 折线（通过率）+ 柱状（执行次数），看「整体健康度随时间的变化」
 *   2. 不稳定/常败用例 —— 横向条形，按通过率着色：0 = 常败（红）、(0,1) = 不稳定（橙）
 *   3. 慢用例 —— 横向条形，按平均耗时降序
 *
 * 图表配色刻意不随涨跌语义走（这是测试通过率，不是股价），
 * 红 = 差、绿 = 好，与直觉一致。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { statsApi, type FlakyCase, type SlowCase, type TrendPoint } from '@/api'
import { useProjectStore } from '@/stores/project'
import { formatDuration } from '@/utils/format'
import { notifyError } from '@/utils/error'
import echarts from '@/utils/echarts'

const projects = useProjectStore()
const projectId = computed(() => projects.currentId)

const DAY_OPTIONS = [
  { label: '近 7 天', value: 7 },
  { label: '近 14 天', value: 14 },
  { label: '近 30 天', value: 30 },
]
const days = ref(30)
const loading = ref(false)

const trend = ref<TrendPoint[]>([])
const flaky = ref<FlakyCase[]>([])
const slowest = ref<SlowCase[]>([])

const trendEl = ref<HTMLElement | null>(null)
const flakyEl = ref<HTMLElement | null>(null)
const slowEl = ref<HTMLElement | null>(null)

let trendChart: echarts.ECharts | null = null
let flakyChart: echarts.ECharts | null = null
let slowChart: echarts.ECharts | null = null

/** 正文色 / 弱化色，与全局样式对齐。 */
const TEXT = '#303133'
const MUTED = '#909399'
const BORDER = '#dcdfe6'
const RED = '#f56c6c'
const ORANGE = '#e6a23c'
const GREEN = '#67c23a'
const BLUE = '#409eff'

async function load() {
  if (!projectId.value) return
  loading.value = true
  try {
    const base = { project_id: projectId.value, days: days.value }
    const [t, f, s] = await Promise.all([
      statsApi.statsTrend(base),
      statsApi.statsFlaky({ ...base, limit: 20 }),
      statsApi.statsSlowest({ ...base, limit: 20 }),
    ])
    trend.value = t.points
    flaky.value = f.items
    slowest.value = s.items
    // v-if/v-else 刚切到图表分支时 DOM 还没 patch，必须等 nextTick
    // 否则 flakyEl/slowEl 还是 null，renderFlaky 会静默跳过（卡片空白）。
    await nextTick()
    renderCharts()
  } catch (e) {
    notifyError(e, '加载统计数据失败')
  } finally {
    loading.value = false
  }
}

function renderCharts() {
  renderTrend()
  renderFlaky()
  renderSlowest()
}

function renderTrend() {
  if (!trendEl.value) return
  if (!trendChart) trendChart = echarts.init(trendEl.value)
  const days_ = trend.value.map((p) => p.day.slice(5)) // 只留 MM-DD
  const rates = trend.value.map((p) => Math.round(p.rate * 1000) / 10) // 0~100
  const totals = trend.value.map((p) => p.total)

  trendChart.setOption({
    tooltip: { trigger: 'axis' },
    legend: { data: ['通过率 %', '执行次数'], textStyle: { color: TEXT } },
    grid: { left: 40, right: 20, top: 40, bottom: 30 },
    xAxis: {
      type: 'category',
      data: days_,
      axisLine: { lineStyle: { color: BORDER } },
      axisLabel: { color: MUTED },
    },
    yAxis: [
      {
        type: 'value',
        min: 0,
        max: 100,
        axisLabel: { color: MUTED, formatter: '{value}%' },
        splitLine: { lineStyle: { color: BORDER } },
      },
      {
        type: 'value',
        minInterval: 1,
        axisLabel: { color: MUTED },
        splitLine: { show: false },
      },
    ],
    series: [
      {
        name: '通过率 %',
        type: 'line',
        data: rates,
        smooth: true,
        itemStyle: { color: BLUE },
        areaStyle: { opacity: 0.08 },
      },
      {
        name: '执行次数',
        type: 'bar',
        yAxisIndex: 1,
        data: totals,
        itemStyle: { color: '#c0c4cc' },
        barMaxWidth: 18,
      },
    ],
  })
}

/** 通过率 → 颜色与语义档。 */
function flakyMeta(rate: number): { color: string; label: string } {
  if (rate <= 0) return { color: RED, label: '常败' }
  if (rate < 0.9) return { color: ORANGE, label: '不稳定' }
  return { color: GREEN, label: '健康' }
}

function renderFlaky() {
  if (!flakyEl.value) return
  if (!flakyChart) flakyChart = echarts.init(flakyEl.value)
  // 横向条形：名称在上、数值大的在上 → 用反转让「最不稳定」排最上。
  const items = [...flaky.value].reverse()
  const names = items.map((c) => c.config_name || c.case_code)
  const rates = items.map((c) => Math.round(c.rate * 1000) / 10)

  flakyChart.setOption({
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (ps: Array<{ dataIndex: number }>) => {
        const i = ps[0]?.dataIndex ?? 0
        const c = items[i]
        if (!c) return ''
        const meta = flakyMeta(c.rate)
        return `${c.config_name || c.case_code}<br/>通过率 ${(c.rate * 100).toFixed(1)}%（${meta.label}）<br/>${c.passed}/${c.runs} 次通过`
      },
    },
    grid: { left: 120, right: 50, top: 20, bottom: 30 },
    xAxis: {
      type: 'value',
      min: 0,
      max: 100,
      axisLabel: { color: MUTED, formatter: '{value}%' },
      splitLine: { lineStyle: { color: BORDER } },
    },
    yAxis: {
      type: 'category',
      data: names,
      axisLabel: { color: TEXT, width: 100, overflow: 'truncate' },
      axisLine: { lineStyle: { color: BORDER } },
    },
    series: [
      {
        type: 'bar',
        data: rates.map((r, i) => ({
          value: r,
          itemStyle: { color: flakyMeta(items[i].rate).color },
        })),
        barMaxWidth: 16,
        label: { show: true, position: 'right', color: MUTED, formatter: '{c}%' },
      },
    ],
  })
}

function renderSlowest() {
  if (!slowEl.value) return
  if (!slowChart) slowChart = echarts.init(slowEl.value)
  const items = [...slowest.value].reverse()
  const names = items.map((c) => c.config_name || c.case_code)
  const avg = items.map((c) => c.avg_ms)

  slowChart.setOption({
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'shadow' },
      formatter: (ps: Array<{ dataIndex: number }>) => {
        const i = ps[0]?.dataIndex ?? 0
        const c = items[i]
        if (!c) return ''
        return `${c.config_name || c.case_code}<br/>平均 ${formatDuration(c.avg_ms)}<br/>最慢 ${formatDuration(c.max_ms)}（${c.runs} 次）`
      },
    },
    grid: { left: 120, right: 60, top: 20, bottom: 30 },
    xAxis: {
      type: 'value',
      axisLabel: { color: MUTED, formatter: (v: number) => formatDuration(v) },
      splitLine: { lineStyle: { color: BORDER } },
    },
    yAxis: {
      type: 'category',
      data: names,
      axisLabel: { color: TEXT, width: 100, overflow: 'truncate' },
      axisLine: { lineStyle: { color: BORDER } },
    },
    series: [
      {
        type: 'bar',
        data: avg.map((v) => ({ value: v, itemStyle: { color: ORANGE } })),
        barMaxWidth: 16,
        label: {
          show: true,
          position: 'right',
          color: MUTED,
          formatter: (p: { value: number }) => formatDuration(p.value),
        },
      },
    ],
  })
}

function handleResize() {
  trendChart?.resize()
  flakyChart?.resize()
  slowChart?.resize()
}

watch(days, () => void load())
watch(projectId, (n, o) => {
  if (n !== o) void load()
})

onMounted(async () => {
  if (!projects.loaded) await projects.fetchList()
  await load()
  window.addEventListener('resize', handleResize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', handleResize)
  trendChart?.dispose()
  flakyChart?.dispose()
  slowChart?.dispose()
})

const hasAnyData = computed(
  () => trend.value.some((p) => p.total > 0) || flaky.value.length > 0 || slowest.value.length > 0,
)
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h1 class="hrp-page__title">统计看板</h1>
        <p class="hrp-page__sub">通过率趋势、不稳定用例与慢用例，基于执行历史聚合</p>
      </div>
      <el-radio-group v-model="days" size="small">
        <el-radio-button v-for="o in DAY_OPTIONS" :key="o.value" :value="o.value">
          {{ o.label }}
        </el-radio-button>
      </el-radio-group>
    </div>

    <div v-loading="loading" class="stats">
      <el-empty v-if="!loading && !hasAnyData" description="暂无执行数据，先跑几次用例再看趋势吧" />

      <template v-else>
        <el-card shadow="never" class="stats__card">
          <template #header>
            <span class="stats__title">通过率趋势</span>
            <span class="hrp-muted stats__sub">柱 = 每日执行次数，线 = 通过率</span>
          </template>
          <div ref="trendEl" class="stats__chart"></div>
        </el-card>

        <div class="stats__row">
          <el-card shadow="never" class="stats__card">
            <template #header>
              <span class="stats__title">不稳定 / 常败用例</span>
              <span class="hrp-muted stats__sub">通过率越低越靠前</span>
            </template>
            <div v-if="flaky.length === 0" class="hrp-empty-hint">暂无失败用例</div>
            <div v-else ref="flakyEl" class="stats__chart"></div>
          </el-card>

          <el-card shadow="never" class="stats__card">
            <template #header>
              <span class="stats__title">慢用例排行</span>
              <span class="hrp-muted stats__sub">按平均耗时降序</span>
            </template>
            <div v-if="slowest.length === 0" class="hrp-empty-hint">暂无执行耗时数据</div>
            <div v-else ref="slowEl" class="stats__chart"></div>
          </el-card>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.stats__card {
  margin-bottom: 14px;
}
.stats__title {
  font-weight: 600;
  font-size: 14px;
}
.stats__sub {
  margin-left: 8px;
  font-size: 12px;
  font-weight: normal;
}
.stats__chart {
  width: 100%;
  height: 300px;
}
.stats__row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 14px;
}
@media (max-width: 1100px) {
  .stats__row {
    grid-template-columns: 1fr;
  }
}
</style>
