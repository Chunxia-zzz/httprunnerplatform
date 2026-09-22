<script setup lang="ts">
/**
 * 执行记录列表。
 *
 * 两个「必须一眼看到」的字段：
 *   - `attribution`：失败归因计数。这是平台相对"看日志猜原因"的核心增量，
 *     所以它出现在列表而不是藏在详情里。
 *   - `count_mismatch`：用例数对账不一致。它意味着"预期跑 1 条、实际跑了 0 条"，
 *     哪怕退出码是 0，也会被强制标成 error。必须显式提示，否则用户会把它
 *     当成一次普通的红。
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { runApi, isTerminalRun, type RunListQuery, type RunRecord } from '@/api'
import { useProjectStore } from '@/stores/project'
import { attrTagType, runStatusMeta } from '@/utils/dict'
import { formatDuration, formatTime } from '@/utils/format'
import { notifyError } from '@/utils/error'

const router = useRouter()
const projects = useProjectStore()

const loading = ref(false)
const list = ref<RunRecord[]>([])
const total = ref(0)

const query = reactive<RunListQuery>({
  page: 1,
  page_size: 20,
  status: '',
})

const projectId = computed(() => projects.currentId)

/** 有在跑的执行时自动刷新，让列表不用手点也能看到结果落下来。 */
const hasActive = computed(() => list.value.some((r) => !isTerminalRun(r.status)))
let timer: number | undefined

async function load() {
  loading.value = true
  try {
    const data = await runApi.listRuns({
      ...query,
      project_id: projectId.value || undefined,
    })
    list.value = data.list
    total.value = data.total
  } catch (e) {
    notifyError(e, '加载执行记录失败')
  } finally {
    loading.value = false
  }
}

function syncTimer() {
  if (hasActive.value && timer === undefined) {
    timer = window.setInterval(() => void load(), 3000)
  } else if (!hasActive.value && timer !== undefined) {
    window.clearInterval(timer)
    timer = undefined
  }
}

/**
 * 归因计数渲染成「标签 + 数量」。
 *
 * 参数放宽成 unknown 是因为 el-table 的作用域插槽把 row 擦成了
 * `DefaultRow`，与 RunRecord 无结构关系；在函数内收回真实类型，
 * 这样类型检查仍然覆盖到取字段的部分。
 */
function attrEntries(row: unknown): Array<{ key: string; count: number }> {
  const run = row as RunRecord
  const m = run.attribution
  if (!m) return []
  return Object.entries(m).map(([key, count]) => ({ key, count: Number(count) }))
}

function goto(id: number) {
  void router.push({ name: 'run-detail', params: { id } })
}

onMounted(async () => {
  if (!projects.loaded) await projects.fetchList()
  await load()
  syncTimer()
})

watch(hasActive, syncTimer)
watch(projectId, () => {
  query.page = 1
  void load()
})

onBeforeUnmount(() => {
  if (timer !== undefined) window.clearInterval(timer)
})
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">执行记录</h2>
        <p class="hrp-page__sub">
          每次执行一个用例一个独立子进程；归因标签与用例数对账由服务端计算，页面直接展示。
        </p>
      </div>
      <el-button :loading="loading" @click="load">
        <el-icon><Refresh /></el-icon>刷新
      </el-button>
    </div>

    <el-card shadow="never" class="hrp-card">
      <div class="hrp-toolbar">
        <el-select v-model="query.status" placeholder="状态" clearable style="width: 130px" @change="load">
          <el-option label="排队中" value="queued" />
          <el-option label="执行中" value="running" />
          <el-option label="通过" value="success" />
          <el-option label="失败" value="failed" />
          <el-option label="错误" value="error" />
          <el-option label="已终止" value="canceled" />
        </el-select>
        <span class="hrp-muted">共 {{ total }} 条</span>
        <el-tag v-if="hasActive" size="small" type="primary" effect="plain">
          <el-icon class="spin"><Loading /></el-icon>有执行在跑，每 3 秒自动刷新
        </el-tag>
      </div>

      <el-table v-loading="loading" :data="list" border stripe empty-text="还没有执行记录">
        <el-table-column prop="id" label="运行 ID" width="100">
          <template #default="{ row }">
            <el-link type="primary" :underline="false" @click="goto(row.id)">{{ row.id }}</el-link>
          </template>
        </el-table-column>
        <el-table-column prop="target_name" label="用例" min-width="160" show-overflow-tooltip />
        <el-table-column label="状态" width="96">
          <template #default="{ row }">
            <el-tag size="small" :type="runStatusMeta(row.status).type" effect="dark">
              {{ runStatusMeta(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="用例数" width="120">
          <template #default="{ row }">
            <span class="hrp-mono">通过 {{ row.passed }} / 失败 {{ row.failed }} / 错误 {{ row.error }}</span>
          </template>
        </el-table-column>
        <el-table-column label="归因" min-width="150">
          <template #default="{ row }">
            <template v-if="attrEntries(row).length">
              <el-tag
                v-for="a in attrEntries(row)"
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
          </template>
        </el-table-column>
        <el-table-column label="对账" width="90">
          <template #default="{ row }">
            <el-tooltip
              v-if="row.count_mismatch"
              :content="`预期执行 ${row.expected_case_count} 条，实际执行 ${row.actual_case_count} 条。这种情况会被强制标为 error —— 退出码再好看也不代表跑到了。`"
              placement="top"
            >
              <el-tag size="small" type="danger" effect="dark">不一致</el-tag>
            </el-tooltip>
            <span v-else class="hrp-muted">一致</span>
          </template>
        </el-table-column>
        <el-table-column label="耗时" width="100">
          <template #default="{ row }">{{ formatDuration(row.duration_ms) }}</template>
        </el-table-column>
        <el-table-column label="环境" width="110">
          <template #default="{ row }">
            <span :class="{ 'hrp-muted': !row.env_name }">{{ row.env_name || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="开始时间" width="170">
          <template #default="{ row }">{{ formatTime(row.started_at, '未开始') }}</template>
        </el-table-column>
      </el-table>

      <el-pagination
        class="pager"
        layout="total, sizes, prev, pager, next"
        :total="total"
        :current-page="query.page"
        :page-size="query.page_size"
        :page-sizes="[10, 20, 50]"
        @current-change="(p: number) => { query.page = p; load() }"
        @size-change="(s: number) => { query.page_size = s; query.page = 1; load() }"
      />
    </el-card>
  </div>
</template>

<style scoped>
.attr-tag {
  margin-right: 4px;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}

.spin {
  animation: spin 1.4s linear infinite;
  vertical-align: -2px;
  margin-right: 1px;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
