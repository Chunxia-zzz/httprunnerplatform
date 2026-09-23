<script setup lang="ts">
/**
 * 用例导入对话框（M4）：HAR / Postman / curl → 平台用例。
 *
 * 两段式流程与后端一致：先预览（不落库）看到「将创建哪些用例、各含几步」，
 * 再点确认提交。格式默认 auto（后端内容探测），识别不了再让用户显式指定。
 */
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'

import { importApi, type ImportedCase } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError } from '@/utils/error'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: boolean): void
  (e: 'imported', count: number): void
}>()

const projects = useProjectStore()
const projectId = computed(() => projects.currentId)

const FORMAT_OPTIONS = [
  { label: '自动识别', value: 'auto' },
  { label: 'HAR 抓包', value: 'har' },
  { label: 'Postman 集合', value: 'postman' },
  { label: 'curl 命令', value: 'curl' },
]

const visible = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const fileInput = ref<HTMLInputElement | null>(null)
const file = ref<File | null>(null)
const format = ref('auto')
const detected = ref('')
const cases = ref<ImportedCase[]>([])
const previewing = ref(false)
const committing = ref(false)

watch(visible, (v) => {
  if (v) reset()
})

function reset() {
  file.value = null
  format.value = 'auto'
  detected.value = ''
  cases.value = []
}

function onPickFile() {
  fileInput.value?.click()
}

function onFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  file.value = input.files?.[0] ?? null
  cases.value = []
  detected.value = ''
}

/** 预览：转换但不落库。 */
async function onPreview() {
  if (!file.value) {
    ElMessage.warning('请先选择要导入的文件')
    return
  }
  previewing.value = true
  try {
    const result = await importApi.importPreview(projectId.value, file.value, format.value)
    detected.value = result.detected
    cases.value = result.cases
  } catch (e) {
    notifyError(e, '转换失败')
  } finally {
    previewing.value = false
  }
}

/** 提交：落库并报告结果。 */
async function onCommit() {
  if (!file.value || cases.value.length === 0) return
  committing.value = true
  try {
    const result = await importApi.importCommit(projectId.value, file.value, format.value)
    if (result.created.length > 0) {
      ElMessage.success(`成功导入 ${result.created.length} 条用例`)
    }
    for (const f of result.failed) {
      ElMessage.error(`「${f.name}」导入失败：${f.reason}`)
    }
    emit('imported', result.created.length)
    visible.value = false
  } catch (e) {
    notifyError(e, '导入失败')
  } finally {
    committing.value = false
  }
}
</script>

<template>
  <el-dialog v-model="visible" title="导入用例（HAR / Postman / curl）" width="640px" destroy-on-close>
    <el-alert type="info" show-icon :closable="false" class="imp__tip">
      <template #title>转换在服务端由 hrp convert 完成，导入的用例可在编辑器里继续调整</template>
      HAR 建议先裁剪出目标请求（浏览器 DevTools → 右键请求 → Save all as HAR）。
    </el-alert>

    <div class="imp__row">
      <el-select v-model="format" style="width: 150px">
        <el-option v-for="o in FORMAT_OPTIONS" :key="o.value" :label="o.label" :value="o.value" />
      </el-select>
      <el-button @click="onPickFile">
        <el-icon><Paperclip /></el-icon>
        {{ file ? file.name : '选择文件' }}
      </el-button>
      <input ref="fileInput" type="file" accept=".har,.json,.txt" style="display: none" @change="onFileChange" />
      <el-button type="primary" :loading="previewing" :disabled="!file" @click="onPreview">转换预览</el-button>
      <el-tag v-if="detected" size="small" type="success" effect="plain">识别为 {{ detected }}</el-tag>
    </div>

    <el-table v-if="cases.length" :data="cases" border size="small" class="imp__table">
      <el-table-column prop="name" label="将创建用例" min-width="150" />
      <el-table-column prop="code" label="标识" width="140">
        <template #default="{ row }"><span class="hrp-mono">{{ row.code }}</span></template>
      </el-table-column>
      <el-table-column label="步骤数" width="80">
        <template #default="{ row }">{{ row.step_count }}</template>
      </el-table-column>
      <el-table-column label="请求摘要" min-width="220">
        <template #default="{ row }">
          <div v-for="(s, i) in row.summary" :key="i" class="hrp-mono imp__summary">{{ s }}</div>
        </template>
      </el-table-column>
    </el-table>
    <div v-else class="hrp-empty-hint">选择文件后点「转换预览」，确认无误再导入</div>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button
        type="primary"
        :loading="committing"
        :disabled="cases.length === 0"
        @click="onCommit"
      >
        确认导入（{{ cases.length }} 条）
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.imp__tip {
  margin-bottom: 12px;
}
.imp__row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}
.imp__table {
  margin-top: 4px;
}
.imp__summary {
  line-height: 1.6;
}
</style>
