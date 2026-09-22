<script setup lang="ts">
/**
 * 用例集管理。
 *
 * 用例集是**扁平**的：一组用例的有序列表，不支持"用例集里放用例集"
 * （理由见 docs/用例集与测试计划设计.md 1.1）。
 *
 * 这个页面上两件事最要紧：
 *   1. **成员顺序即执行顺序** —— 列表顺序必须是可调整的，而且要能
 *      让用户看见"这条排在第二个"；
 *   2. **跑不了的成员要标出来** —— 用例被禁用或被删时，执行会跳过它，
 *      如果列表里看不出来，用户只能对着"预期 10 条实际 8 条"去猜少了谁。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessageBox, type FormInstance } from 'element-plus'

import {
  caseApi,
  runApi,
  suiteApi,
  type CaseListItem,
  type ID,
  type Suite,
  type SuiteMember,
} from '@/api'
import { useProjectStore } from '@/stores/project'
import { caseStatusMeta, executeModeMeta, onFailureMeta } from '@/utils/dict'
import { notifyError, notifyOk } from '@/utils/error'
import { useRouter } from 'vue-router'

const projects = useProjectStore()
const router = useRouter()

const loading = ref(false)
const list = ref<Suite[]>([])
const keyword = ref('')

// 表单
const dialogVisible = ref(false)
const submitting = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()
const form = ref({ code: '', name: '', description: '', on_failure: 'continue' as 'continue' | 'abort' })

// 成员抽屉
const memberVisible = ref(false)
const memberSuiteId = ref<ID>(0)
const memberSuiteName = ref('')
const members = ref<SuiteMember[]>([])
const memberLoading = ref(false)
const pickedIds = ref<ID[]>([])
const memberSubmitting = ref(false)
const caseOptions = ref<CaseListItem[]>([])

const isEdit = computed(() => editingId.value > 0)
const projectId = computed(() => projects.currentId)

async function load() {
  if (!projectId.value) {
    list.value = []
    return
  }
  loading.value = true
  try {
    const data = await suiteApi.listSuites(projectId.value, { keyword: keyword.value })
    list.value = data.list
  } catch (e) {
    notifyError(e, '加载用例集失败')
  } finally {
    loading.value = false
  }
}

function asSuite(row: unknown): Suite {
  return row as Suite
}

function openCreate() {
  editingId.value = 0
  form.value = { code: '', name: '', description: '', on_failure: 'continue' }
  dialogVisible.value = true
}

function openEdit(row: unknown) {
  const s = asSuite(row)
  editingId.value = s.id
  form.value = {
    code: s.code,
    name: s.name,
    description: s.description,
    on_failure: s.on_failure,
  }
  dialogVisible.value = true
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    if (isEdit.value) {
      await suiteApi.updateSuite(editingId.value, {
        name: form.value.name.trim(),
        description: form.value.description.trim(),
        on_failure: form.value.on_failure,
      })
      notifyOk('用例集已保存')
    } else {
      await suiteApi.createSuite(projectId.value, {
        code: form.value.code.trim(),
        name: form.value.name.trim(),
        description: form.value.description.trim(),
        on_failure: form.value.on_failure,
      })
      notifyOk('用例集已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    notifyError(e, isEdit.value ? '保存用例集失败' : '创建用例集失败')
  } finally {
    submitting.value = false
  }
}

async function remove(row: unknown) {
  const s = asSuite(row)
  try {
    await ElMessageBox.confirm(
      `确认删除用例集「${s.name}」？若它已被测试计划引用，后端会拒绝删除（40003）。`,
      '删除用例集',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await suiteApi.deleteSuite(s.id)
    notifyOk('用例集已删除')
    await load()
  } catch (e) {
    notifyError(e, '删除用例集失败')
  }
}

// ---------------------------------------------------------------------------
// 成员
// ---------------------------------------------------------------------------

async function openMembers(row: unknown) {
  const s = asSuite(row)
  memberSuiteId.value = s.id
  memberSuiteName.value = s.name
  memberVisible.value = true
  await Promise.all([loadMembers(), loadCaseOptions()])
}

async function loadMembers() {
  memberLoading.value = true
  try {
    members.value = await suiteApi.listSuiteMembers(memberSuiteId.value)
    pickedIds.value = members.value.map((m) => m.case_id)
  } catch (e) {
    notifyError(e, '加载成员失败')
  } finally {
    memberLoading.value = false
  }
}

async function loadCaseOptions() {
  try {
    const data = await caseApi.listCases(projectId.value, { page: 1, page_size: 200 })
    caseOptions.value = data.list
  } catch (e) {
    notifyError(e, '加载用例列表失败')
  }
}

/** 已勾选的用例按勾选顺序排列 —— 这个顺序就是执行顺序。 */
const pickedMembers = computed(() =>
  pickedIds.value
    .map((id) => caseOptions.value.find((c) => c.id === id))
    .filter((c): c is CaseListItem => !!c),
)

/** 已经在成员里但跑不了的（被禁用/被删），用服务端返回的标注显示。 */
const brokenNotes = computed(() => {
  const out = new Map<ID, string>()
  for (const m of members.value) {
    if (!m.runnable && m.skip_reason) out.set(m.case_id, m.skip_reason)
  }
  return out
})

function move(idx: number, delta: number) {
  const next = idx + delta
  if (next < 0 || next >= pickedIds.value.length) return
  const arr = [...pickedIds.value]
  const tmp = arr[idx]
  arr[idx] = arr[next]!
  arr[next] = tmp!
  pickedIds.value = arr
}

function dropAt(idx: number) {
  pickedIds.value = pickedIds.value.filter((_, i) => i !== idx)
}

async function saveMembers() {
  memberSubmitting.value = true
  try {
    members.value = await suiteApi.setSuiteMembers(memberSuiteId.value, pickedIds.value)
    notifyOk('成员已保存')
    await load()
  } catch (e) {
    notifyError(e, '保存成员失败')
  } finally {
    memberSubmitting.value = false
  }
}

// ---------------------------------------------------------------------------
// 执行
// ---------------------------------------------------------------------------

async function runSuite(row: unknown) {
  const s = asSuite(row)
  try {
    const res = await runApi.startRun({
      project_id: projectId.value,
      target_type: 'suite',
      target_id: s.id,
    })
    notifyOk(`已启动执行 #${res.run_id}`)
    await router.push({ name: 'run-detail', params: { id: res.run_id } })
  } catch (e) {
    notifyError(e, '启动执行失败')
  }
}

onMounted(load)
watch(projectId, () => void load())
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">用例集管理</h2>
        <p class="hrp-page__sub">
          用例集是一组用例的<strong>有序</strong>列表；顺序就是执行顺序，每个用例独立子进程串行执行。
        </p>
      </div>
      <el-button type="primary" :disabled="!projectId" @click="openCreate">
        <el-icon><Plus /></el-icon>新建用例集
      </el-button>
    </div>

    <el-empty v-if="!projectId" description="请先在顶栏选择项目" />

    <el-card v-else shadow="never" class="hrp-card">
      <div class="hrp-toolbar">
        <el-input v-model="keyword" placeholder="按标识或名称搜索" clearable style="width: 240px" @keyup.enter="load"
          @clear="load" />
        <el-button :loading="loading" @click="load"><el-icon><Refresh /></el-icon>刷新</el-button>
        <span class="hrp-muted">共 {{ list.length }} 个用例集</span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe empty-text="该项目还没有用例集">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="code" label="标识" width="140">
          <template #default="{ row }"><span class="hrp-mono">{{ row.code }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="160" show-overflow-tooltip />
        <el-table-column label="执行模式" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="executeModeMeta(row.execute_mode).type" effect="plain">
              {{ executeModeMeta(row.execute_mode).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="遇错行为" width="110">
          <template #default="{ row }">
            <el-tag size="small" :type="onFailureMeta(row.on_failure).type" effect="plain">
              {{ onFailureMeta(row.on_failure).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="case_count" label="成员" width="80" />
        <el-table-column label="操作" width="240" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openMembers(row)">成员</el-button>
            <el-button link type="success" @click="runSuite(row)">执行</el-button>
            <el-button link @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <!-- 新建 / 编辑 -->
    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑用例集' : '新建用例集'" width="620px" destroy-on-close>
      <el-form ref="formRef" :model="form" label-width="100px">
        <el-form-item v-if="!isEdit" label="标识" prop="code" :rules="[
          { required: true, message: '请输入标识', trigger: 'blur' },
          { pattern: /^[a-zA-Z0-9_-]{1,64}$/, message: '只允许字母、数字、下划线与短横线，长度 1-64', trigger: 'blur' },
        ]">
          <el-input v-model="form.code" placeholder="例如 smoke_login" class="hrp-mono" style="width: 260px" />
          <div class="hrp-muted tip">进入执行历史与产物文件名，创建后不可修改</div>
        </el-form-item>

        <el-form-item label="名称" prop="name" :rules="[{ required: true, message: '请输入名称', trigger: 'blur' }]">
          <el-input v-model="form.name" placeholder="例如 登录冒烟" />
        </el-form-item>

        <el-form-item label="遇错行为">
          <el-radio-group v-model="form.on_failure">
            <el-radio value="continue">继续执行</el-radio>
            <el-radio value="abort">立即停止</el-radio>
          </el-radio-group>
          <div class="hrp-muted tip">
            默认「继续执行」：一条失败不影响后面，一次就能看到全部失败，不用"修一个跑一次"。
          </div>
        </el-form-item>

        <el-form-item label="备注">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>

      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 成员 -->
    <el-drawer v-model="memberVisible" :title="`成员 · ${memberSuiteName}`" size="620px">
      <el-alert type="info" show-icon :closable="false" style="margin-bottom: 12px">
        勾选顺序<strong>就是执行顺序</strong>；每个用例一个独立子进程，串行执行。
      </el-alert>

      <div class="pick">
        <el-select v-model="pickedIds" multiple filterable placeholder="勾选用例（按勾选顺序）"
          style="width: 100%">
          <el-option v-for="c in caseOptions" :key="c.id" :label="`${c.code} · ${c.name}`" :value="c.id">
            <span class="hrp-mono">{{ c.code }}</span>
            <span class="hrp-muted"> · {{ c.name }}</span>
            <el-tag v-if="c.status !== 'active'" size="small" type="warning" effect="plain" style="margin-left: 6px">
              {{ caseStatusMeta(c.status).label }}
            </el-tag>
          </el-option>
        </el-select>
      </div>

      <el-table v-loading="memberLoading" :data="pickedMembers" size="small" border style="margin-top: 12px"
        empty-text="还没有勾选用例">
        <el-table-column label="#" width="50">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="code" label="标识" width="130">
          <template #default="{ row }"><span class="hrp-mono">{{ row.code }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="120" show-overflow-tooltip />
        <el-table-column label="状态" width="80">
          <template #default="{ row }">
            <el-tag size="small" :type="caseStatusMeta(row.status).type" effect="plain">
              {{ caseStatusMeta(row.status).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="顺序" width="110">
          <template #default="{ $index }">
            <el-button link :disabled="$index === 0" @click="move($index, -1)">↑</el-button>
            <el-button link :disabled="$index === pickedMembers.length - 1" @click="move($index, 1)">↓</el-button>
            <el-button link type="danger" @click="dropAt($index)">移出</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-alert v-for="[cid, reason] in brokenNotes" :key="cid" type="warning" show-icon :closable="false"
        style="margin-top: 12px">
        {{ reason }}
      </el-alert>

      <template #footer>
        <el-button @click="memberVisible = false">关闭</el-button>
        <el-button type="primary" :loading="memberSubmitting" @click="saveMembers">保存成员</el-button>
      </template>
    </el-drawer>
  </div>
</template>

<style scoped>
.tip {
  font-size: 12px;
  line-height: 1.6;
}

.pick {
  margin-bottom: 4px;
}
</style>
