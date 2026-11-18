<script setup lang="ts">
/**
 * 变更历史（F8）：谁、什么时候、对什么做了什么。
 *
 * 只读页面：审计数据没有编辑/删除入口，这是有意的。
 */
import { onMounted, reactive, ref } from 'vue'

import { changeApi, type ChangeLog } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError } from '@/utils/error'

const projects = useProjectStore()

const loading = ref(false)
const rows = ref<ChangeLog[]>([])
const total = ref(0)

const q = reactive({
  entity: '',
  action: '',
  page: 1,
  page_size: 20,
})

const ENTITY_LABEL: Record<string, string> = {
  case: '用例',
  api: '接口',
  environment: '环境',
  suite: '用例集',
  plan: '计划',
  dataset: '参数集',
}

const ACTION_LABEL: Record<string, string> = {
  create: '新建',
  update: '修改',
  delete: '删除',
}

const ACTION_TYPE: Record<string, 'success' | 'warning' | 'danger'> = {
  create: 'success',
  update: 'warning',
  delete: 'danger',
}

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    const data = await changeApi.listChanges(projects.currentId, { ...q })
    rows.value = data.list ?? []
    total.value = data.total
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function search() {
  q.page = 1
  load()
}

function onPage(p: number) {
  q.page = p
  load()
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page__head">
      <div>
        <h2 class="page__title">变更历史</h2>
        <p class="hrp-muted page__desc">
          关键实体（用例/接口/环境/用例集/计划/参数集）的新增、修改与删除都会留痕；只记录，不可编辑或删除。
        </p>
      </div>
    </div>

    <div class="filters">
      <el-select v-model="q.entity" placeholder="全部类型" clearable style="width: 140px" @change="search">
        <el-option v-for="(label, key) in ENTITY_LABEL" :key="key" :label="label" :value="key" />
      </el-select>
      <el-select v-model="q.action" placeholder="全部动作" clearable style="width: 120px" @change="search">
        <el-option v-for="(label, key) in ACTION_LABEL" :key="key" :label="label" :value="key" />
      </el-select>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-table v-loading="loading" :data="rows" border stripe>
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ row.created_at?.slice(0, 19).replace('T', ' ') }}</template>
      </el-table-column>
      <el-table-column label="操作人" width="130">
        <template #default="{ row }">{{ row.username || '—' }}</template>
      </el-table-column>
      <el-table-column label="动作" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="ACTION_TYPE[row.action] || 'info'" effect="plain">
            {{ ACTION_LABEL[row.action] || row.action }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="类型" width="90">
        <template #default="{ row }">{{ ENTITY_LABEL[row.entity] || row.entity }}</template>
      </el-table-column>
      <el-table-column prop="entity_key" label="标识" width="160" show-overflow-tooltip>
        <template #default="{ row }"><span class="hrp-mono">{{ row.entity_key }}</span></template>
      </el-table-column>
      <el-table-column prop="summary" label="摘要" min-width="320" show-overflow-tooltip />
      <template #empty>
        <p class="hrp-muted">还没有变更记录。新建/修改一次用例或接口就会出现在这里。</p>
      </template>
    </el-table>

    <div class="pager">
      <el-pagination
        layout="total, prev, pager, next"
        :total="total"
        :page-size="q.page_size"
        :current-page="q.page"
        @current-change="onPage"
      />
    </div>
  </div>
</template>

<style scoped>
.page__head {
  margin-bottom: 14px;
}
.page__title {
  margin: 0;
  font-size: 18px;
}
.page__desc {
  margin: 4px 0 0;
  font-size: 12px;
}
.filters {
  display: flex;
  gap: 8px;
  margin-bottom: 12px;
}
.pager {
  display: flex;
  justify-content: flex-end;
  margin-top: 12px;
}
</style>
