<script setup lang="ts">
/**
 * 引擎状态告警条。
 *
 * 为什么单独做成组件：引擎不可用时平台仍然可以正常编辑用例（后端刻意
 * 不阻断启动），所以在页面上必须给一个**跨页面持续可见**的提示，
 * 否则用户会在"点了执行但什么都没发生"之后才开始怀疑。
 *
 * 三种状态要分清：
 *   - 后端不可达（probeError）：连不上服务，什么都不能做
 *   - 引擎不可用（available=false，HTTP 200 + code=50001）：能编辑不能执行
 *   - 正常：不渲染任何东西（避免常驻噪音）
 */
import { computed, onMounted } from 'vue'

import { useEngineStore } from '@/stores/engine'

const engine = useEngineStore()

const severity = computed<'error' | 'warning'>(() => (engine.probeError ? 'error' : 'warning'))

const visible = computed(() => !!engine.probeError || (!!engine.status && !engine.status.available))

const title = computed(() => {
  if (engine.probeError) return '无法连接后端服务'
  return '引擎不可用：用例可以编辑保存，但无法执行'
})

const detail = computed(() => {
  if (engine.probeError) {
    return `${engine.probeError}。请确认后端已启动（默认 127.0.0.1:8080），前端开发服务器的 /api 代理指向它。`
  }
  const s = engine.status
  if (!s) return ''
  const parts: string[] = []
  if (s.error) parts.push(s.error)
  if (s.binary_path) parts.push(`已配置路径：${s.binary_path}`)
  else parts.push('未配置 engine.binary_path')
  parts.push('把 hrp v4.3.6 二进制放到 <工具目录>/bin/ 下，或设置环境变量 HRP_BINARY_PATH。')
  return parts.join(' ')
})

onMounted(() => {
  if (!engine.probedAt) void engine.probe()
})
</script>

<template>
  <el-alert
    v-if="visible"
    class="engine-banner"
    :type="severity"
    show-icon
    :closable="false"
  >
    <template #default>
      <div class="engine-banner__body">
        <div class="engine-banner__text">
          <strong>{{ title }}</strong>
          <p>{{ detail }}</p>
        </div>
        <el-button size="small" :loading="engine.loading" @click="engine.probe()">重新探测</el-button>
      </div>
    </template>
  </el-alert>
</template>

<style scoped>
.engine-banner {
  margin-bottom: 12px;
}

.engine-banner__body {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  justify-content: space-between;
}

.engine-banner__text p {
  margin: 6px 0 0;
  line-height: 1.6;
}
</style>
