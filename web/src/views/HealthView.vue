<script setup lang="ts">
/**
 * 资产健康度（F12）。
 *
 * 只读体检：给清单与建议，不提供任何"一键删除"——删用例/接口的风险远大于收益。
 * 评分口径直接展示在页面上（每一类扣多少），避免"分数怎么来的"说不清。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { healthApi, type HealthCategory, type HealthItem, type HealthReport } from '@/api'
import { useProjectStore } from '@/stores/project'
import { notifyError } from '@/utils/error'

const router = useRouter()
const projects = useProjectStore()

const loading = ref(false)
const report = ref<HealthReport | null>(null)
const days = ref(30)
const openCats = ref<string[]>([])

const scoreColor = computed(() => {
  const s = report.value?.score ?? 100
  if (s >= 90) return '#18a058'
  if (s >= 70) return '#f0a020'
  return '#d03050'
})

const SEVERITY_LABEL: Record<string, string> = { high: '高', medium: '中', low: '低' }
const SEVERITY_TYPE: Record<string, 'danger' | 'warning' | 'info'> = {
  high: 'danger',
  medium: 'warning',
  low: 'info',
}

async function load() {
  if (!projects.currentId) return
  loading.value = true
  try {
    report.value = await healthApi.getHealth(projects.currentId, days.value)
    // 默认展开有内容的分类，空的收起
    openCats.value = report.value.categories.filter((c) => c.count > 0).map((c) => c.key)
  } catch (e) {
    notifyError(e)
  } finally {
    loading.value = false
  }
}

function openItem(item: { entity_type: string; entity_id: number }) {
  if (item.entity_type === 'case') router.push(`/cases/${item.entity_id}`)
  if (item.entity_type === 'api') router.push('/apis')
}

function catType(c: HealthCategory) {
  return SEVERITY_TYPE[c.severity] ?? 'info'
}

onMounted(load)
watch(() => projects.currentId, load)
</script>

<template>
  <div class="page" v-loading="loading">
    <div class="page__head">
      <div>
        <h2 class="page__title">资产健康度</h2>
        <p class="hrp-muted page__desc">
          找出僵尸资产：空壳用例、没有断言、重复用例、长期没跑、从未执行的用例，以及没被引用的接口定义。
          本页只给建议，不会自动删除任何东西。
        </p>
      </div>
      <div class="head__actions">
        <span class="hrp-muted">多久算僵尸</span>
        <el-input-number v-model="days" :min="1" :max="3650" controls-position="right" style="width: 120px" />
        <span class="hrp-muted">天</span>
        <el-button type="primary" :loading="loading" @click="load">重新体检</el-button>
      </div>
    </div>

    <template v-if="report">
      <div class="score-row">
        <el-card shadow="never" class="score-card">
          <div class="score" :style="{ color: scoreColor }">{{ report.score }}</div>
          <div class="hrp-muted score__label">健康分（满分 100）</div>
          <div class="hrp-muted score__at">生成于 {{ report.generated_at }}</div>
        </el-card>

        <el-card shadow="never" class="deduct-card">
          <template #header><span>扣分明细</span></template>
          <div v-if="!report.deductions.length" class="hrp-muted">
            没有发现问题，保持住。
          </div>
          <div v-for="d in report.deductions" :key="d.key" class="deduct">
            <el-tag size="small" :type="SEVERITY_TYPE[d.severity] ?? 'info'" effect="plain">
              {{ SEVERITY_LABEL[d.severity] ?? d.severity }}
            </el-tag>
            <span class="deduct__label">{{ d.label }}</span>
            <span class="hrp-muted">{{ d.count }} 项</span>
            <span class="deduct__penalty">-{{ d.penalty }} 分</span>
          </div>
        </el-card>
      </div>

      <el-collapse v-model="openCats">
        <el-collapse-item v-for="c in report.categories" :key="c.key" :name="c.key">
          <template #title>
            <div class="cat-title">
              <el-tag size="small" :type="catType(c)" effect="plain">
                {{ SEVERITY_LABEL[c.severity] ?? c.severity }}
              </el-tag>
              <span class="cat-title__label">{{ c.label }}</span>
              <span class="cat-title__count" :class="{ 'is-zero': c.count === 0 }">{{ c.count }}</span>
              <span class="hrp-muted cat-title__hint">{{ c.hint }}</span>
            </div>
          </template>

          <el-table v-if="c.items.length" :data="c.items" size="small" border>
            <el-table-column label="标识" width="180">
              <template #default="{ row }"><span class="hrp-mono">{{ row.key }}</span></template>
            </el-table-column>
            <el-table-column prop="name" label="名称" min-width="160" show-overflow-tooltip />
            <el-table-column prop="detail" label="说明" min-width="280" show-overflow-tooltip />
            <el-table-column label="打开" width="80">
              <template #default="{ row }">
                <el-button link type="primary" @click="openItem(row as HealthItem)">查看</el-button>
              </template>
            </el-table-column>
          </el-table>
          <p v-else class="hrp-muted empty">这一类没有问题。</p>
        </el-collapse-item>
      </el-collapse>
    </template>
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
  max-width: 720px;
}
.head__actions {
  display: flex;
  align-items: center;
  gap: 8px;
  white-space: nowrap;
}
.score-row {
  display: flex;
  gap: 14px;
  margin-bottom: 14px;
  align-items: stretch;
}
.score-card {
  width: 240px;
  text-align: center;
}
.score {
  font-size: 56px;
  font-weight: 700;
  line-height: 1.1;
}
.score__label {
  font-size: 12px;
  margin-top: 4px;
}
.score__at {
  font-size: 12px;
  margin-top: 8px;
}
.deduct-card {
  flex: 1;
}
.deduct {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 4px 0;
  font-size: 13px;
}
.deduct__label {
  min-width: 140px;
}
.deduct__penalty {
  margin-left: auto;
  color: #d03050;
  font-weight: 600;
}
.cat-title {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding-right: 12px;
}
.cat-title__label {
  font-weight: 600;
}
.cat-title__count {
  min-width: 28px;
  text-align: center;
  font-weight: 600;
  color: #d03050;
}
.cat-title__count.is-zero {
  color: var(--el-text-color-placeholder);
}
.cat-title__hint {
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.empty {
  margin: 6px 0;
  font-size: 12px;
}
</style>
