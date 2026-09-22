<script setup lang="ts">
/**
 * 用例列表。
 *
 * 左侧按模块分组的树 + 右侧表格。两个容易踩的点：
 *   1. `step_count` 是**启用**步骤数（后端 enabledStepCounts 只数 enabled=true）。
 *      全部禁用的用例显示 0，这是刻意的：让"这条用例会跑多少东西"一眼可见。
 *   2. 用例名在项目内唯一（引擎以 config.name 作 summary.json 的唯一标识，
 *      重名会导致结果无法区分，实测 F11），所以重名会直接被后端拒。
 */
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, ElTree } from 'element-plus'

import { caseApi, environmentApi, runApi, type CaseListItem, type CaseListQuery, type ModuleCount } from '@/api'
import { useProjectStore } from '@/stores/project'
import { caseStatusMeta, priorityMeta, stepStatusMeta } from '@/utils/dict'
import { formatTime } from '@/utils/format'
import { notifyError, notifyOk } from '@/utils/error'

const router = useRouter()
const projects = useProjectStore()

const loading = ref(false)
const list = ref<CaseListItem[]>([])
const total = ref(0)
const tree = ref<ModuleCount[]>([])
const runningId = ref(0)

const query = reactive<CaseListQuery>({
  page: 1,
  page_size: 20,
  module: '',
  priority: '',
  status: '',
  keyword: '',
})

const projectId = computed(() => projects.currentId)

/** 树的节点。加一个"全部"根节点，让用户能一键取消模块筛选。 */
interface TreeNode {
  /** el-tree 的 node-key 必须全局唯一。用 module 当 key 会撞：
   *  根节点的 module 是 ''，而"未分组"的 module 也是 ''。 */
  key: string
  module: string
  label: string
  count: number
}

const treeData = computed<TreeNode[]>(() => [
  { key: '__all__', module: '', label: '全部用例', count: total.value },
  ...tree.value.map((t) => ({
    key: `mod:${t.module}`,
    module: t.module,
    label: t.module ? t.module : '（未分组）',
    count: t.count,
  })),
])

const activeModule = ref('')
const treeRef = ref<InstanceType<typeof ElTree>>()

// 用代码改 activeModule（例如点"重置"）时同步树的高亮。
// el-tree 的 current-node-key 只在初始化时生效，不会跟随 prop 变化。
watch(activeModule, (m) => {
  treeRef.value?.setCurrentKey(m ? `mod:${m}` : '__all__')
})

async function loadCases() {
  if (!projectId.value) return
  loading.value = true
  try {
    const data = await caseApi.listCases(projectId.value, { ...query, module: activeModule.value })
    list.value = data.list
    total.value = data.total
  } catch (e) {
    notifyError(e, '加载用例列表失败')
  } finally {
    loading.value = false
  }
}

async function loadTree() {
  if (!projectId.value) return
  try {
    tree.value = await caseApi.caseTree(projectId.value)
  } catch (e) {
    notifyError(e, '加载模块树失败')
  }
}

async function reload() {
  await Promise.all([loadCases(), loadTree()])
}

function onTreeClick(data: { module: string }) {
  activeModule.value = data.module ?? ''
  query.page = 1
  void loadCases()
}

function resetFilters() {
  query.keyword = ''
  query.priority = ''
  query.status = ''
  activeModule.value = ''
  query.page = 1
  void loadCases()
}

/**
 * el-table 的作用域插槽把 row 擦成 `DefaultRow`，与具体实体类型无结构关系。
 * 在入口处收回真实类型，避免把函数签名整体放宽成 any。
 */
function asCase(row: unknown): CaseListItem {
  return row as CaseListItem
}

/** 从列表直接执行一条用例：用项目的默认环境（env_id=0）。 */
async function runCase(row: unknown) {
  const item = asCase(row)
  runningId.value = item.id
  try {
    const env = await resolveDefaultEnvId()
    const resp = await runApi.startRun({
      project_id: projectId.value,
      target_type: 'case',
      target_id: item.id,
      env_id: env,
      options: { gen_html_report: true },
    })
    notifyOk(`已开始执行「${item.name}」`)
    await router.push({ name: 'run-detail', params: { id: resp.run_id } })
  } catch (e) {
    notifyError(e, '启动执行失败')
  } finally {
    runningId.value = 0
  }
}

/**
 * 取默认环境 ID。
 *
 * 传 0 让后端自己回退到默认环境也是合法的，但那样用户在详情页看不到
 * 用的是哪个环境，出错时要多跳一次页面才能确认。这里显式取出来。
 */
async function resolveDefaultEnvId(): Promise<number> {
  try {
    const data = await environmentApi.listEnvironments(projectId.value)
    const def = data.list.find((e) => e.is_default) ?? data.list[0]
    return def?.id ?? 0
  } catch {
    return 0
  }
}

async function removeCase(row: unknown) {
  const item = asCase(row)
  try {
    await ElMessageBox.confirm(
      `确认删除用例「${item.name}」？若它被用例集引用，后端会拒绝删除。`,
      '删除用例',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await caseApi.deleteCase(item.id)
    notifyOk('用例已删除')
    await reload()
  } catch (e) {
    notifyError(e, '删除用例失败')
  }
}

function goNew() {
  if (!projectId.value) {
    ElMessage.warning('请先选择项目')
    return
  }
  void router.push({ name: 'case-new' })
}

function goEdit(row: unknown) {
  void router.push({ name: 'case-edit', params: { id: asCase(row).id } })
}

function goRun(row: unknown) {
  const id = asCase(row).last_run_id
  if (!id) return
  void router.push({ name: 'run-detail', params: { id } })
}

onMounted(reload)
watch(projectId, () => {
  query.page = 1
  activeModule.value = ''
  void reload()
})
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">用例管理</h2>
        <p class="hrp-page__sub">
          表单化编辑，保存时编译成 hrp 用例；执行时一个用例一个独立子进程（断言失败会 panic，目录模式会丢整批结果）。
        </p>
      </div>
      <el-button type="primary" :disabled="!projectId" @click="goNew">
        <el-icon><Plus /></el-icon>新建用例
      </el-button>
    </div>

    <el-empty v-if="!projectId" description="请先在顶栏选择项目" />

    <div v-else class="layout">
      <el-card shadow="never" class="layout__tree">
        <div class="tree-title">模块</div>
        <el-tree
          ref="treeRef"
          :data="treeData"
          :props="{ label: 'label', children: 'children' }"
          node-key="key"
          :current-node-key="activeModule ? `mod:${activeModule}` : '__all__'"
          highlight-current
          :expand-on-click-node="false"
          @node-click="onTreeClick"
        >
          <template #default="{ data }">
            <span class="tree-node">
              <span class="tree-node__label">{{ data.label }}</span>
              <span class="hrp-muted">{{ data.count }}</span>
            </span>
          </template>
        </el-tree>
      </el-card>

      <el-card shadow="never" class="layout__main">
        <div class="hrp-toolbar">
          <el-input
            v-model="query.keyword"
            placeholder="按标识 / 名称 / 标签搜索"
            clearable
            style="width: 240px"
            @keyup.enter="loadCases"
            @clear="loadCases"
          >
            <template #prefix><el-icon><Search /></el-icon></template>
          </el-input>
          <el-select v-model="query.priority" placeholder="优先级" clearable style="width: 110px" @change="loadCases">
            <el-option v-for="p in ['P0', 'P1', 'P2', 'P3']" :key="p" :label="p" :value="p" />
          </el-select>
          <el-select v-model="query.status" placeholder="状态" clearable style="width: 110px" @change="loadCases">
            <el-option label="启用" value="active" />
            <el-option label="草稿" value="draft" />
            <el-option label="禁用" value="disabled" />
          </el-select>
          <el-button :loading="loading" @click="loadCases">查询</el-button>
          <el-button @click="resetFilters">重置</el-button>
          <span class="hrp-muted">共 {{ total }} 条</span>
        </div>

        <el-table v-loading="loading" :data="list" border stripe empty-text="没有符合条件的用例">
          <el-table-column prop="id" label="ID" width="70" />
          <el-table-column prop="name" label="名称" min-width="170" show-overflow-tooltip>
            <template #default="{ row }">
              <el-link type="primary" :underline="false" @click="goEdit(row)">{{ row.name }}</el-link>
            </template>
          </el-table-column>
          <el-table-column prop="code" label="标识" width="150">
            <template #default="{ row }"><span class="hrp-mono">{{ row.code }}</span></template>
          </el-table-column>
          <el-table-column prop="module" label="模块" width="110">
            <template #default="{ row }">
              <span :class="{ 'hrp-muted': !row.module }">{{ row.module || '（未分组）' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="优先级" width="88">
            <template #default="{ row }">
              <el-tag size="small" :type="priorityMeta(row.priority).type" effect="plain">
                {{ priorityMeta(row.priority).label }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <el-tag size="small" :type="caseStatusMeta(row.status).type" effect="plain">
                {{ caseStatusMeta(row.status).label }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="启用步骤" width="96">
            <template #default="{ row }">
              <el-tooltip v-if="row.step_count === 0" content="没有任何启用的步骤：执行时会被引擎静默丢弃（退出码 0 但一条都没跑）" placement="top">
                <el-tag size="small" type="danger" effect="plain">0 步</el-tag>
              </el-tooltip>
              <span v-else>{{ row.step_count }} 步</span>
            </template>
          </el-table-column>
          <el-table-column prop="tags" label="标签" min-width="110" show-overflow-tooltip>
            <template #default="{ row }">
              <span :class="{ 'hrp-muted': !row.tags }">{{ row.tags || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="最近结果" width="120">
            <template #default="{ row }">
              <el-tag
                v-if="row.last_status"
                size="small"
                :type="stepStatusMeta(row.last_status).type"
                effect="plain"
                class="clickable"
                @click="goRun(row)"
              >
                {{ stepStatusMeta(row.last_status).label }}
              </el-tag>
              <span v-else class="hrp-muted">未执行</span>
            </template>
          </el-table-column>
          <el-table-column label="更新时间" width="170">
            <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="190" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" :loading="runningId === row.id" @click="runCase(row)">执行</el-button>
              <el-button link type="primary" @click="goEdit(row)">编辑</el-button>
              <el-button link type="danger" @click="removeCase(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>

        <el-pagination
          class="pager"
          layout="total, sizes, prev, pager, next"
          :total="total"
          :current-page="query.page"
          :page-size="query.page_size"
          :page-sizes="[10, 20, 50, 100]"
          @current-change="(p: number) => { query.page = p; loadCases() }"
          @size-change="(s: number) => { query.page_size = s; query.page = 1; loadCases() }"
        />
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.layout {
  display: flex;
  gap: 14px;
  align-items: flex-start;
}

.layout__tree {
  width: 220px;
  flex: none;
}

.layout__main {
  flex: 1;
  min-width: 0;
}

.tree-title {
  font-weight: 600;
  margin-bottom: 8px;
  font-size: 13px;
}

.tree-node {
  display: flex;
  justify-content: space-between;
  align-items: center;
  width: 100%;
  padding-right: 6px;
  font-size: 13px;
}

.tree-node__label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}

.clickable {
  cursor: pointer;
}
</style>
