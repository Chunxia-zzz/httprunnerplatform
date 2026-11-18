<script setup lang="ts">
/**
 * 通知设置（F2）。
 *
 * 四个渠道都是「群机器人 webhook」这一个形态，区别只在 payload 与签名：
 * 企微 markdown / 钉钉 markdown+签名 / 飞书 text+签名 / 通用 JSON+HMAC。
 * 页面提供「发送测试」，配完就知道通不通，不用等一次真实执行。
 */
import { onMounted, reactive, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'

import { notifyApi, type NotifyConfig, type NotifyLog } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { useProjectStore } from '@/stores/project'
import { notifyError, notifyOk } from '@/utils/error'

const projects = useProjectStore()
const auth = useAuthStore()

const loading = ref(false)
const configs = ref<NotifyConfig[]>([])
const logs = ref<NotifyLog[]>([])
const testingId = ref(0)
const lastTest = ref<{ id: number; ok: boolean; message: string } | null>(null)

const dialogVisible = ref(false)
const submitting = ref(false)
const editingId = ref(0)
const form = reactive({
  name: '',
  channel: 'wecom',
  webhook_url: '',
  secret: '',
  on_success: false,
})

const CHANNELS: { value: string; label: string; hint: string }[] = [
  { value: 'wecom', label: '企业微信', hint: '群机器人 webhook（qyapi.weixin.qq.com/…/send?key=）' },
  { value: 'dingtalk', label: '钉钉', hint: '群机器人 webhook；加签密钥可选' },
  { value: 'feishu', label: '飞书', hint: '群机器人 webhook；签名校验可选' },
  { value: 'webhook', label: '通用 Webhook', hint: '接收 JSON，可选 HMAC 签名头' },
]

function channelLabel(c: string) {
  return CHANNELS.find((x) => x.value === c)?.label ?? c
}

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    const [cs, ls] = await Promise.all([
      notifyApi.listNotify(projects.currentId),
      notifyApi.listNotifyLogs(projects.currentId, 50),
    ])
    configs.value = cs ?? []
    logs.value = ls ?? []
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = 0
  form.name = ''
  form.channel = 'wecom'
  form.webhook_url = ''
  form.secret = ''
  form.on_success = false
  dialogVisible.value = true
}

function openEdit(row: NotifyConfig) {
  editingId.value = row.id
  form.name = row.name
  form.channel = row.channel
  form.webhook_url = row.webhook_url
  // 非管理员看不到明文；留空表示保持原值（后端对空串按"不改"处理？——这里显式用 ***）
  form.secret = row.has_secret ? '***' : ''
  form.on_success = row.on_success
  dialogVisible.value = true
}

async function submit() {
  if (!projects.currentId) return
  submitting.value = true
  try {
    const payload = {
      name: form.name.trim(),
      channel: form.channel,
      webhook_url: form.webhook_url.trim(),
      secret: form.secret,
      on_success: form.on_success,
    }
    if (editingId.value) await notifyApi.updateNotify(editingId.value, payload)
    else await notifyApi.createNotify(projects.currentId, payload)
    notifyOk('已保存')
    dialogVisible.value = false
    await load()
  } catch (e) {
    notifyError(e)
  } finally {
    submitting.value = false
  }
}

async function sendTest(row: NotifyConfig) {
  testingId.value = row.id
  lastTest.value = null
  try {
    const r = await notifyApi.testNotify(row.id)
    lastTest.value = {
      id: row.id,
      ok: r.ok,
      message: r.ok
        ? `发送成功（HTTP ${r.http_status}，尝试 ${r.attempts} 次）`
        : `发送失败（HTTP ${r.http_status || '—'}，尝试 ${r.attempts} 次）：${r.error || r.response}`,
    }
    if (r.ok) notifyOk('测试消息已发送')
    else notifyError(new Error(lastTest.value.message))
    await load()
  } catch (e) {
    notifyError(e)
  } finally {
    testingId.value = 0
  }
}

async function remove(row: NotifyConfig) {
  try {
    await ElMessageBox.confirm(`删除后不会再推送「${row.name}」。确定删除？`, '删除通知', { type: 'warning' })
  } catch {
    return
  }
  try {
    await notifyApi.deleteNotify(row.id)
    notifyOk('已删除')
    await load()
  } catch (e) {
    notifyError(e)
  }
}

onMounted(load)
watch(() => projects.currentId, load)
</script>

<template>
  <div class="page">
    <div class="page__head">
      <div>
        <h2 class="page__title">通知设置</h2>
        <p class="hrp-muted page__desc">
          执行进入终态时推送到群机器人：失败/错误/终止<strong>总是推送</strong>，成功由「成功也通知」控制。
          webhook 地址里通常含访问密钥，非管理员看到的是掩码。
        </p>
      </div>
      <el-button type="primary" :disabled="!auth.isAdmin" @click="openCreate">新建通知</el-button>
    </div>

    <el-table v-loading="loading" :data="configs" border stripe class="card">
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column label="渠道" width="120">
        <template #default="{ row }">
          <el-tag size="small" effect="plain">{{ channelLabel(row.channel) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="Webhook" min-width="240" show-overflow-tooltip>
        <template #default="{ row }"><span class="hrp-mono">{{ row.webhook_url }}</span></template>
      </el-table-column>
      <el-table-column label="密钥" width="80">
        <template #default="{ row }">{{ row.has_secret ? '已配置' : '—' }}</template>
      </el-table-column>
      <el-table-column label="成功也通知" width="110">
        <template #default="{ row }">
          <el-tag size="small" :type="row.on_success ? 'success' : 'info'" effect="plain">
            {{ row.on_success ? '是' : '仅失败' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="190" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" :loading="testingId === row.id" :disabled="!auth.isAdmin" @click="sendTest(row as NotifyConfig)">
            发送测试
          </el-button>
          <el-button link :disabled="!auth.isAdmin" @click="openEdit(row as NotifyConfig)">编辑</el-button>
          <el-button link type="danger" :disabled="!auth.isAdmin" @click="remove(row as NotifyConfig)">删除</el-button>
        </template>
      </el-table-column>
      <template #empty>
        <p class="hrp-muted">还没有通知配置。新建一个群机器人，执行结束就能收到消息。</p>
      </template>
    </el-table>

    <el-alert
      v-if="lastTest"
      :type="lastTest.ok ? 'success' : 'error'"
      :closable="false"
      show-icon
      class="test-result"
      :title="lastTest.message"
    />

    <el-card shadow="never" class="card">
      <template #header>
        <div class="card__head">
          <span>最近投递（{{ logs.length }}）</span>
          <el-button size="small" @click="load">刷新</el-button>
        </div>
      </template>
      <el-table :data="logs" border stripe size="small">
        <el-table-column label="时间" width="170">
          <template #default="{ row }">{{ row.created_at?.slice(0, 19).replace('T', ' ') }}</template>
        </el-table-column>
        <el-table-column label="渠道" width="110">
          <template #default="{ row }">{{ channelLabel(row.channel) }}</template>
        </el-table-column>
        <el-table-column label="执行" width="90">
          <template #default="{ row }">{{ row.run_id ? `#${row.run_id}` : '测试' }}</template>
        </el-table-column>
        <el-table-column label="结果" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.status === 'success' ? 'success' : 'danger'" effect="plain">
              {{ row.status === 'success' ? '成功' : '失败' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="http_status" label="HTTP" width="80" />
        <el-table-column prop="attempts" label="尝试" width="70" />
        <el-table-column label="响应/错误" min-width="260" show-overflow-tooltip>
          <template #default="{ row }">{{ row.error || row.response }}</template>
        </el-table-column>
        <template #empty>
          <p class="hrp-muted">还没有投递记录。点「发送测试」或跑一次执行就会出现在这里。</p>
        </template>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="editingId ? '编辑通知' : '新建通知'" width="560px">
      <el-form label-width="100px">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="如 测试群 / 值班群" />
        </el-form-item>
        <el-form-item label="渠道">
          <el-select v-model="form.channel" style="width: 200px">
            <el-option v-for="c in CHANNELS" :key="c.value" :label="c.label" :value="c.value" />
          </el-select>
          <div class="hrp-muted form-hint">{{ CHANNELS.find((c) => c.value === form.channel)?.hint }}</div>
        </el-form-item>
        <el-form-item label="Webhook">
          <el-input v-model="form.webhook_url" placeholder="https://…（群机器人完整地址）" class="hrp-mono" />
        </el-form-item>
        <el-form-item label="签名密钥">
          <el-input v-model="form.secret" placeholder="可选；钉钉/飞书机器人安全设置里的密钥" show-password />
          <div class="hrp-muted form-hint">
            编辑时留 <code>***</code> 表示不修改；清空表示移除密钥。
          </div>
        </el-form-item>
        <el-form-item label="成功也通知">
          <el-switch v-model="form.on_success" active-text="成功也推送" inactive-text="仅失败推送" />
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
.page__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 14px;
}
.page__title {
  margin: 0;
  font-size: 18px;
}
.page__desc {
  margin: 4px 0 0;
  font-size: 12px;
  max-width: 760px;
}
.card {
  margin-bottom: 14px;
}
.card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.test-result {
  margin-bottom: 14px;
}
.form-hint {
  font-size: 12px;
  margin-top: 4px;
}
</style>
