<script setup lang="ts">
/**
 * 测试计划管理。
 *
 * 计划 = 一组用例集 + 什么时候跑。这个页面上三件事最要紧：
 *
 *   1. **cron 要配时区**，并当场把"下一次什么时候跑"算给用户看。
 *      只给 cron 表达式不给时区，等于只给了一半信息 —— 服务器跑在 UTC 上时，
 *      用户填的"早上 9 点"会是 UTC 9 点，而这类错误不报错，只会静默错位。
 *   2. **跳过的调度点要看得见**。定时任务是运维里最难发现的一类故障：
 *      它不会报错，只会让该跑的没跑。所以 last_missed_at / last_skip_reason
 *      必须在列表上有位置。
 *   3. **新建默认不启用**，避免一保存就开始跑。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessageBox, type FormInstance } from 'element-plus'

import {
  environmentApi,
  planApi,
  runApi,
  suiteApi,
  type Environment,
  type ID,
  type Plan,
  type PlanSuite,
  type Suite,
} from '@/api'
import { useProjectStore } from '@/stores/project'
import {
  CRON_PRESETS,
  COMMON_TIMEZONES,
  triggerTypeMeta,
} from '@/utils/dict'
import { notifyError, notifyOk } from '@/utils/error'
import { useRouter } from 'vue-router'

const projects = useProjectStore()
const router = useRouter()

const loading = ref(false)
const list = ref<Plan[]>([])
const envOptions = ref<Environment[]>([])
const suiteOptions = ref<Suite[]>([])

const dialogVisible = ref(false)
const submitting = ref(false)
const editingId = ref(0)
const formRef = ref<FormInstance>()
const form = ref({
  name: '',
  description: '',
  env_id: 0 as ID,
  trigger_type: 'manual' as 'manual' | 'cron',
  cron_expr: '0 9 * * *',
  timezone: 'Asia/Shanghai',
  enabled: false,
  suite_ids: [] as ID[],
})

// 成员抽屉
const memberVisible = ref(false)
const memberPlanId = ref<ID>(0)
const memberPlanName = ref('')
const members = ref<PlanSuite[]>([])
const memberLoading = ref(false)
const pickedIds = ref<ID[]>([])
const memberSubmitting = ref(false)

const isEdit = computed(() => editingId.value > 0)
const projectId = computed(() => projects.currentId)
const isCron = computed(() => form.value.trigger_type === 'cron')

async function load() {
  if (!projectId.value) {
    list.value = []
    return
  }
  loading.value = true
  try {
    const data = await planApi.listPlans(projectId.value)
    list.value = data.list
  } catch (e) {
    notifyError(e, '加载测试计划失败')
  } finally {
    loading.value = false
  }
}

async function loadOptions() {
  if (!projectId.value) return
  try {
    const [envs, suites] = await Promise.all([
      environmentApi.listEnvironments(projectId.value),
      suiteApi.listSuites(projectId.value),
    ])
    envOptions.value = envs.list
    suiteOptions.value = suites.list
  } catch (e) {
    notifyError(e, '加载环境/用例集选项失败')
  }
}

function asPlan(row: unknown): Plan {
  return row as Plan
}

function openCreate() {
  editingId.value = 0
  form.value = {
    name: '',
    description: '',
    env_id: envOptions.value.find((e) => e.is_default)?.id ?? 0,
    trigger_type: 'manual',
    cron_expr: '0 9 * * *',
    timezone: 'Asia/Shanghai',
    enabled: false,
    suite_ids: [],
  }
  dialogVisible.value = true
}

function openEdit(row: unknown) {
  const p = asPlan(row)
  editingId.value = p.id
  form.value = {
    name: p.name,
    description: p.description,
    env_id: p.env_id,
    trigger_type: p.trigger_type === 'cron' ? 'cron' : 'manual',
    cron_expr: p.cron_expr || '0 9 * * *',
    timezone: p.timezone || 'Asia/Shanghai',
    enabled: p.enabled,
    suite_ids: [],
  }
  dialogVisible.value = true
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  const payload = {
    name: form.value.name.trim(),
    description: form.value.description.trim(),
    env_id: form.value.env_id,
    trigger_type: form.value.trigger_type,
    cron_expr: isCron.value ? form.value.cron_expr.trim() : '',
    timezone: form.value.timezone,
    enabled: form.value.enabled,
  }

  submitting.value = true
  try {
    if (isEdit.value) {
      await planApi.updatePlan(editingId.value, payload)
      notifyOk('计划已保存')
    } else {
      await planApi.createPlan(projectId.value, { ...payload, suite_ids: form.value.suite_ids })
      notifyOk('计划已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    notifyError(e, isEdit.value ? '保存计划失败' : '创建计划失败')
  } finally {
    submitting.value = false
  }
}

async function remove(row: unknown) {
  const p = asPlan(row)
  try {
    await ElMessageBox.confirm(`确认删除计划「${p.name}」？`, '删除测试计划', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await planApi.deletePlan(p.id)
    notifyOk('计划已删除')
    await load()
  } catch (e) {
    notifyError(e, '删除计划失败')
  }
}

async function toggle(row: unknown, on: boolean) {
  const p = asPlan(row)
  try {
    await planApi.setPlanEnabled(p.id, on)
    notifyOk(on ? '计划已启用' : '计划已停用')
    await load()
  } catch (e) {
    notifyError(e, '切换计划开关失败')
  }
}

async function runNow(row: unknown) {
  const p = asPlan(row)
  try {
    const suites = await planApi.listPlanSuites(p.id)
    if (suites.length === 0) {
      notifyError(new Error('这个计划还没有挂用例集'), '无法执行')
      return
    }
    // 一次计划触发 = 每个用例集各起一条执行记录（失败隔离）。
    // 这里跳转到最后一条，让页面不至于跳来跳去。
    let lastRunId = 0
    for (const s of suites) {
      const res = await runApi.startRun({
        project_id: projectId.value,
        target_type: 'suite',
        target_id: s.suite_id,
        env_id: p.env_id,
      })
      lastRunId = res.run_id
    }
    notifyOk(`已为 ${suites.length} 个用例集启动执行`)
    if (lastRunId) await router.push({ name: 'run-detail', params: { id: lastRunId } })
  } catch (e) {
    notifyError(e, '启动执行失败')
  }
}

// ---------------------------------------------------------------------------
// 成员
// ---------------------------------------------------------------------------

async function openMembers(row: unknown) {
  const p = asPlan(row)
  memberPlanId.value = p.id
  memberPlanName.value = p.name
  memberVisible.value = true
  memberLoading.value = true
  try {
    members.value = await planApi.listPlanSuites(p.id)
    pickedIds.value = members.value.map((m) => m.suite_id)
  } catch (e) {
    notifyError(e, '加载成员失败')
  } finally {
    memberLoading.value = false
  }
}

const pickedSuites = computed(() =>
  pickedIds.value
    .map((id) => suiteOptions.value.find((s) => s.id === id))
    .filter((s): s is Suite => !!s),
)

function move(idx: number, delta: number) {
  const next = idx + delta
  if (next < 0 || next >= pickedIds.value.length) return
  const arr = [...pickedIds.value]
  const tmp = arr[idx]
  arr[idx] = arr[next]!
  arr[next] = tmp!
  pickedIds.value = arr
}

async function saveMembers() {
  memberSubmitting.value = true
  try {
    members.value = await planApi.setPlanSuites(memberPlanId.value, pickedIds.value)
    notifyOk('成员已保存')
    await load()
  } catch (e) {
    notifyError(e, '保存成员失败')
  } finally {
    memberSubmitting.value = false
  }
}

onMounted(async () => {
  await Promise.all([load(), loadOptions()])
})
watch(projectId, async () => {
  await Promise.all([load(), loadOptions()])
})
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">测试计划</h2>
        <p class="hrp-page__sub">
          计划 = 一组用例集 + 什么时候跑。定时按<strong>计划自己的时区</strong>解释 cron。
        </p>
      </div>
      <el-button type="primary" :disabled="!projectId" @click="openCreate">
        <el-icon><Plus /></el-icon>新建计划
      </el-button>
    </div>

    <el-empty v-if="!projectId" description="请先在顶栏选择项目" />

    <el-card v-else shadow="never" class="hrp-card">
      <div class="hrp-toolbar">
        <el-button :loading="loading" @click="load"><el-icon><Refresh /></el-icon>刷新</el-button>
        <span class="hrp-muted">共 {{ list.length }} 个计划</span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe empty-text="该项目还没有测试计划">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
        <el-table-column label="触发" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="triggerTypeMeta(row.trigger_type).type" effect="plain">
              {{ triggerTypeMeta(row.trigger_type).label }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="定时" min-width="200">
          <template #default="{ row }">
            <template v-if="row.trigger_type === 'cron'">
              <div><span class="hrp-mono">{{ row.cron_expr }}</span></div>
              <div class="hrp-muted">
                {{ row.cron_human || '—' }} · {{ row.timezone }}
              </div>
              <div class="hrp-muted">下次 {{ row.next_fire_at || '—' }}</div>
            </template>
            <span v-else class="hrp-muted">手动触发</span>
          </template>
        </el-table-column>
        <el-table-column prop="suite_count" label="用例集" width="80" />
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <!-- el-switch 的 update 事件是联合类型，这里显式收窄回 boolean -->
            <el-switch :model-value="row.enabled"
              @update:model-value="(v: string | number | boolean) => toggle(row, v === true)" />
          </template>
        </el-table-column>
        <el-table-column label="最近" min-width="180">
          <template #default="{ row }">
            <div class="hrp-muted">触发 {{ row.last_fired_at || '—' }}</div>
            <el-tag v-if="row.last_skip_reason" size="small" type="warning" effect="plain">
              跳过：{{ row.last_skip_reason }}（{{ row.last_missed_at }}）
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="240" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openMembers(row)">成员</el-button>
            <el-button link type="success" @click="runNow(row)">立即执行</el-button>
            <el-button link @click="openEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑计划' : '新建计划'" width="680px" destroy-on-close>
      <el-form ref="formRef" :model="form" label-width="110px">
        <el-form-item label="名称" prop="name" :rules="[{ required: true, message: '请输入计划名称', trigger: 'blur' }]">
          <el-input v-model="form.name" placeholder="例如 每日回归" />
        </el-form-item>

        <el-form-item label="环境">
          <el-select v-model="form.env_id" placeholder="不指定则用项目默认环境" clearable style="width: 260px">
            <el-option v-for="e in envOptions" :key="e.id" :label="e.name" :value="e.id" />
          </el-select>
        </el-form-item>

        <el-form-item label="触发方式">
          <el-radio-group v-model="form.trigger_type">
            <el-radio value="manual">手动</el-radio>
            <el-radio value="cron">定时</el-radio>
          </el-radio-group>
        </el-form-item>

        <template v-if="isCron">
          <el-form-item label="cron" prop="cron_expr"
            :rules="[{ required: true, message: '请填写 cron 表达式', trigger: 'blur' }]">
            <el-input v-model="form.cron_expr" class="hrp-mono" style="width: 220px" placeholder="0 9 * * *" />
            <div class="hrp-muted tip">
              5 段：<span class="hrp-mono">分 时 日 月 周</span>，支持
              <span class="hrp-mono">@daily</span> / <span class="hrp-mono">@every 30m</span>
            </div>
            <div style="margin-top: 6px">
              <el-button v-for="p in CRON_PRESETS" :key="p.value" size="small" link
                @click="form.cron_expr = p.value">{{ p.label }}</el-button>
            </div>
          </el-form-item>

          <el-form-item label="时区">
            <el-select v-model="form.timezone" filterable allow-create style="width: 260px">
              <el-option v-for="t in COMMON_TIMEZONES" :key="t.value" :label="t.label" :value="t.value" />
            </el-select>
            <div class="hrp-muted tip">
              ⭐ 按这个时区解释 cron。服务器多半跑在 UTC，不指定时区会让"早上 9 点"静默错位 8 小时。
            </div>
          </el-form-item>
        </template>

        <el-form-item v-if="!isEdit" label="用例集">
          <el-select v-model="form.suite_ids" multiple filterable placeholder="可稍后在「成员」里挂" style="width: 100%">
            <el-option v-for="s in suiteOptions" :key="s.id" :label="`${s.code} · ${s.name}`" :value="s.id" />
          </el-select>
        </el-form-item>

        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
          <span class="hrp-muted tip">新建默认关闭 —— 免得一保存就开始跑</span>
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

    <el-drawer v-model="memberVisible" :title="`成员 · ${memberPlanName}`" size="560px">
      <el-alert type="info" show-icon :closable="false" style="margin-bottom: 12px">
        勾选的用例集<strong>按顺序</strong>各起一条执行记录：一个用例集出问题不影响其它。
      </el-alert>

      <el-select v-model="pickedIds" multiple filterable placeholder="选择用例集" style="width: 100%">
        <el-option v-for="s in suiteOptions" :key="s.id" :label="`${s.code} · ${s.name}`" :value="s.id">
          <span class="hrp-mono">{{ s.code }}</span>
          <span class="hrp-muted"> · {{ s.name }}（{{ s.case_count }} 条用例）</span>
        </el-option>
      </el-select>

      <el-table v-loading="memberLoading" :data="pickedSuites" size="small" border style="margin-top: 12px"
        empty-text="还没有选择用例集">
        <el-table-column label="#" width="50">
          <template #default="{ $index }">{{ $index + 1 }}</template>
        </el-table-column>
        <el-table-column prop="code" label="标识" width="130">
          <template #default="{ row }"><span class="hrp-mono">{{ row.code }}</span></template>
        </el-table-column>
        <el-table-column prop="name" label="名称" min-width="120" show-overflow-tooltip />
        <el-table-column prop="case_count" label="用例数" width="80" />
        <el-table-column label="顺序" width="90">
          <template #default="{ $index }">
            <el-button link :disabled="$index === 0" @click="move($index, -1)">↑</el-button>
            <el-button link :disabled="$index === pickedSuites.length - 1" @click="move($index, 1)">↓</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-alert v-for="m in members.filter((x) => !x.runnable)" :key="m.suite_id" type="warning" show-icon
        :closable="false" style="margin-top: 12px">
        {{ m.suite_code }}：{{ m.skip_reason }}
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
  margin-left: 8px;
}
</style>
