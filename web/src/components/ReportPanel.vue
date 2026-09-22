<script setup lang="ts">
/**
 * HTML 报告内嵌面板。
 *
 * ⚠️ 关键点：**断言失败时报告必然不存在**（实测 F8 —— 引擎在断言阶段 panic，
 * 根本走不到生成报告那一步）。所以"没有报告"是高频正常路径，
 * 必须给友好文案 + 原因说明，而不是显示一个破图的 iframe。
 *
 * 实现上先做一次 fetch 探状态，再把 iframe 指向**真实 URL**（而不是 blob:）：
 *   - blob: 文档的 origin 是 null，报告里的相对资源与 localStorage 都会失效；
 *   - 直连 URL 与页面同源，Cookie 自动携带，报告以真实 origin 渲染。
 * 代价是多一次请求，但换来的是"状态可判定"与"渲染正确"两者兼得。
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import { runApi, type ReportResult } from '@/api'
import { apiMessage } from '@/utils/error'

const props = defineProps<{
  runId: number
  caseResultId?: number
  /** 服务端 stat 出来的产物是否存在，用于先给出结论再决定要不要请求 */
  hasReport: boolean
}>()

const loading = ref(false)
const result = ref<ReportResult | null>(null)
const failed = ref('')

/** iframe 直接指向的地址：同源、带 Cookie、真实 origin。 */
const reportUrl = computed(() => {
  const params = props.caseResultId ? `?case_result_id=${props.caseResultId}` : ''
  return `/api/v1/runs/${props.runId}/report${params}`
})

async function check() {
  failed.value = ''
  result.value = null

  if (!props.hasReport) {
    // 服务端已经数过盘了，不必再打一次注定 404 的请求。
    result.value = {
      ok: false,
      html: '',
      message: '本次执行未产出 HTML 报告（引擎在断言失败时会 panic，不会生成报告）',
    }
    return
  }

  loading.value = true
  try {
    result.value = await runApi.fetchReport(props.runId, props.caseResultId)
  } catch (e) {
    failed.value = apiMessage(e, '获取报告失败')
  } finally {
    loading.value = false
  }
}

function openInNewWindow() {
  window.open(reportUrl.value, '_blank', 'noopener')
}

watch(() => [props.runId, props.caseResultId, props.hasReport], check, { immediate: true })

onBeforeUnmount(() => {
  result.value = null
})
</script>

<template>
  <div v-loading="loading" class="rp">
    <el-alert v-if="failed" type="error" show-icon :closable="false" :title="failed" />

    <template v-else-if="result?.ok">
      <div class="rp__bar">
        <span class="hrp-muted">
          报告是自包含单文件（引擎产出，无外部静态资源依赖），内嵌展示与在浏览器直接打开等价。
        </span>
        <el-button size="small" link type="primary" @click="openInNewWindow">在新窗口打开</el-button>
      </div>
      <iframe :src="reportUrl" class="rp__frame" title="HTML 报告" />
    </template>

    <el-alert v-else type="info" show-icon :closable="false" title="本次执行没有 HTML 报告">
      <div class="rp__why">
        <p>{{ result?.message }}</p>
        <p>
          这不是平台把报告弄丢了，而是引擎的既定行为：<strong>断言未通过时会 panic</strong>，
          进程在生成报告之前就退出了（退出码 2，且 summary.json 也不会产出）。
        </p>
        <p>
          失败线索请到「步骤明细」看：平台会从 stdout 的报文快照重建失败步骤的请求/响应，
          并用「用例声明的断言 + 快照」把不通过的那一条断言标出来（来源列会显示「平台重建」）。
          原始日志在「原始日志」页，用来做离线复核。
        </p>
      </div>
    </el-alert>
  </div>
</template>

<style scoped>
.rp__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
  font-size: 12px;
}

.rp__frame {
  width: 100%;
  height: 72vh;
  border: 1px solid var(--hrp-border);
  border-radius: 4px;
  background: #fff;
}

.rp__why p {
  margin: 0 0 8px;
  line-height: 1.8;
}

.rp__why p:last-child {
  margin-bottom: 0;
}
</style>
