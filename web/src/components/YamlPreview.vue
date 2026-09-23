<script setup lang="ts">
/**
 * 编译后 YAML 预览 + 源码可编辑（M3 ③-c 4.2）。
 *
 * 默认只读；点「编辑源码」后 YAML 主体变成可写 textarea（行号保留），
 * 点「保存源码」把内容发到后端反解析回结构化数据落库。
 *
 * 反解析失败的闭环：后端返回 code=50003、错误信息带「第 N 行」前缀，
 * 这里解析出行号并用红色高亮标记，让用户直接看到是哪一行改错了 ——
 * 而不是"保存失败"四个字。
 *
 * 契约硬要求：预览的 YAML 与执行时写盘的内容**逐字节一致**（后端共用
 * 同一条 compiler.Render 渲染链路），所以它可以直接当作"引擎到底看到了什么"。
 */
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'

import { ApiError, caseApi, type YAMLPreview } from '@/api'

const props = defineProps<{
  preview: YAMLPreview | null
  loading?: boolean
  error?: string
  /** 用例 ID，编辑源码保存时需要（null = 尚未保存，无法编辑源码） */
  caseId?: number | null
}>()

const emit = defineEmits<{
  /** 源码保存成功后触发，父组件据此刷新预览 + 表单 */
  (e: 'saved'): void
}>()

const editing = ref(false)
const draft = ref('')
const saving = ref(false)
/** 反解析报错行（从「第 N 行」解析），0 = 无错误 */
const errorLine = ref(0)
const saveError = ref('')

const lines = computed(() => {
  const text = editing.value ? draft.value : (props.preview?.yaml ?? '')
  return text.replace(/\n$/, '').split('\n')
})

const envLines = computed(() => {
  const text = props.preview?.env ?? ''
  return text.replace(/\n$/, '').split('\n').filter((l) => l !== '')
})

// 预览内容变化（如切环境重新编译）时，若不在编辑态就同步草稿
watch(
  () => props.preview?.yaml,
  (v) => {
    if (!editing.value && v != null) draft.value = v
  },
)

function startEdit() {
  if (props.preview?.yaml != null) draft.value = props.preview.yaml
  errorLine.value = 0
  saveError.value = ''
  editing.value = true
}

function cancelEdit() {
  editing.value = false
  errorLine.value = 0
  saveError.value = ''
}

/** 从后端错误信息里解析「第 N 行」。 */
function parseLine(msg: string): number {
  const m = msg.match(/第\s*(\d+)\s*行/)
  return m ? Number(m[1]) : 0
}

async function save() {
  if (props.caseId == null) {
    ElMessage.warning('用例尚未保存，无法保存源码')
    return
  }
  saving.value = true
  saveError.value = ''
  errorLine.value = 0
  try {
    await caseApi.saveCaseYaml(props.caseId, draft.value)
    ElMessage.success('源码已保存，结构化数据已同步')
    editing.value = false
    emit('saved')
  } catch (e) {
    const msg = e instanceof ApiError ? e.message : '保存源码失败'
    errorLine.value = parseLine(msg)
    saveError.value = msg
  } finally {
    saving.value = false
  }
}

async function copy(text: string, label: string) {
  if (!text) return
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success(`${label}已复制`)
  } catch {
    ElMessage.warning('浏览器拒绝了剪贴板访问，请手动选择复制')
  }
}
</script>

<template>
  <div v-loading="loading" class="yaml">
    <el-alert v-if="error && !editing" type="error" show-icon :closable="false" :title="error" />

    <template v-else-if="preview">
      <div class="yaml__meta">
        <span class="hrp-mono">{{ preview.filename }}</span>
        <div class="yaml__btns">
          <template v-if="!editing">
            <el-button size="small" link type="primary" @click="copy(preview.yaml, 'YAML')">复制 YAML</el-button>
            <el-button size="small" link type="primary" @click="copy(preview.env, '.env')">复制 .env</el-button>
            <el-button size="small" type="primary" plain :disabled="caseId == null" @click="startEdit">编辑源码</el-button>
          </template>
          <template v-else>
            <el-button size="small" @click="cancelEdit">取消</el-button>
            <el-button size="small" type="primary" :loading="saving" @click="save">保存源码</el-button>
          </template>
        </div>
      </div>

      <el-alert
        v-if="editing && saveError"
        type="error"
        show-icon
        :closable="false"
        class="yaml__saveerr"
        :title="saveError"
      >
        <template #default>
          {{ errorLine > 0 ? `报错在第 ${errorLine} 行（见左侧行号高亮）。` : '' }}反解析失败意味着这段 YAML 无法落库，
          请修正后再保存 —— 平台不会静默丢弃你改不出来的内容。
        </template>
      </el-alert>

      <el-collapse v-if="!editing" class="yaml__env">
        <el-collapse-item :title="`环境 .env（${envLines.length} 行）`" name="env">
          <pre class="hrp-pre">{{ preview.env }}</pre>
          <div class="hrp-muted yaml__note">
            环境变量渲染自「环境管理」，其中 <code>base_url</code> 用于拼接用例里的相对路径。
          </div>
        </el-collapse-item>
      </el-collapse>

      <div class="yaml__code">
        <div class="yaml__gutter">
          <span
            v-for="(_, i) in lines"
            :key="i"
            :class="{ 'is-errline': editing && errorLine === i + 1 }"
          >
            {{ i + 1 }}
          </span>
        </div>
        <textarea
          v-if="editing"
          v-model="draft"
          class="hrp-pre yaml__editor"
          spellcheck="false"
          :rows="Math.max(lines.length, 20)"
        />
        <pre v-else class="hrp-pre hrp-pre--tall yaml__body">{{ lines.join('\n') }}</pre>
      </div>
    </template>

    <div v-else class="hrp-empty-hint">
      还没有可预览的内容。<br />
      用例需要先保存（拿到 ID）才能编译 —— 编译是"DB → YAML"单向的，未落库的内容无法编译。
    </div>
  </div>
</template>

<style scoped>
.yaml__meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-bottom: 8px;
}

.yaml__btns {
  flex: none;
  display: flex;
  align-items: center;
}

.yaml__env {
  margin-bottom: 8px;
}

.yaml__saveerr {
  margin-bottom: 8px;
}

.yaml__note {
  font-size: 12px;
  line-height: 1.6;
}

.yaml__note code {
  font-family: var(--hrp-mono);
  background: #f2f3f5;
  border-radius: 3px;
  padding: 0 3px;
}

.yaml__code {
  display: flex;
  border: 1px solid var(--hrp-border);
  border-radius: 4px;
  overflow: hidden;
}

.yaml__gutter {
  flex: none;
  display: flex;
  flex-direction: column;
  padding: 10px 8px;
  background: #f5f7fa;
  color: var(--hrp-muted);
  font-family: var(--hrp-mono);
  font-size: 12px;
  line-height: 1.6;
  text-align: right;
  user-select: none;
  max-height: 640px;
  overflow: hidden;
}

.yaml__gutter .is-errline {
  background: var(--el-color-danger);
  color: #fff;
  border-radius: 2px;
  font-weight: 700;
}

.yaml__body {
  border: none;
  border-radius: 0;
  flex: 1;
  min-width: 0;
}

.yaml__editor {
  border: none;
  border-radius: 0;
  flex: 1;
  min-width: 0;
  resize: vertical;
  padding: 10px;
  margin: 0;
  font-family: var(--hrp-mono);
  font-size: 12px;
  line-height: 1.6;
  color: inherit;
  background: transparent;
  outline: none;
}
</style>
