<script setup lang="ts">
/**
 * CI 令牌管理。
 *
 * 这个页面上只有一件事是真正要紧的：
 *
 *   ⭐ **明文只出现一次**。签发之后列表里只剩 prefix，谁也拿不回来
 *   （库里存的是 sha256 哈希）。所以签发弹窗必须让用户当场复制，
 *   并且关窗前给一次"你确定保存好了吗"的确认 —— 关掉就真的没了。
 *
 * 另外两处刻意做重的说明：
 *   - 令牌是**按项目签发**的，触发时请求里的 project_id 必须一致；
 *   - `exit_code_for_ci` 是给 pipeline 直接用的数字（0/1/2），
 *     免得每条流水线都写一段 jq 去解析状态枚举。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessageBox, type FormInstance } from 'element-plus'

import { planApi, type IssuedToken, type Token } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError, notifyOk } from '@/utils/error'

const projects = useProjectStore()

const loading = ref(false)
const list = ref<Token[]>([])

const dialogVisible = ref(false)
const submitting = ref(false)
const formRef = ref<FormInstance>()
const form = ref({ name: '', ttl_days: 0 })

/** 签发出来的明文。只存在于内存里，关窗即弃。 */
const issued = ref<IssuedToken | null>(null)
const copied = ref(false)

const projectId = computed(() => projects.currentId)

async function load() {
  if (!projectId.value) {
    list.value = []
    return
  }
  loading.value = true
  try {
    list.value = await planApi.listTokens(projectId.value)
  } catch (e) {
    notifyError(e, '加载令牌失败')
  } finally {
    loading.value = false
  }
}

function openIssue() {
  issued.value = null
  copied.value = false
  form.value = { name: '', ttl_days: 0 }
  dialogVisible.value = true
}

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    issued.value = await planApi.issueToken(projectId.value, {
      name: form.value.name.trim(),
      ttl_days: form.value.ttl_days,
    })
    notifyOk('令牌已签发，请立即复制保存')
    await load()
  } catch (e) {
    notifyError(e, '签发令牌失败')
  } finally {
    submitting.value = false
  }
}

async function copyToken() {
  if (!issued.value) return
  try {
    await navigator.clipboard.writeText(issued.value.token)
    copied.value = true
    notifyOk('已复制到剪贴板')
  } catch {
    notifyError(new Error('浏览器拒绝了剪贴板访问，请手动选中复制'), '复制失败')
  }
}

/**
 * 关窗前确认。
 *
 * 这一步看着啰嗦，但"签完没保存、关窗后再也要不回来"是这类页面
 * 最常见的投诉 —— 而重签意味着要去改 CI 的 secret，成本远高于多点一次。
 */
async function beforeClose() {
  if (!issued.value || copied.value) {
    dialogVisible.value = false
    return
  }
  try {
    await ElMessageBox.confirm(
      '令牌明文只显示这一次，关闭后就再也取不回来了（只能吊销后重签）。确定已经保存好了吗？',
      '确认关闭',
      { type: 'warning', confirmButtonText: '我已保存，关闭', cancelButtonText: '再看看' },
    )
    dialogVisible.value = false
  } catch {
    /* 留在窗口里 */
  }
}

async function revoke(row: unknown) {
  const t = row as Token
  try {
    await ElMessageBox.confirm(
      `确认吊销令牌「${t.name}」？使用该令牌的流水线会立刻失败（40100）。`,
      '吊销令牌',
      { type: 'warning', confirmButtonText: '吊销', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await planApi.revokeToken(projectId.value, t.id)
    notifyOk('令牌已吊销')
    await load()
  } catch (e) {
    notifyError(e, '吊销令牌失败')
  }
}

/** 给一段可以直接贴进流水线的调用示例。 */
const curlExample = computed(() => {
  const base = typeof location !== 'undefined' ? location.origin : 'http://127.0.0.1:8080'
  return [
    `# 触发（默认异步，返回 run_id）`,
    `curl -X POST ${base}/open/runs \\`,
    `  -H "Authorization: Bearer $HRP_TOKEN" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -d '{"project_id": ${projectId.value}, "target_type": "suite", "target_id": 1}'`,
    ``,
    `# 取结果：exit_code_for_ci 0=成功 1=有用例没过 2=还没跑完`,
    `curl ${base}/open/runs/42/result?wait=1 -H "Authorization: Bearer $HRP_TOKEN"`,
  ].join('\n')
})

onMounted(load)
watch(projectId, () => void load())
</script>

<template>
  <div class="hrp-page">
    <div class="hrp-page__head">
      <div>
        <h2 class="hrp-page__title">CI 令牌</h2>
        <p class="hrp-page__sub">
          按项目签发；触发与读结果都会校验 <span class="hrp-mono">project_id</span> 与令牌所属项目一致。
        </p>
      </div>
      <el-button type="primary" :disabled="!projectId" @click="openIssue">
        <el-icon><Plus /></el-icon>签发令牌
      </el-button>
    </div>

    <el-empty v-if="!projectId" description="请先在顶栏选择项目" />

    <el-card v-else shadow="never" class="hrp-card">
      <el-alert type="warning" show-icon :closable="false" style="margin-bottom: 12px">
        令牌明文<strong>只在签发时显示一次</strong>，之后只能看到前缀。忘了就吊销重签。
      </el-alert>

      <div class="hrp-toolbar">
        <el-button :loading="loading" @click="load"><el-icon><Refresh /></el-icon>刷新</el-button>
        <span class="hrp-muted">共 {{ list.length }} 枚令牌</span>
      </div>

      <el-table v-loading="loading" :data="list" border stripe empty-text="该项目还没有令牌">
        <el-table-column prop="id" label="ID" width="70" />
        <el-table-column prop="name" label="名称" min-width="140" show-overflow-tooltip />
        <el-table-column label="前缀" width="190">
          <template #default="{ row }"><span class="hrp-mono">{{ row.prefix }}</span></template>
        </el-table-column>
        <el-table-column prop="scope" label="权限" min-width="180" />
        <el-table-column label="过期" width="160">
          <template #default="{ row }">
            <el-tag v-if="row.expired" size="small" type="danger" effect="plain">已过期</el-tag>
            <span v-else class="hrp-muted">{{ row.expire_at || '不过期' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="最近使用" width="160">
          <template #default="{ row }">
            <span :class="row.last_used_at ? '' : 'hrp-muted'">{{ row.last_used_at || '从未使用' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button link type="danger" @click="revoke(row)">吊销</el-button>
          </template>
        </el-table-column>
      </el-table>

      <el-divider content-position="left">流水线里怎么用</el-divider>
      <pre class="hrp-mono example">{{ curlExample }}</pre>
    </el-card>

    <el-dialog v-model="dialogVisible" title="签发 CI 令牌" width="640px" :before-close="beforeClose"
      destroy-on-close>
      <template v-if="!issued">
        <el-form ref="formRef" :model="form" label-width="110px">
          <el-form-item label="名称" prop="name"
            :rules="[{ required: true, message: '请输入令牌名称', trigger: 'blur' }]">
            <el-input v-model="form.name" placeholder="例如 GitHub Actions · 每日回归" />
            <div class="hrp-muted tip">同一项目内不可重名；名字只是给你自己认的</div>
          </el-form-item>
          <el-form-item label="有效期">
            <el-input-number v-model="form.ttl_days" :min="0" :max="3650" />
            <span class="hrp-muted tip">天；0 表示不过期</span>
          </el-form-item>
          <el-form-item label="权限">
            <div class="hrp-muted">run:trigger, run:read（触发执行 + 读取结果，M2 只有这两项）</div>
          </el-form-item>
        </el-form>
      </template>

      <template v-else>
        <el-alert type="success" show-icon :closable="false" title="令牌已签发">
          把它存进 CI 的 secret（例如 <code>HRP_TOKEN</code>）。<strong>关闭本窗口后不会再显示。</strong>
        </el-alert>
        <div class="secret">
          <code class="hrp-mono">{{ issued.token }}</code>
          <el-button size="small" :type="copied ? 'success' : 'primary'" @click="copyToken">
            {{ copied ? '已复制' : '复制' }}
          </el-button>
        </div>
        <div class="hrp-muted">前缀 {{ issued.prefix }}（列表里只显示这个）</div>
      </template>

      <template #footer>
        <el-button @click="beforeClose">{{ issued ? '关闭' : '取消' }}</el-button>
        <el-button v-if="!issued" type="primary" :loading="submitting" @click="submit">签发</el-button>
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

.secret {
  display: flex;
  gap: 8px;
  align-items: center;
  margin: 12px 0;
  padding: 10px;
  background: var(--el-fill-color-light);
  border-radius: 4px;
  word-break: break-all;
}

.example {
  margin: 0;
  padding: 12px;
  background: var(--el-fill-color-light);
  border-radius: 4px;
  font-size: 12px;
  line-height: 1.7;
  overflow-x: auto;
}
</style>
