<script setup lang="ts">
/**
 * 环境管理。
 *
 * 环境是唯一会被渲染成工作区 `.env` 文件的实体（base_url 自 hrp v4.1 起
 * 从用例 config 移到了 .env），所以这里的字段语义直接决定用例能不能跑通。
 *
 * 两个必须让用户看见的事实：
 *   1. 非管理员读到的是 `***` 掩码值，原样保存会保留原值（不会把 token 冲掉）；
 *   2. 项目的第一个环境会自动成为默认环境 —— 没有默认环境时执行会退回
 *      引擎内置 base_url，那种失败很难被联想到"是环境没设默认"。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessageBox, type FormInstance } from 'element-plus'

import { environmentApi, MASKED_VALUE, type Environment, type EnvMap, type EnvironmentReq } from '@/api'
import KeyValueEditor from '@/components/KeyValueEditor.vue'
import { useProjectStore } from '@/stores/project'
import { mapToRows, rowsToMap, type KVRow } from '@/utils/format'
import { notifyError, notifyOk } from '@/utils/error'

const projects = useProjectStore()

const loading = ref(false)
const list = ref<Environment[]>([])

const dialogVisible = ref(false)
const submitting = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()

const form = ref<{
  name: string
  base_url: string
  verify_ssl: boolean
  is_default: boolean
  environs: KVRow[]
  global_headers: KVRow[]
}>({
  name: '',
  base_url: '',
  verify_ssl: false,
  is_default: false,
  environs: [],
  global_headers: [],
})

const isEdit = computed(() => editingId.value > 0)
const projectId = computed(() => projects.currentId)

/** 界面上出现掩码值时给个提示，避免用户以为值丢了。 */
const hasMasked = computed(() =>
  [...form.value.environs, ...form.value.global_headers].some((r) => r.value === MASKED_VALUE),
)

async function load() {
  if (!projectId.value) {
    list.value = []
    return
  }
  loading.value = true
  try {
    const data = await environmentApi.listEnvironments(projectId.value)
    list.value = data.list
  } catch (e) {
    notifyError(e, '加载环境列表失败')
  } finally {
    loading.value = false
  }
}

function reset() {
  editingId.value = 0
  form.value = {
    name: '',
    base_url: '',
    verify_ssl: false,
    is_default: list.value.length === 0,
    environs: [],
    global_headers: [],
  }
}

function openCreate() {
  reset()
  dialogVisible.value = true
}

/**
 * el-table 的作用域插槽把 row 擦成 `DefaultRow`，与具体实体类型无结构关系。
 * 在入口处收回真实类型，避免把函数签名整体放宽成 any。
 */
function asEnv(row: unknown): Environment {
  return row as Environment
}

function openEdit(row: unknown) {
  const e = asEnv(row)
  editingId.value = e.id
  form.value = {
    name: e.name,
    base_url: e.base_url,
    verify_ssl: e.verify_ssl,
    is_default: e.is_default,
    environs: mapToRows(e.environs),
    global_headers: mapToRows(e.global_headers),
  }
  dialogVisible.value = true
}

function toPayload(): EnvironmentReq {
  return {
    name: form.value.name.trim(),
    base_url: form.value.base_url.trim(),
    environs: rowsToMap(form.value.environs) as EnvMap | null,
    global_headers: rowsToMap(form.value.global_headers) as EnvMap | null,
    verify_ssl: form.value.verify_ssl,
    is_default: form.value.is_default,
  }
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  const payload = toPayload()
  submitting.value = true
  try {
    if (isEdit.value) {
      await environmentApi.updateEnvironment(editingId.value, payload)
      notifyOk('环境已保存')
    } else {
      await environmentApi.createEnvironment(projectId.value, payload)
      notifyOk('环境已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    notifyError(e, isEdit.value ? '保存环境失败' : '创建环境失败')
  } finally {
    submitting.value = false
  }
}

async function remove(row: unknown) {
  const e = asEnv(row)
  try {
    await ElMessageBox.confirm(
      `确认删除环境「${e.name}」？若它被测试计划引用，后端会拒绝删除。`,
      '删除环境',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await environmentApi.deleteEnvironment(e.id)
    notifyOk('环境已删除')
    await load()
  } catch (err) {
    notifyError(err, '删除环境失败')
  }
}

function envCount(m: EnvMap | null): number {
  return m ? Object.keys(m).length : 0
}

onMounted(load)
watch(projectId, () => {
  void load()
})
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">环境管理</h2>
        <p class="hrp-page__sub">
          环境渲染为工作区根目录的 <span class="hrp-mono">.env</span>；用例里的
          <span class="hrp-mono">$base_url</span> 等变量都来自这里。
        </p>
      </div>
      <el-button type="primary" :disabled="!projectId" @click="openCreate">
        <el-icon><Plus /></el-icon>新建环境
      </el-button>
    </div>

    <el-empty v-if="!projectId" description="请先在顶栏选择项目（还没有项目就去项目管理页新建）" />

    <el-card v-else shadow="never" class="hrp-card">
      <div class="hrp-toolbar">
        <el-button :loading="loading" @click="load">
          <el-icon><Refresh /></el-icon>刷新
        </el-button>
        <span class="hrp-muted">共 {{ list.length }} 个环境</span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe empty-text="该项目还没有环境">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="name" label="名称" width="150">
          <template #default="{ row }">
            {{ row.name }}
            <el-tag v-if="row.is_default" size="small" type="success" effect="plain">默认</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="base_url" label="base_url" min-width="220" show-overflow-tooltip>
          <template #default="{ row }">
            <span :class="row.base_url ? 'hrp-mono' : 'hrp-muted'">{{ row.base_url || '未设置' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="变量" width="80">
          <template #default="{ row }">{{ envCount(row.environs) }}</template>
        </el-table-column>
        <el-table-column label="全局请求头" width="100">
          <template #default="{ row }">{{ envCount(row.global_headers) }}</template>
        </el-table-column>
        <el-table-column label="校验 SSL" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.verify_ssl ? 'success' : 'info'" effect="plain">
              {{ row.verify_ssl ? '开启' : '关闭' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="140" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑环境' : '新建环境'" width="720px" destroy-on-close>
      <el-form ref="formRef" :model="form" label-width="100px">
        <el-form-item
          label="名称"
          prop="name"
          :rules="[{ required: true, message: '请输入环境名称', trigger: 'blur' }]"
        >
          <el-input v-model="form.name" placeholder="例如 local / staging" style="width: 260px" />
          <span class="hrp-muted tip">同一项目内不可重名</span>
        </el-form-item>

        <el-form-item label="base_url">
          <el-input v-model="form.base_url" placeholder="http://127.0.0.1:8899" class="hrp-mono" />
          <div class="hrp-muted tip">用例里写相对路径（如 /get）时，与它拼接成最终 URL</div>
        </el-form-item>

        <el-form-item label="环境变量">
          <KeyValueEditor v-model="form.environs" key-placeholder="变量名，如 api_key" />
        </el-form-item>

        <el-form-item label="全局请求头">
          <KeyValueEditor v-model="form.global_headers" key-placeholder="Header 名，如 X-From" />
        </el-form-item>

        <el-form-item label="选项">
          <div class="options">
            <el-switch v-model="form.verify_ssl" active-text="校验 SSL 证书" />
            <el-switch
              v-model="form.is_default"
              active-text="设为默认环境"
              :disabled="isEdit && form.is_default"
            />
          </div>
          <div class="hrp-muted tip">
            设为默认后，同项目其他环境的默认标记会被自动取消。项目的第一个环境会自动成为默认。
          </div>
        </el-form-item>

        <el-alert v-if="hasMasked" type="info" show-icon :closable="false">
          变量值里的 <code>***</code> 是服务端掩码（当前账号无权查看原文）。保持不动即可保留原值，
          填写新值则会覆盖它。
        </el-alert>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.tip {
  font-size: 12px;
  line-height: 1.6;
  margin-left: 8px;
}

.options {
  display: flex;
  gap: 28px;
  align-items: center;
}
</style>
