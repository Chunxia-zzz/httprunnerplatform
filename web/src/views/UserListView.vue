<script setup lang="ts">
/**
 * 账号管理（仅管理员可见，路由 meta.admin 与侧边栏双重把关）。
 *
 * 这个页面真正的价值不是 CRUD，而是把后端的三条守卫**提前说清楚**：
 *
 *   1. 不能删自己 / 不能把自己降级或禁用；
 *   2. 系统里必须永远剩至少一个「可用的管理员」（role=admin 且 enabled=true）；
 *   3. 改角色、改启用状态、重置密码都会**立即吊销对方全部会话**。
 *
 * 前两条如果只在后端拦，用户会撞一个 40300 才知道自己踩线了；第三条如果
 * 不写在界面上，管理员会以为"改完要等一会儿才生效"——而真相是
 * 会话表里缓存着角色，所以后端必须吊销、也确实吊销了。
 *
 * 注意：这里的禁用只是**提示性**的（避免给出点了必然失败的按钮），
 * 真实判定始终在后端 —— 而且后端还比这里多一层：
 * 分页会让我算不全管理员总数，此时前端主动放弃提示（见 hasMorePages）。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessageBox, type FormInstance, type FormRules } from 'element-plus'

import { userApi, type Role, type UpdateUserReq, type User } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { roleMeta } from '@/utils/dict'
import { notifyError, notifyOk } from '@/utils/error'
import { formatTime } from '@/utils/format'
import { passwordValidator } from '@/utils/password'

const auth = useAuthStore()

const loading = ref(false)
const list = ref<User[]>([])
const total = ref(0)

// --- 服务端分页：账号总量很小，但沿用与其他页面一致的分页协议 ---
const page = ref(1)
const pageSize = ref(20)

/** 搜索结果不满一页 ⇒ 列表就是全集 ⇒ 前端算管理员总数才是可信的。 */
const hasMorePages = computed(() => list.value.length < total.value)

const enabledAdmins = computed(() => list.value.filter((u) => u.role === 'admin' && u.enabled))
const enabledAdminCount = computed(() =>
  hasMorePages.value ? Number.POSITIVE_INFINITY : enabledAdmins.value.length,
)

const keyword = ref('')
const roleFilter = ref('')

/**
 * el-table 的作用域插槽把 row 擦成 `DefaultRow`（一个索引签名对象），
 * 它与具体实体类型没有结构关系，直接传给强类型函数会被 TS 拒绝。
 * 与 ProjectListView 的 asProject 同一套路：在入口处收回真实类型，
 * 而不是把函数签名放宽成 any —— 那等于把类型检查从这些函数里整体拿掉。
 */
function asUser(row: unknown): User {
  return row as User
}

// ---------------------------------------------------------------------------
// 加载
// ---------------------------------------------------------------------------

async function load() {
  loading.value = true
  try {
    const data = await userApi.listUsers({
      keyword: keyword.value || undefined,
      role: roleFilter.value || undefined,
      page: page.value,
      page_size: pageSize.value,
    })
    list.value = data.list ?? []
    total.value = data.total ?? 0
  } catch (e) {
    // 40300：非管理员访问（正常流程看不到本页，但直接输 URL 会到这里）
    notifyError(e, '加载账号列表失败')
  } finally {
    loading.value = false
  }
}

function search() {
  page.value = 1
  void load()
}

function resetFilter() {
  keyword.value = ''
  roleFilter.value = ''
  search()
}

// ---------------------------------------------------------------------------
// 新建
// ---------------------------------------------------------------------------

const createVisible = ref(false)
const createFormRef = ref<FormInstance>()
const creating = ref(false)

const createForm = reactive({
  username: '',
  password: '',
  nickname: '',
  role: 'member' as Role,
  enabled: true,
})

const createRules: FormRules = {
  username: [
    { required: true, message: '请输入账号', trigger: 'blur' },
    {
      // 对齐后端 usernameRe：2–64 位，字母/数字/._-@，且以字母或数字开头
      pattern: /^[A-Za-z0-9][A-Za-z0-9._@-]{1,63}$/,
      message: '2–64 位，只允许字母、数字与 . _ - @，且必须以字母或数字开头',
      trigger: 'blur',
    },
  ],
  password: [{ required: true, validator: passwordValidator(), trigger: 'blur' }],
}

function openCreate() {
  createForm.username = ''
  createForm.password = ''
  createForm.nickname = ''
  createForm.role = 'member'
  createForm.enabled = true
  createVisible.value = true
}

async function submitCreate() {
  if (!createFormRef.value) return
  const ok = await createFormRef.value.validate().catch(() => false)
  if (!ok) return

  creating.value = true
  try {
    await userApi.createUser({
      username: createForm.username.trim(),
      password: createForm.password,
      nickname: createForm.nickname.trim(),
      role: createForm.role,
      enabled: createForm.enabled,
    })
    createVisible.value = false
    notifyOk('账号已创建')
    await load()
  } catch (e) {
    // 40001：账号名已存在（后端做了小写归一，Admin 与 admin 视为同一个）
    notifyError(e, '创建账号失败')
  } finally {
    creating.value = false
  }
}

// ---------------------------------------------------------------------------
// 编辑
// ---------------------------------------------------------------------------

const editVisible = ref(false)
const editFormRef = ref<FormInstance>()
const editing = ref(false)
const editingUser = ref<User | null>(null)

const editForm = reactive({ nickname: '', role: 'member' as Role, enabled: true })
const editRules: FormRules = {
  nickname: [{ required: true, message: '请输入昵称', trigger: 'blur' }],
}

const editingSelf = computed(() => editingUser.value?.id === auth.principal?.user_id)

function openEdit(u: User) {
  editingUser.value = u
  editForm.nickname = u.nickname
  editForm.role = (u.role === 'admin' ? 'admin' : 'member') as Role
  editForm.enabled = u.enabled
  editVisible.value = true
}

/** 该账号是当前唯一「可用（已启用）的管理员」。分页不全时一律返回 false。 */
function isLastEnabledAdmin(u: User): boolean {
  if (u.role !== 'admin' || !u.enabled) return false
  return enabledAdminCount.value <= 1
}

/** 编辑弹窗里，该账号的角色/启用状态是否被锁住。 */
const lockRole = computed(() => editingSelf.value || (editingUser.value ? isLastEnabledAdmin(editingUser.value) : false))
const lockEnabled = computed(() => lockRole.value)

const editLockReason = computed(() => {
  if (editingSelf.value) return '不能修改自己的角色或禁用自己 —— 否则你会把自己关在门外。'
  return '这是最后一个已启用的管理员账号，降级或禁用会导致无人能管理系统。'
})

async function submitEdit() {
  if (!editFormRef.value || !editingUser.value) return
  const ok = await editFormRef.value.validate().catch(() => false)
  if (!ok) return

  const target = editingUser.value
  // 被锁住的字段不回传：后端把「不传」当作保持原值，
  // 而传了原值也一样，但少传能避免前端状态与后端不一致时误改。
  const payload: UpdateUserReq = { nickname: editForm.nickname.trim() }
  if (!lockRole.value) payload.role = editForm.role
  if (!lockEnabled.value) payload.enabled = editForm.enabled

  const roleChanged = payload.role !== undefined && payload.role !== target.role
  const enabledChanged = payload.enabled !== undefined && payload.enabled !== target.enabled

  // 权限变更会把人踢下线，这是需要事先知情的事，不能悄悄做。
  if (roleChanged || enabledChanged) {
    const what: string[] = []
    if (roleChanged) what.push(`角色改为「${roleMeta(payload.role as string).label}」`)
    if (enabledChanged) what.push(payload.enabled ? '启用账号' : '禁用账号')
    try {
      await ElMessageBox.confirm(
        `将 ${what.join('、')}。\n该账号当前 ${target.online_sessions} 个在线会话会**立即失效**（不是等一会儿），对方需要重新登录。`,
        '确认变更权限',
        { type: 'warning', confirmButtonText: '确认变更', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
  }

  editing.value = true
  try {
    await userApi.updateUser(target.id, payload)
    editVisible.value = false
    notifyOk(roleChanged || enabledChanged ? '已保存，对方会话已吊销' : '已保存')
    await load()
  } catch (e) {
    notifyError(e, '保存账号失败')
  } finally {
    editing.value = false
  }
}

// ---------------------------------------------------------------------------
// 重置密码
// ---------------------------------------------------------------------------

const resetVisible = ref(false)
const resetFormRef = ref<FormInstance>()
const resetting = ref(false)
const resetTarget = ref<User | null>(null)
const resetForm = reactive({ password: '' })
const resetRules: FormRules = {
  password: [{ required: true, validator: passwordValidator(), trigger: 'blur' }],
}

function openReset(u: User) {
  resetTarget.value = u
  resetForm.password = ''
  resetVisible.value = true
}

async function submitReset() {
  if (!resetFormRef.value || !resetTarget.value) return
  const ok = await resetFormRef.value.validate().catch(() => false)
  if (!ok) return

  resetting.value = true
  try {
    await userApi.resetPassword(resetTarget.value.id, { password: resetForm.password })
    resetVisible.value = false
    notifyOk(`密码已重置，${resetTarget.value.username} 的登录已全部失效`)
    await load()
  } catch (e) {
    // 40300：重置自己（应走「修改密码」）
    notifyError(e, '重置密码失败')
  } finally {
    resetting.value = false
  }
}

// ---------------------------------------------------------------------------
// 删除
// ---------------------------------------------------------------------------

async function remove(u: User) {
  try {
    await ElMessageBox.confirm(
      `确认删除账号「${u.username}」？\n\n` +
        `· 这是软删除，历史执行记录里的关联会保留；\n` +
        `· 该账号 ${u.online_sessions} 个在线会话会立即失效；\n` +
        '· 要临时停用而不是删除，请用「编辑」里的禁用。',
      '删除账号',
      { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }

  try {
    await userApi.deleteUser(u.id)
    notifyOk('账号已删除')
    await load()
  } catch (e) {
    notifyError(e, '删除账号失败')
  }
}

onMounted(load)
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">账号管理</h2>
        <p class="hrp-page__sub">
          只有 <b>管理员</b> 能进入本页。改角色、改启用状态、重置密码都会
          <b>立即吊销</b> 对方的全部登录会话。
        </p>
      </div>
      <el-button type="primary" @click="openCreate">
        <el-icon><Plus /></el-icon>新建账号
      </el-button>
    </div>

    <el-card shadow="never" class="hrp-card">
      <div class="hrp-toolbar">
        <el-input
          v-model="keyword"
          placeholder="按账号或昵称搜索"
          clearable
          style="width: 220px"
          @keyup.enter="search"
          @clear="search"
        >
          <template #prefix><el-icon><Search /></el-icon></template>
        </el-input>
        <el-select v-model="roleFilter" placeholder="全部角色" clearable style="width: 140px" @change="search">
          <el-option label="管理员" value="admin" />
          <el-option label="成员" value="member" />
        </el-select>
        <el-button :loading="loading" @click="search">查询</el-button>
        <el-button link @click="resetFilter">重置</el-button>
        <span class="hrp-muted">共 {{ total }} 个账号</span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe empty-text="没有匹配的账号">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column label="账号" min-width="180">
          <template #default="{ row }">
            <span class="hrp-mono">{{ row.username }}</span>
            <el-tag v-if="row.id === auth.principal?.user_id" size="small" type="success" effect="plain" class="ml6">
              我自己
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="nickname" label="昵称" min-width="120" show-overflow-tooltip />
        <el-table-column label="角色" width="140">
          <template #default="{ row }">
            <el-tooltip :content="roleMeta(row.role).desc" placement="top">
              <el-tag :type="roleMeta(row.role).type" size="small" effect="plain">
                {{ roleMeta(row.role).label }}
              </el-tag>
            </el-tooltip>
            <el-tooltip v-if="isLastEnabledAdmin(asUser(row))" content="最后一个已启用的管理员，不能降级或禁用" placement="top">
              <el-tag size="small" type="warning" effect="plain" class="ml6">唯一</el-tag>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="row.enabled ? 'success' : 'info'" size="small" effect="plain">
              {{ row.enabled ? '已启用' : '已禁用' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="在线会话" width="110" align="center">
          <template #default="{ row }">
            <el-tooltip content="该账号当前有效的登录会话数；被禁用或重置密码后应立即归零" placement="top">
              <span :class="{ 'hrp-muted': row.online_sessions === 0 }">{{ row.online_sessions }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">{{ formatTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="220" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openEdit(asUser(row))">编辑</el-button>
            <el-tooltip
              :disabled="row.id !== auth.principal?.user_id"
              content="请用右上角「修改密码」（需要验证原密码）"
              placement="top"
            >
              <span>
                <el-button
                  link
                  type="primary"
                  :disabled="row.id === auth.principal?.user_id"
                  @click="openReset(asUser(row))"
                >
                  重置密码
                </el-button>
              </span>
            </el-tooltip>
            <el-tooltip
              :disabled="row.id !== auth.principal?.user_id && !isLastEnabledAdmin(asUser(row))"
              :content="
                row.id === auth.principal?.user_id
                  ? '不能删除自己的账号'
                  : '这是最后一个已启用的管理员，删除后无人能管理系统'
              "
              placement="top"
            >
              <span>
                <el-button
                  link
                  type="danger"
                  :disabled="row.id === auth.principal?.user_id || isLastEnabledAdmin(asUser(row))"
                  @click="remove(asUser(row))"
                >
                  删除
                </el-button>
              </span>
            </el-tooltip>
          </template>
        </el-table-column>
      </el-table>

      <el-pagination
        class="pager"
        layout="total, sizes, prev, pager, next"
        :total="total"
        :current-page="page"
        :page-size="pageSize"
        :page-sizes="[20, 50, 100]"
        @current-change="(p: number) => { page = p; load() }"
        @size-change="(s: number) => { pageSize = s; page = 1; load() }"
      />
    </el-card>

    <!-- 新建 -->
    <el-dialog v-model="createVisible" title="新建账号" width="480px" destroy-on-close>
      <el-form ref="createFormRef" :model="createForm" :rules="createRules" label-width="90px">
        <el-form-item label="账号" prop="username">
          <el-input v-model="createForm.username" placeholder="例如 zhangsan 或 zhangsan@corp.com" class="hrp-mono" />
          <div class="hrp-muted form-tip">账号是登录标识，创建后不可修改（它还会进入执行记录的历史）。</div>
        </el-form-item>
        <el-form-item label="初始密码" prop="password">
          <el-input v-model="createForm.password" type="password" show-password autocomplete="new-password" />
          <div class="hrp-muted form-tip">至少 8 位，不含空白字符。建议创建后让对方自行修改。</div>
        </el-form-item>
        <el-form-item label="昵称">
          <el-input v-model="createForm.nickname" placeholder="留空则与账号相同" />
        </el-form-item>
        <el-form-item label="角色">
          <el-radio-group v-model="createForm.role">
            <el-radio value="member">成员</el-radio>
            <el-radio value="admin">管理员</el-radio>
          </el-radio-group>
          <div class="hrp-muted form-tip">{{ roleMeta(createForm.role).desc }}</div>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="createForm.enabled" />
          <span class="hrp-muted form-tip-inline">禁用状态下无法登录</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <!-- 编辑 -->
    <el-dialog v-model="editVisible" title="编辑账号" width="480px" destroy-on-close>
      <el-alert v-if="lockRole" type="warning" :closable="false" show-icon class="tip">
        {{ editLockReason }}
      </el-alert>
      <el-form ref="editFormRef" :model="editForm" :rules="editRules" label-width="90px">
        <el-form-item label="账号">
          <span class="hrp-mono">{{ editingUser?.username }}</span>
          <span class="hrp-muted form-tip-inline">不可修改</span>
        </el-form-item>
        <el-form-item label="昵称" prop="nickname">
          <el-input v-model="editForm.nickname" />
          <div class="hrp-muted form-tip">
            只改昵称不会把对方踢下线，顶栏显示会实时更新。
          </div>
        </el-form-item>
        <el-form-item label="角色">
          <el-radio-group v-model="editForm.role" :disabled="lockRole">
            <el-radio value="member">成员</el-radio>
            <el-radio value="admin">管理员</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="editForm.enabled" :disabled="lockEnabled" />
          <span class="hrp-muted form-tip-inline">
            {{ editingUser?.enabled === false ? '当前已禁用' : '禁用后立即登出对方' }}
          </span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editing" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>

    <!-- 重置密码 -->
    <el-dialog v-model="resetVisible" title="重置密码" width="460px" destroy-on-close>
      <el-alert type="warning" :closable="false" show-icon class="tip">
        将把 <b>{{ resetTarget?.username }}</b> 的密码改为你设定的值，并
        <b>立即吊销其全部登录会话</b>（共 {{ resetTarget?.online_sessions ?? 0 }} 个）。
        请通过安全渠道告知对方，并建议其登录后立即修改。
      </el-alert>
      <el-form ref="resetFormRef" :model="resetForm" :rules="resetRules" label-width="90px">
        <el-form-item label="新密码" prop="password">
          <el-input v-model="resetForm.password" type="password" show-password autocomplete="new-password" />
          <div class="hrp-muted form-tip">至少 8 位，不含空白字符；不能与该账号名相同或使用常见弱密码。</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="resetVisible = false">取消</el-button>
        <el-button type="danger" :loading="resetting" @click="submitReset">确认重置</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.ml6 {
  margin-left: 6px;
}

.form-tip {
  font-size: 12px;
  line-height: 1.6;
  margin-top: 4px;
}

.form-tip-inline {
  font-size: 12px;
  margin-left: 8px;
}

.tip {
  margin-bottom: 16px;
}

.pager {
  margin-top: 12px;
  justify-content: flex-end;
}
</style>
