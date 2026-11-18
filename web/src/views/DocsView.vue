<script setup lang="ts">
/**
 * 接口文档（F6）：从接口库与用例实时生成，生成只读分享链接给非平台用户。
 *
 * 两个约定：
 *   1. 预览用 iframe 直接渲染后端 HTML —— 与分享链接看到的是**同一份产物**，
 *      不会出现「平台内一个样、发出去另一个样」。
 *   2. 分享 token 只存哈希，明文仅在创建时展示一次，列表只显示前缀。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessageBox } from 'element-plus'

import { docsApi, type APIDoc, type ShareLink } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError, notifyOk } from '@/utils/error'

const projects = useProjectStore()

const loading = ref(false)
const doc = ref<APIDoc | null>(null)
const shares = ref<ShareLink[]>([])
const frameUrl = ref('')

const dialogVisible = ref(false)
const creating = ref(false)
const form = reactive({ name: '', days: 30 })
const issuedUrl = ref('')

const coverage = computed(() => {
  if (!doc.value || doc.value.total_apis === 0) return '—'
  return `${doc.value.covered_apis}/${doc.value.total_apis}`
})

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    const [d, s] = await Promise.all([
      docsApi.getDoc(projects.currentId),
      docsApi.listShares(projects.currentId),
    ])
    doc.value = d
    shares.value = s ?? []
    // 加时间戳避免浏览器缓存旧文档
    frameUrl.value = `${docsApi.docHtmlUrl(projects.currentId)}?t=${Date.now()}`
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  form.name = ''
  form.days = 30
  issuedUrl.value = ''
  dialogVisible.value = true
}

async function create() {
  if (!projects.currentId) return
  creating.value = true
  try {
    const r = await docsApi.createShare(projects.currentId, {
      name: form.name.trim(),
      expires_in_days: form.days,
    })
    issuedUrl.value = `${window.location.origin}${r.path}`
    notifyOk('已生成（链接只显示这一次，请立即复制）')
    await load()
  } catch (e) {
    notifyError(e)
  } finally {
    creating.value = false
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    notifyOk('已复制')
  } catch {
    notifyError(new Error('浏览器拒绝了剪贴板访问，请手动复制'))
  }
}

async function revoke(row: ShareLink) {
  if (!projects.currentId) return
  try {
    await ElMessageBox.confirm(
      `吊销后该链接立即失效，已发送出去的链接将无法打开。确定吊销「${row.name || '未命名'}」？`,
      '吊销确认',
      { type: 'warning' },
    )
  } catch {
    return
  }
  try {
    await docsApi.revokeShare(projects.currentId, row.id)
    notifyOk('已吊销')
    await load()
  } catch (e) {
    notifyError(e)
  }
}

function openFrame() {
  window.open(frameUrl.value, '_blank')
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page__head">
      <div>
        <h2 class="page__title">接口文档</h2>
        <p class="hrp-muted page__desc">
          从接口库与用例实时生成；接口覆盖 <b>{{ coverage }}</b>。只用 GET 打开，页面没有任何写操作。
        </p>
      </div>
      <div class="page__actions">
        <el-button @click="openFrame">新窗口打开</el-button>
        <el-button type="primary" :disabled="!doc?.total_apis" @click="openCreate">生成分享链接</el-button>
      </div>
    </div>

    <div v-loading="loading" class="frame-wrap">
      <iframe v-if="frameUrl" :src="frameUrl" class="frame" title="接口文档预览" />
      <p v-else class="hrp-muted">选择一个项目后加载文档。</p>
    </div>

    <el-card shadow="never" class="card">
      <template #header>
        <div class="card__head">
          <span>分享链接（{{ shares.length }}）</span>
          <span class="hrp-muted">token 只存哈希，明文仅在生成时显示一次</span>
        </div>
      </template>
      <el-table :data="shares" border stripe size="small">
        <el-table-column prop="name" label="备注" min-width="140">
          <template #default="{ row }">{{ row.name || '未命名' }}</template>
        </el-table-column>
        <el-table-column prop="prefix" label="令牌前缀" width="110">
          <template #default="{ row }"><span class="hrp-mono">{{ row.prefix }}</span></template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.status === 'active' ? 'success' : 'info'" effect="plain">
              {{ row.status === 'active' ? '有效' : '已过期' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="过期时间" width="180">
          <template #default="{ row }">{{ row.expires_at ? row.expires_at.slice(0, 19) : '永久' }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="180">
          <template #default="{ row }">{{ row.created_at?.slice(0, 19) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100" fixed="right">
          <template #default="{ row }">
            <el-button link type="danger" @click="revoke(row as ShareLink)">吊销</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <p class="hrp-muted">还没有分享链接。生成后把链接发给非平台用户（前端/上下游/领导）即可查看。</p>
        </template>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" title="生成只读分享链接" width="520px">
      <el-form label-width="90px">
        <el-form-item label="备注">
          <el-input v-model="form.name" placeholder="如 给前端联调 / 给 QA 验收" />
        </el-form-item>
        <el-form-item label="有效期">
          <el-select v-model="form.days" style="width: 180px">
            <el-option :value="7" label="7 天" />
            <el-option :value="30" label="30 天" />
            <el-option :value="90" label="90 天" />
            <el-option :value="0" label="永久有效" />
          </el-select>
        </el-form-item>
      </el-form>
      <el-alert type="warning" :closable="false" show-icon title="链接等同于只读访问凭证，谁拿到都能看">
        不要发到公开渠道；需要作废时到列表里「吊销」。
      </el-alert>
      <div v-if="issuedUrl" class="issued">
        <el-input :model-value="issuedUrl" readonly class="hrp-mono" />
        <el-button type="primary" @click="copy(issuedUrl)">复制</el-button>
      </div>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ issuedUrl ? '关闭' : '取消' }}</el-button>
        <el-button v-if="!issuedUrl" type="primary" :loading="creating" @click="create">生成</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
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
.page__actions {
  display: flex;
  gap: 8px;
}
.frame-wrap {
  background: #fff;
  border: 1px solid var(--el-border-color-light);
  border-radius: 8px;
  overflow: hidden;
  margin-bottom: 16px;
  min-height: 420px;
}
.frame {
  width: 100%;
  height: 620px;
  border: 0;
  display: block;
}
.card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.issued {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}
</style>
