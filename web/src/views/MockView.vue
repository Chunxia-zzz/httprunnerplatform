<script setup lang="ts">
/**
 * Mock 服务（F4）：按接口定义自动生成响应。
 *
 * 页面要讲清三件事：
 *   1. 内容是**自动**的——响应体取自接口库里的接口定义，不需要逐条配置；
 *   2. 基址是 `/mock/{项目code}`，免登录，给前端/上下游直接调用；
 *   3. 命中记录只存在内存里，重启后清空（顶部有提示）。
 */
import { computed, onMounted, ref } from 'vue'

import { mockApi, type MockHit, type MockInfo } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError, notifyOk } from '@/utils/error'

const projects = useProjectStore()

const loading = ref(false)
const info = ref<MockInfo | null>(null)
const hits = ref<MockHit[]>([])
const hitsNote = ref('')
const switching = ref(false)

/** 当前平台地址（页面里给的 curl 示例要能直接跑）。 */
const origin = computed(() => window.location.origin)
const baseUrl = computed(() => (info.value ? `${origin.value}${info.value.base_path}` : ''))

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    info.value = await mockApi.getMockInfo(projects.currentId)
    await loadHits()
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

async function loadHits() {
  if (!projects.currentId) return
  try {
    const r = await mockApi.listMockHits(projects.currentId, 50)
    hits.value = r.hits ?? []
    hitsNote.value = r.note ?? ''
  } catch {
    hits.value = []
  }
}

async function toggle(val: string | number | boolean) {
  const enabled = Boolean(val)
  if (!projects.currentId) return
  switching.value = true
  try {
    await mockApi.setMockEnabled(projects.currentId, enabled)
    notifyOk(enabled ? 'Mock 已开启' : 'Mock 已关闭')
    if (info.value) info.value.enabled = enabled
  } catch (e) {
    notifyError(e)
  } finally {
    switching.value = false
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    notifyOk('已复制')
  } catch {
    notifyError(new Error('浏览器拒绝了剪贴板访问，请手动复制'))
  }
}

function statusType(code: number): 'success' | 'warning' | 'danger' | 'info' {
  if (code < 300) return 'success'
  if (code < 400) return 'info'
  if (code < 500) return 'warning'
  return 'danger'
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page__head">
      <div>
        <h2 class="page__title">Mock 服务</h2>
        <p class="hrp-muted page__desc">
          按接口库里的接口定义自动提供 mock 端点：响应体就是定义里的示例数据，改接口即改 mock。
        </p>
      </div>
      <el-switch
        :model-value="info?.enabled ?? false"
        :loading="switching"
        active-text="已开启"
        inactive-text="未开启"
        @update:model-value="toggle"
      />
    </div>

    <el-card v-loading="loading" shadow="never" class="card">
      <template v-if="info">
        <div class="base">
          <span class="hrp-muted">Mock 基址</span>
          <code class="hrp-mono base__url">{{ baseUrl }}</code>
          <el-button size="small" @click="copy(baseUrl)">复制</el-button>
        </div>
        <div v-if="info.sample_curl" class="base">
          <span class="hrp-muted">示例</span>
          <code class="hrp-mono base__curl">{{ info.sample_curl }}</code>
          <el-button size="small" @click="copy(info.sample_curl)">复制</el-button>
        </div>
        <el-alert type="info" :closable="false" show-icon class="tip">
          <template #title>调用方不需要登录</template>
          mock 端点是公开的（调用方是被测系统/前端，不是平台用户）。响应体完全来自接口定义，
          不碰库里其它数据；未开启或未命中会返回可读的 404 说明。
        </el-alert>
      </template>
    </el-card>

    <el-card shadow="never" class="card">
      <template #header>
        <div class="card__head">
          <span>可被 mock 的接口（{{ info?.apis.length ?? 0 }}）</span>
          <span class="hrp-muted">没有 body 的接口返回 200 空体</span>
        </div>
      </template>
      <el-table :data="info?.apis ?? []" border stripe size="small">
        <el-table-column prop="method" label="方法" width="80" />
        <el-table-column label="路径" min-width="240" show-overflow-tooltip>
          <template #default="{ row }"><span class="hrp-mono">{{ row.path }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="120" show-overflow-tooltip />
        <el-table-column label="模块" width="110">
          <template #default="{ row }">{{ row.module || '未分类' }}</template>
        </el-table-column>
        <el-table-column label="响应体" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.has_body" size="small" type="success" effect="plain">有</el-tag>
            <span v-else class="hrp-muted">空体</span>
          </template>
        </el-table-column>
        <template #empty>
          <p class="hrp-muted">接口库还是空的：先到「接口库」建接口，mock 会自动跟上。</p>
        </template>
      </el-table>
    </el-card>

    <el-card shadow="never" class="card">
      <template #header>
        <div class="card__head">
          <span>最近命中</span>
          <div class="card__actions">
            <span class="hrp-muted">{{ hitsNote }}</span>
            <el-button size="small" @click="loadHits">刷新</el-button>
          </div>
        </div>
      </template>
      <el-table :data="hits" border stripe size="small">
        <el-table-column prop="at" label="时间" width="170" />
        <el-table-column prop="method" label="方法" width="80" />
        <el-table-column prop="path" label="请求路径" min-width="220" show-overflow-tooltip />
        <el-table-column label="命中接口" width="150">
          <template #default="{ row }">
            <span v-if="row.api_code" class="hrp-mono">{{ row.api_code }}</span>
            <span v-else class="hrp-muted">未命中</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="statusType(row.status)" effect="plain">{{ row.status }}</el-tag>
          </template>
        </el-table-column>
        <template #empty>
          <p class="hrp-muted">还没有命中记录。开启 Mock 后用 curl 或前端请求一次试试。</p>
        </template>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.page__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
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
.card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.card__actions {
  display: flex;
  align-items: center;
  gap: 10px;
}
.base {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
  flex-wrap: wrap;
}
.base__url,
.base__curl {
  padding: 4px 8px;
  background: var(--el-fill-color-light);
  border-radius: 4px;
  font-size: 12px;
  word-break: break-all;
}
.tip {
  margin-top: 10px;
}
</style>
