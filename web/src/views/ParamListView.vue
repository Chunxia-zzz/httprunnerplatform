<script setup lang="ts">
/**
 * 参数化数据集管理（M3 · ③-b）。
 *
 * 两种数据源（与后端 ParamService 对齐）：
 *   - list：内联 [{...}, ...] 列表，编译时直接内联进 YAML；
 *   - csv ：项目工作区 data/{name}.csv，编译时用 ${P(data/xxx.csv)} 引用。
 *
 * 一个必须让用户看见的事实：CSV 的 limit 是平台在**编译期裁剪**的
 * （引擎 ${P()} 语法里表达不了 limit，实测 A22）——所以这里展示的是
 * limit_effective（实际会迭代几次），而不是把 limit 当成引擎参数。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessageBox, type FormInstance } from 'element-plus'

import { paramApi, type ParamDataset, type ParamDatasetReq, type ParamSource } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError, notifyOk } from '@/utils/error'

const projects = useProjectStore()

const loading = ref(false)
const list = ref<ParamDataset[]>([])

const dialogVisible = ref(false)
const submitting = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()

const form = ref<{
  name: string
  source: ParamSource
  inlineText: string
  csvName: string
  csvText: string
  limit: number
}>({
  name: '',
  source: 'list',
  inlineText: '',
  csvName: '',
  csvText: '',
  limit: 0,
})

const isEdit = computed(() => editingId.value > 0)
const projectId = computed(() => projects.currentId)

async function load() {
  if (!projectId.value) {
    list.value = []
    return
  }
  loading.value = true
  try {
    list.value = await paramApi.listDatasets(projectId.value)
  } catch (e) {
    notifyError(e, '加载数据集列表失败')
  } finally {
    loading.value = false
  }
}

function reset() {
  editingId.value = 0
  form.value = { name: '', source: 'list', inlineText: '', csvName: '', csvText: '', limit: 0 }
}

function openCreate() {
  reset()
  dialogVisible.value = true
}

function asDs(row: unknown): ParamDataset {
  return row as ParamDataset
}

/** 编辑：list 数据集回填 inline 文本；csv 数据集回填文件名 + 读回 CSV 文本。 */
async function openEdit(row: unknown) {
  const d = asDs(row)
  editingId.value = d.id
  form.value = {
    name: d.name,
    source: d.source === 'csv' ? 'csv' : 'list',
    inlineText: '',
    csvName: '',
    csvText: '',
    limit: d.limit,
  }
  if (form.value.source === 'list') {
    form.value.inlineText = prettyInline(d.inline)
  } else {
    form.value.csvName = csvBase(d.csv_path)
    try {
      form.value.csvText = await paramApi.datasetCsvText(d.id)
    } catch {
      // 读回失败不阻塞编辑（文件可能被手动清理），让用户重新传内容
      form.value.csvText = ''
    }
  }
  dialogVisible.value = true
}

function prettyInline(inline: unknown): string {
  if (inline == null) return ''
  try {
    return JSON.stringify(inline, null, 2)
  } catch {
    return ''
  }
}

/** data/users.csv → users.csv */
function csvBase(p: string): string {
  const parts = (p || '').split(/[\\/]/)
  return parts[parts.length - 1] || ''
}

function toPayload(): ParamDatasetReq {
  const req: ParamDatasetReq = {
    name: form.value.name.trim(),
    source: form.value.source,
    limit: form.value.limit,
  }
  if (form.value.source === 'list') {
    req.inline = JSON.parse(form.value.inlineText)
  } else {
    req.csv_name = form.value.csvName.trim() || undefined
    req.csv_text = form.value.csvText || undefined
  }
  return req
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  // 前端先行校验：list 的 JSON 文本必须合法；csv 至少给文件名或内容。
  if (form.value.source === 'list') {
    const t = form.value.inlineText.trim()
    if (!t) {
      notifyError(new Error('请输入内联数据（[{...}, ...] 数组）'), '内联数据不能为空')
      return
    }
    try {
      const parsed = JSON.parse(t)
      if (!Array.isArray(parsed) || parsed.length === 0) {
        notifyError(new Error('内联数据必须是包含至少一个对象的数组'), '内联数据格式错误')
        return
      }
    } catch (e) {
      notifyError(e, '内联数据不是合法 JSON')
      return
    }
  } else {
    if (!form.value.csvName.trim() && !form.value.csvText.trim()) {
      notifyError(new Error('请填写文件名或粘贴 CSV 内容'), 'CSV 内容不能为空')
      return
    }
  }

  const payload = toPayload()
  submitting.value = true
  try {
    if (isEdit.value) {
      await paramApi.updateDataset(editingId.value, payload)
      notifyOk('数据集已保存')
    } else {
      await paramApi.createDataset(projectId.value, payload)
      notifyOk('数据集已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    notifyError(e, isEdit.value ? '保存数据集失败' : '创建数据集失败')
  } finally {
    submitting.value = false
  }
}

async function remove(row: unknown) {
  const d = asDs(row)
  try {
    await ElMessageBox.confirm(
      `确认删除数据集「${d.name}」？引用它的用例会在预览/校验时报「数据集不存在」。`,
      '删除数据集',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await paramApi.deleteDataset(d.id)
    notifyOk('数据集已删除')
    await load()
  } catch (err) {
    notifyError(err, '删除数据集失败')
  }
}

function sourceTag(source: string) {
  return source === 'csv' ? 'CSV' : 'List'
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
        <h2 class="hrp-page__title">参数化数据集</h2>
        <p class="hrp-page__sub">
          为用例提供数据驱动迭代。在用例配置里勾选数据集后，用例会按数据行数迭代执行。
        </p>
      </div>
      <el-button type="primary" :disabled="!projectId" @click="openCreate">
        <el-icon><Plus /></el-icon>新建数据集
      </el-button>
    </div>

    <el-empty v-if="!projectId" description="请先在顶栏选择项目（还没有项目就去项目管理页新建）" />

    <el-card v-else shadow="never" class="hrp-card">
      <div class="hrp-toolbar">
        <el-button :loading="loading" @click="load">
          <el-icon><Refresh /></el-icon>刷新
        </el-button>
        <span class="hrp-muted">共 {{ list.length }} 个数据集</span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe empty-text="该项目还没有数据集">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="name" label="名称" min-width="160" show-overflow-tooltip>
          <template #default="{ row }">
            <span class="hrp-mono">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="来源" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.source === 'csv' ? 'warning' : 'primary'" effect="plain">
              {{ sourceTag(row.source) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="列" min-width="140">
          <template #default="{ row }">
            <template v-if="row.columns && row.columns.length">
              <el-tag
                v-for="c in row.columns.slice(0, 4)"
                :key="c"
                size="small"
                type="info"
                effect="plain"
                class="col-tag"
              >
                {{ c }}
              </el-tag>
              <span v-if="row.columns.length > 4" class="hrp-muted">+{{ row.columns.length - 4 }}</span>
            </template>
            <span v-else class="hrp-muted">—</span>
          </template>
        </el-table-column>
        <el-table-column prop="row_count" label="数据行数" width="90" />
        <el-table-column prop="limit" label="limit" width="90">
          <template #default="{ row }">
            <span :class="row.limit > 0 ? '' : 'hrp-muted'">{{ row.limit > 0 ? row.limit : '全部' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="实际迭代" width="90">
          <template #default="{ row }">
            <el-tag size="small" type="success" effect="plain">{{ row.limit_effective }} 次</el-tag>
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

    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑数据集' : '新建数据集'" width="760px" destroy-on-close>
      <el-form ref="formRef" :model="form" label-width="100px">
        <el-form-item
          label="名称"
          prop="name"
          :rules="[
            { required: true, message: '请输入数据集名称', trigger: 'blur' },
            { max: 64, message: '名称最多 64 字符', trigger: 'blur' },
          ]"
        >
          <el-input v-model="form.name" placeholder="例如 users / login_cred" style="width: 280px" />
          <div class="hrp-muted tip">
            这是用例配置里引用的名字，同一项目内不可重名。改了名字会让引用它的用例报「数据集不存在」。
          </div>
        </el-form-item>

        <el-form-item label="数据来源">
          <el-radio-group v-model="form.source" :disabled="isEdit">
            <el-radio-button value="list">内联列表（List）</el-radio-button>
            <el-radio-button value="csv">CSV 文件</el-radio-button>
          </el-radio-group>
          <div v-if="isEdit" class="hrp-muted tip">数据来源创建后不可切换（要换就新建一个数据集）。</div>
        </el-form-item>

        <el-form-item v-if="form.source === 'list'" label="内联数据">
          <el-input
            v-model="form.inlineText"
            type="textarea"
            :rows="8"
            class="hrp-mono"
            placeholder='[{"username": "alice", "password": "pw1"}, {"username": "bob", "password": "pw2"}]'
          />
          <div class="hrp-muted tip">
            一个对象数组，每一行是一次迭代。字段名会按字典序排序后作为参数名（编译产物稳定）。
          </div>
        </el-form-item>

        <template v-else>
          <el-form-item label="文件名">
            <el-input v-model="form.csvName" placeholder="users.csv" class="hrp-mono" style="width: 280px" />
            <div class="hrp-muted tip">纯文件名（不含路径），必须以 .csv 结尾。新建时可留空、只贴内容。</div>
          </el-form-item>
          <el-form-item label="CSV 内容">
            <el-input
              v-model="form.csvText"
              type="textarea"
              :rows="8"
              class="hrp-mono"
              placeholder="username,password&#10;alice,pw1&#10;bob,pw2"
            />
            <div class="hrp-muted tip">第一行是表头，之后每行是一次迭代。列数必须一致。</div>
          </el-form-item>
        </template>

        <el-form-item label="迭代上限">
          <el-input-number v-model="form.limit" :min="0" :max="100000" style="width: 200px" />
          <div class="hrp-muted tip">
            0 = 全部迭代。填 N 表示只跑前 N 行（平台在编译期裁剪，引擎无 limit 开关）。
          </div>
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
.tip {
  font-size: 12px;
  line-height: 1.6;
  margin-left: 8px;
}

.col-tag {
  margin-right: 4px;
}
</style>
