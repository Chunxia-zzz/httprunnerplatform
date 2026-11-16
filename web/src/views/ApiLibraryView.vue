<script setup lang="ts">
/**
 * 接口定义库（F1）：项目 → 服务（模块）→ 接口。
 *
 * 两条界面约定：
 *   1. 服务来自接口的 module 字段聚合（后端 GROUP BY），不单独维护服务表；
 *   2. 「被引用」列是派生值：>0 时删除会被后端拒绝（40003），
 *      这里先给出提示，避免用户点了删除才看到错误。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessageBox, type FormInstance } from 'element-plus'

import { apiDefApi, type APIDef, type APIGroup, type APIReq, type EnvMap } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError, notifyOk } from '@/utils/error'

const projects = useProjectStore()

const loading = ref(false)
const groups = ref<APIGroup[]>([])
const activeModule = ref<string>('') // 空 = 全部
const rows = ref<APIDef[]>([])

const filteredRows = computed(() => {
  if (!activeModule.value) return rows.value
  return rows.value.filter((r) => (r.module || '未分类') === activeModule.value)
})

const dialogVisible = ref(false)
const editingId = ref<number | null>(null)
const formRef = ref<FormInstance>()

const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS']

interface APIForm {
  module: string
  code: string
  name: string
  method: string
  path: string
  description: string
  body_type: string
  headersText: string
  paramsText: string
  bodyText: string
}

const form = reactive<APIForm>({
  module: '',
  code: '',
  name: '',
  method: 'GET',
  path: '',
  description: '',
  body_type: 'none',
  headersText: '',
  paramsText: '',
  bodyText: '',
})

function resetForm() {
  Object.assign(form, {
    module: activeModule.value === '未分类' ? '' : activeModule.value,
    code: '',
    name: '',
    method: 'GET',
    path: '',
    description: '',
    body_type: 'none',
    headersText: '',
    paramsText: '',
    bodyText: '',
  })
}

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    const [list, tree] = await Promise.all([apiDefApi.listApis(projects.currentId), apiDefApi.apiTree(projects.currentId)])
    rows.value = list.items ?? []
    groups.value = tree
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = null
  resetForm()
  dialogVisible.value = true
}

function openEdit(row: APIDef) {
  editingId.value = row.id
  Object.assign(form, {
    module: row.module ?? '',
    code: row.code,
    name: row.name,
    method: row.method,
    path: row.path,
    description: row.description,
    body_type: row.body_type || 'none',
    headersText: row.headers ? JSON.stringify(row.headers, null, 2) : '',
    paramsText: row.params ? JSON.stringify(row.params, null, 2) : '',
    bodyText: row.body != null ? JSON.stringify(row.body, null, 2) : '',
  })
  dialogVisible.value = true
}

function parseJSONMap(text: string, label: string): EnvMap | null {
  const t = text.trim()
  if (!t) return null
  try {
    return JSON.parse(t) as EnvMap
  } catch {
    throw new Error(`${label} 不是合法的 JSON 对象`)
  }
}

async function save() {
  if (!projects.currentId) return
  try {
    const data: APIReq = {
      module: form.module.trim(),
      code: form.code.trim(),
      name: form.name.trim(),
      method: form.method,
      path: form.path.trim(),
      description: form.description.trim(),
      body_type: form.body_type,
      headers: parseJSONMap(form.headersText, '请求头'),
      params: parseJSONMap(form.paramsText, '查询参数'),
      body: form.bodyText.trim() ? JSON.parse(form.bodyText) : null,
    }
    if (editingId.value) {
      await apiDefApi.updateApi(editingId.value, data)
      notifyOk('已更新')
    } else {
      await apiDefApi.createApi(projects.currentId, data)
      notifyOk('已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    notifyError(e)
  }
}

async function remove(row: APIDef) {
  if (row.used_by > 0) {
    notifyError(
      new Error(`该接口被 ${row.used_by} 个用例步骤引用，请先到用例里解除引用再删除`),
    )
    return
  }
  try {
    await ElMessageBox.confirm(`确定删除接口「${row.name}」？`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await apiDefApi.deleteApi(row.id)
    notifyOk('已删除')
    await load()
  } catch (e) {
    notifyError(e)
  }
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page__head">
      <div>
        <h2 class="page__title">接口库</h2>
        <p class="hrp-muted page__desc">
          按服务（模块）组织接口定义；用例通过「引用接口」使用它们，改一处、全部用例生效。
        </p>
      </div>
      <el-button type="primary" @click="openCreate">新建接口</el-button>
    </div>

    <div class="lib">
      <aside class="lib__side">
        <div
          class="lib__module"
          :class="{ 'lib__module--active': activeModule === '' }"
          @click="activeModule = ''"
        >
          全部 <span class="hrp-muted">{{ rows.length }}</span>
        </div>
        <div
          v-for="g in groups"
          :key="g.module"
          class="lib__module"
          :class="{ 'lib__module--active': activeModule === g.module }"
          @click="activeModule = g.module"
        >
          {{ g.module }} <span class="hrp-muted">{{ g.count }}</span>
        </div>
        <p v-if="!groups.length" class="hrp-muted lib__empty">还没有服务分组</p>
      </aside>

      <div class="lib__main">
        <el-table v-loading="loading" :data="filteredRows" border stripe>
          <el-table-column prop="method" label="方法" width="90" />
          <el-table-column prop="path" label="路径" min-width="240" show-overflow-tooltip />
          <el-table-column prop="name" label="名称" min-width="120" show-overflow-tooltip />
          <el-table-column prop="code" label="标识" width="160" show-overflow-tooltip />
          <el-table-column label="模块" width="110">
            <template #default="{ row }">{{ row.module || '未分类' }}</template>
          </el-table-column>
          <el-table-column label="被引用" width="90">
            <template #default="{ row }">
              <el-tag v-if="row.used_by > 0" size="small" effect="plain">{{ row.used_by }} 处</el-tag>
              <span v-else class="hrp-muted">—</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="130" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openEdit(row as APIDef)">编辑</el-button>
              <el-button link type="danger" @click="remove(row as APIDef)">删除</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <p class="hrp-muted">这个项目还没有接口定义，点右上角「新建接口」开始。</p>
          </template>
        </el-table>
      </div>
    </div>

    <el-dialog
      v-model="dialogVisible"
      :title="editingId ? '编辑接口' : '新建接口'"
      width="720px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="form" label-width="96px">
        <el-form-item label="服务 / 模块">
          <el-input v-model="form.module" placeholder="如 user / order，留空为未分类" />
        </el-form-item>
        <el-form-item label="标识" required>
          <el-input v-model="form.code" placeholder="如 user_login（字母/数字/下划线/连字符）" />
        </el-form-item>
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="留空则用标识作为名称" />
        </el-form-item>
        <el-form-item label="方法" required>
          <el-select v-model="form.method" style="width: 140px">
            <el-option v-for="m in METHODS" :key="m" :label="m" :value="m" />
          </el-select>
        </el-form-item>
        <el-form-item label="路径" required>
          <el-input v-model="form.path" placeholder="如 $base_url/users 或 /users" />
        </el-form-item>
        <el-form-item label="请求头">
          <el-input v-model="form.headersText" type="textarea" :rows="2" placeholder='JSON 对象，如 {"X-Trace":"1"}，可留空' />
        </el-form-item>
        <el-form-item label="查询参数">
          <el-input v-model="form.paramsText" type="textarea" :rows="2" placeholder='JSON 对象，如 {"page":1}，可留空' />
        </el-form-item>
        <el-form-item label="请求体类型">
          <el-select v-model="form.body_type" style="width: 160px">
            <el-option label="none" value="none" />
            <el-option label="json" value="json" />
            <el-option label="form" value="form" />
            <el-option label="raw" value="raw" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.body_type !== 'none'" label="请求体">
          <el-input v-model="form.bodyText" type="textarea" :rows="5" placeholder="JSON 内容；form 类型填 JSON 对象" />
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="这个接口做什么、有什么注意事项" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" @click="save">保存</el-button>
      </template>
    </el-dialog>
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
.lib {
  display: flex;
  gap: 16px;
}
.lib__side {
  width: 180px;
  flex: none;
}
.lib__module {
  padding: 8px 12px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 13px;
  margin-bottom: 4px;
}
.lib__module:hover {
  background: var(--el-fill-color-light);
}
.lib__module--active {
  background: var(--el-color-primary-light-9);
  color: var(--el-color-primary);
}
.lib__empty {
  font-size: 12px;
  padding: 8px 12px;
}
.lib__main {
  flex: 1;
  min-width: 0;
}
</style>
