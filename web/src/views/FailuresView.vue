<script setup lang="ts">
/**
 * 失败聚类（F13）。
 *
 * 读法：一簇 = 一个疑似根因。计数最多的簇就是最该先修的。
 * 分类优先级由服务端决定（断言 → HTTP 状态 → 步骤错误 → 归因），
 * 前端只负责诚实展示，不自己再推一套规则。
 */
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { failureApi, type FailureCluster, type FailureClusterResult } from '@/api'
import { useProjectStore } from '@/stores/project'
import { attrTagType } from '@/utils/dict'
import { notifyError } from '@/utils/error'

const route = useRoute()
const router = useRouter()
const projects = useProjectStore()

const loading = ref(false)
const result = ref<FailureClusterResult | null>(null)
const opened = ref<string[]>([])

const q = reactive({
  run_id: route.query.run_id ? Number(route.query.run_id) : 0,
  days: 30,
})

const maxCount = computed(() => Math.max(...(result.value?.clusters.map((c) => c.count) ?? [0]), 1))

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    result.value = await failureApi.getFailureClusters(projects.currentId, {
      run_id: q.run_id || undefined,
      days: q.days,
    })
    opened.value = result.value.clusters.slice(0, 3).map((c) => c.key)
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

function share(c: FailureCluster): string {
  return `${Math.round((c.count / maxCount.value) * 100)}%`
}

function openCase(row: unknown) {
  const item = row as { run_id: number }
  router.push(`/runs/${item.run_id}`)
}

onMounted(load)
watch(() => projects.currentId, load)
</script>

<template>
  <div class="page" v-loading="loading">
    <div class="page__head">
      <div>
        <h2 class="page__title">
          失败聚类
          <el-tag v-if="q.run_id" size="small" type="warning" effect="plain" class="run-tag">
            只看执行 #{{ q.run_id }}
            <el-button link size="small" @click="clearRun">清除</el-button>
          </el-tag>
        </h2>
        <p class="hrp-muted page__desc">
          按「失败断言 → HTTP 状态 → 步骤错误 → 归因」四级特征归类，一簇通常对应一个根因。
          计数最多的簇最该先修。
        </p>
      </div>
      <div class="head__actions">
        <span class="hrp-muted">最近</span>
        <el-input-number v-model="q.days" :min="1" :max="365" controls-position="right" style="width: 110px" />
        <span class="hrp-muted">天</span>
        <el-button type="primary" :loading="loading" @click="search">分析</el-button>
      </div>
    </div>

    <template v-if="result">
      <div class="summary">
        <el-tag effect="plain">失败用例 {{ result.total_failed_cases }}</el-tag>
        <el-tag type="danger" effect="plain">聚成 {{ result.clusters.length }} 簇</el-tag>
        <span class="hrp-muted">生成于 {{ result.generated_at }}</span>
      </div>

      <el-empty v-if="!result.clusters.length" description="这段时间没有失败用例" />

      <el-collapse v-model="opened" class="clusters">
        <el-collapse-item v-for="c in result.clusters" :key="c.key" :name="c.key">
          <template #title>
            <div class="cluster__title">
              <el-tag size="small" type="danger" effect="dark" class="cluster__count">{{ c.count }}</el-tag>
              <span class="cluster__name">{{ c.title }}</span>
              <el-tag size="small" :type="attrTagType(c.attribution)" effect="plain">
                {{ c.attribution }}
              </el-tag>
              <span class="cluster__share hrp-muted">占比 {{ share(c) }}</span>
            </div>
          </template>

          <el-alert type="info" :closable="false" show-icon class="cluster__hint" :title="c.hint" />

          <el-table :data="c.cases" size="small" border>
            <el-table-column label="用例" width="190">
              <template #default="{ row }"><span class="hrp-mono">{{ row.case_code }}</span></template>
            </el-table-column>
            <el-table-column prop="name" label="名称" min-width="140" show-overflow-tooltip />
            <el-table-column prop="detail" label="典型表现" min-width="260" show-overflow-tooltip />
            <el-table-column label="执行" width="90">
              <template #default="{ row }">
                <el-button link type="primary" @click="openCase(row)">
                  #{{ row.run_id }}
                </el-button>
              </template>
            </el-table-column>
          </el-table>
          <p v-if="c.truncated" class="hrp-muted more">该簇超过 50 条，列表已截断（计数为真实总数）。</p>
        </el-collapse-item>
      </el-collapse>
    </template>
  </div>
</template>

<style scoped>
.page__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
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
  max-width: 760px;
}
.head__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}
.summary {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
  font-size: 12px;
}
.cluster__title {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding-right: 12px;
}
.cluster__count {
  min-width: 34px;
  text-align: center;
}
.cluster__name {
  font-weight: 600;
}
.cluster__share {
  margin-left: auto;
  font-size: 12px;
}
.cluster__hint {
  margin-bottom: 8px;
}
.more {
  font-size: 12px;
  margin: 6px 0 0;
}
</style>
