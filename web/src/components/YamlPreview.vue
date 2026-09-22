<script setup lang="ts">
/**
 * 编译后 YAML 的只读预览。
 *
 * 契约硬要求：这份 YAML 与执行时写到盘上的文件**逐字节一致**
 * （后端共用同一条 compiler.Render 渲染链路），所以它可以直接当作
 * "引擎到底看到了什么"的答案 —— 不需要再让人去服务器上看文件。
 *
 * 行号是自己加的：YAML 的报错位置都以行号表述，没有行号等于没法对照。
 */
import { computed } from 'vue'
import { ElMessage } from 'element-plus'

import type { YAMLPreview } from '@/api'

const props = defineProps<{
  preview: YAMLPreview | null
  loading?: boolean
  error?: string
}>()

const lines = computed(() => {
  const text = props.preview?.yaml ?? ''
  // 末尾换行会产生一个"空行"，去掉它让行数与内容一致
  return text.replace(/\n$/, '').split('\n')
})

const envLines = computed(() => {
  const text = props.preview?.env ?? ''
  return text.replace(/\n$/, '').split('\n').filter((l) => l !== '')
})

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
    <el-alert v-if="error" type="error" show-icon :closable="false" :title="error" />

    <template v-else-if="preview">
      <div class="yaml__meta">
        <span class="hrp-mono">{{ preview.filename }}</span>
        <div class="yaml__btns">
          <el-button size="small" link type="primary" @click="copy(preview.yaml, 'YAML')">复制 YAML</el-button>
          <el-button size="small" link type="primary" @click="copy(preview.env, '.env')">复制 .env</el-button>
        </div>
      </div>

      <el-collapse class="yaml__env">
        <el-collapse-item :title="`环境 .env（${envLines.length} 行）`" name="env">
          <pre class="hrp-pre">{{ preview.env }}</pre>
          <div class="hrp-muted yaml__note">
            环境变量渲染自「环境管理」，其中 <code>base_url</code> 用于拼接用例里的相对路径。
          </div>
        </el-collapse-item>
      </el-collapse>

      <div class="yaml__code">
        <div class="yaml__gutter">
          <span v-for="(_, i) in lines" :key="i">{{ i + 1 }}</span>
        </div>
        <pre class="hrp-pre hrp-pre--tall yaml__body">{{ lines.join('\n') }}</pre>
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
}

.yaml__env {
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

.yaml__body {
  border: none;
  border-radius: 0;
  flex: 1;
  min-width: 0;
}
</style>
