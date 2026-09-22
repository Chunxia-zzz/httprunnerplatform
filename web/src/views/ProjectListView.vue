<script setup lang="ts">
/**
 * 项目管理。
 *
 * 项目是平台的顶层上下文：`code` 同时是工作区目录名，创建项目会
 * 同步在 `{workspace.root}/workspaces/{code}/` 下建出 api/testcases/suites/data。
 * 因此 `code` 创建后不可修改（后端会直接拒绝并返回 40000）。
 */
import { onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessageBox, type FormInstance, type FormRules } from 'element-plus'

import { projectApi, type Project, type ProjectReq } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { useProjectStore } from '@/stores/project'
import { formatTime } from '@/utils/format'
import { notifyError, notifyOk } from '@/utils/error'

const router = useRouter()
const projects = useProjectStore()
const auth = useAuthStore()

const keyword = ref('')
const loading = ref(false)

const dialogVisible = ref(false)
const submitting = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()

const form = reactive<ProjectReq>({
  code: '',
  name: '',
  description: '',
  hrp_version: 'v4.3.6',
})

const isEdit = ref(false)

const rules: FormRules = {
  code: [
    { required: true, message: '请输入项目标识', trigger: 'blur' },
    {
      pattern: /^[a-zA-Z0-9_-]{1,64}$/,
      message: '只能包含字母、数字、下划线、连字符，长度 1–64',
      trigger: 'blur',
    },
  ],
  name: [{ required: true, message: '请输入项目名称', trigger: 'blur' }],
}

async function load() {
  loading.value = true
  try {
    await projects.fetchList(keyword.value)
  } catch (e) {
    notifyError(e, '加载项目列表失败')
  } finally {
    loading.value = false
  }
}

function openCreate() {
  isEdit.value = false
  editingId.value = 0
  form.code = ''
  form.name = ''
  form.description = ''
  form.hrp_version = 'v4.3.6'
  dialogVisible.value = true
}

/**
 * el-table 的作用域插槽把 row 擦成 `DefaultRow`（一个索引签名对象），
 * 它和具体实体类型没有结构关系，直接传给强类型函数会被 TS 拒绝。
 * 这里统一在入口处收回真实类型，而不是把函数签名放宽成 any ——
 * 那样等于把类型检查从这些函数里整体拿掉。
 */
function asProject(row: unknown): Project {
  return row as Project
}

function openEdit(row: unknown) {
  const p = asProject(row)
  isEdit.value = true
  editingId.value = p.id
  form.code = p.code
  form.name = p.name
  form.description = p.description
  form.hrp_version = p.hrp_version
  dialogVisible.value = true
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    if (isEdit.value) {
      // code 原样回传（后端要求一致，不一致会拒）
      await projectApi.updateProject(editingId.value, { ...form })
      notifyOk('项目已更新')
    } else {
      const created = await projectApi.createProject({ ...form })
      notifyOk('项目已创建，工作区目录已同步生成')
      projects.setCurrent(created.id)
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    notifyError(e, isEdit.value ? '更新项目失败' : '创建项目失败')
  } finally {
    submitting.value = false
  }
}

async function remove(row: unknown) {
  const p = asProject(row)
  try {
    await ElMessageBox.confirm(
      `确认删除项目「${p.name}」？` +
        '这是软删除，可恢复；但工作区目录不会被清理（里面可能有你手工维护的数据文件）。',
      '删除项目',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }

  try {
    await projectApi.deleteProject(p.id)
    notifyOk('项目已删除')
    if (projects.currentId === p.id) projects.setCurrent(0)
    await load()
  } catch (e) {
    // 40003：项目下还有执行中的任务
    notifyError(e, '删除项目失败')
  }
}

function enter(row: unknown) {
  const p = asProject(row)
  projects.setCurrent(p.id)
  void router.push({ name: 'cases' })
}

onMounted(load)
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">项目管理</h2>
        <p class="hrp-page__sub">
          项目 code 同时是工作区目录名（<span class="hrp-mono">workspaces/{code}/</span>），创建后不可修改。
        </p>
      </div>
      <el-button type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>新建项目
      </el-button>
    </div>

    <el-card shadow="never" class="hrp-card">
      <div class="hrp-toolbar">
        <el-input
          v-model="keyword"
          placeholder="按标识或名称搜索"
          clearable
          style="width: 240px"
          @keyup.enter="load"
          @clear="load"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-button :loading="loading" @click="load">查询</el-button>
        <span class="hrp-muted">共 {{ projects.total }} 个项目</span>
      </div>

      <el-table v-loading="loading" :data="projects.list" border stripe empty-text="还没有项目，先新建一个">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="code" label="标识" width="150">
          <template #default="{ row }">
            <span class="hrp-mono">{{ row.code }}</span>
            <el-tag v-if="projects.currentId === row.id" size="small" type="success" effect="plain" class="ml6">
              当前
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="140" show-overflow-tooltip />
        <el-table-column prop="description" label="描述" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">
            <span :class="{ 'hrp-muted': !row.description }">{{ row.description || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="hrp_version" label="引擎版本" width="110" />
        <el-table-column prop="workspace_path" label="工作区" min-width="200" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="hrp-mono hrp-muted">{{ row.workspace_path || '—' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="200" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="enter(row)">进入</el-button>
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <!--
              删除项目会连带环境与用例，破坏性最大，后端已收紧为管理员专属。
              非管理员这里直接给出不可点的按钮 + 原因，而不是让他点下去收 40300。
            -->
            <el-tooltip
              :disabled="auth.isAdmin"
              content="删除项目会连带其环境与用例，仅管理员可操作"
              placement="top"
            >
              <span>
                <el-button link type="danger" :disabled="!auth.isAdmin" @click="remove(row)">删除</el-button>
              </span>
            </el-tooltip>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog
      v-model="dialogVisible"
      :title="isEdit ? '编辑项目' : '新建项目'"
      width="520px"
      destroy-on-close
    >
      <el-form ref="formRef" :model="form" :rules="rules" label-width="90px">
        <el-form-item label="标识" prop="code">
          <el-input
            v-model="form.code"
            :disabled="isEdit"
            placeholder="例如 demo"
            class="hrp-mono"
          />
          <div v-if="isEdit" class="hrp-muted form-tip">
            code 是工作区目录名，改名需要迁移整个目录，M1 不支持。
          </div>
        </el-form-item>
        <el-form-item label="名称" prop="name">
          <el-input v-model="form.name" placeholder="例如 演示项目" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="可选" />
        </el-form-item>
        <el-form-item label="引擎版本">
          <el-input v-model="form.hrp_version" placeholder="v4.3.6" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.form-tip {
  font-size: 12px;
  line-height: 1.6;
  margin-top: 4px;
}

.ml6 {
  margin-left: 6px;
}
</style>
