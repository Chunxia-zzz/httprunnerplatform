<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'

import EngineStatusBanner from '@/components/EngineStatusBanner.vue'
import { useAuthStore } from '@/stores/auth'
import { useEngineStore } from '@/stores/engine'
import { useProjectStore } from '@/stores/project'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const engine = useEngineStore()
const projects = useProjectStore()

/** 侧边栏导航。用例是主战场，放第一个。 */
const navItems = [
  { name: 'cases', label: '用例管理', icon: 'Document' },
  { name: 'runs', label: '执行记录', icon: 'VideoPlay' },
  { name: 'environments', label: '环境管理', icon: 'Setting' },
  { name: 'projects', label: '项目管理', icon: 'Folder' },
]

const activeNav = computed(() => {
  const n = route.name as string | undefined
  if (!n) return ''
  // case-new / case-edit 属于「用例管理」，保持菜单高亮
  if (n.startsWith('case')) return 'cases'
  if (n === 'run-detail') return 'runs'
  return n
})

const currentProjectId = ref<number>(projects.currentId)

const engineOk = computed(() => engine.status?.available === true)
const engineText = computed(() => {
  if (engine.probeError) return '后端不可达'
  if (!engine.status) return '探测中…'
  if (!engine.status.available) return '引擎不可用'
  return `引擎 ${engine.status.version ?? '就绪'}`
})

onMounted(async () => {
  await projects.fetchList()
  currentProjectId.value = projects.currentId
})

function onProjectChange(id: number) {
  projects.setCurrent(id)
  // 切项目等价于换上下文：回到用例列表，避免停在别的项目的详情页上。
  // 用例/执行/环境页都监听 store 变化重新拉数据。
  void router.push({ name: 'cases' })
}

async function onLogout() {
  await auth.logout()
  ElMessage.success('已退出登录')
  void router.push({ name: 'login' })
}
</script>

<template>
  <el-container class="layout">
    <el-aside width="200px" class="layout__aside">
      <div class="layout__brand">
        <span class="layout__brand-mark">hrp</span>
        <span class="layout__brand-text">接口自动化平台</span>
      </div>
      <el-menu :default-active="activeNav" class="layout__menu" router>
        <el-menu-item v-for="item in navItems" :key="item.name" :index="item.name" :route="{ name: item.name }">
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
        </el-menu-item>
      </el-menu>
    </el-aside>

    <el-container>
      <el-header height="52px" class="layout__header">
        <div class="layout__header-left">
          <span class="layout__label">当前项目</span>
          <el-select
            v-model="currentProjectId"
            placeholder="请选择项目"
            size="small"
            style="width: 220px"
            :disabled="projects.list.length === 0"
            @change="onProjectChange"
          >
            <el-option v-for="p in projects.list" :key="p.id" :label="`${p.name}（${p.code}）`" :value="p.id" />
          </el-select>
          <el-button v-if="projects.list.length === 0" link type="primary" @click="router.push({ name: 'projects' })">
            去创建项目
          </el-button>
        </div>

        <div class="layout__header-right">
          <el-tooltip :content="engineText" placement="bottom">
            <el-tag :type="engineOk ? 'success' : 'danger'" size="small" effect="plain">
              <el-icon class="layout__dot">
                <component :is="engineOk ? 'CircleCheck' : 'WarningFilled'" />
              </el-icon>
              {{ engineText }}
            </el-tag>
          </el-tooltip>

          <el-dropdown @command="(c: string) => c === 'logout' && onLogout()">
            <span class="layout__user">
              <el-icon><User /></el-icon>
              {{ auth.displayName }}
              <el-tag v-if="auth.isAdmin" size="small" type="info" effect="plain">管理员</el-tag>
              <el-icon><ArrowDown /></el-icon>
            </span>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="logout">退出登录</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </el-header>

      <el-main class="layout__main">
        <EngineStatusBanner />
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<style scoped>
.layout {
  height: 100vh;
}

.layout__aside {
  background: #fff;
  border-right: 1px solid var(--hrp-border);
  display: flex;
  flex-direction: column;
}

.layout__brand {
  height: 52px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 16px;
  border-bottom: 1px solid var(--hrp-border);
}

.layout__brand-mark {
  font-family: var(--hrp-mono);
  font-weight: 700;
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
  border-radius: 4px;
  padding: 2px 6px;
  font-size: 12px;
}

.layout__brand-text {
  font-size: 13px;
  font-weight: 600;
}

.layout__menu {
  border-right: none;
}

.layout__header {
  background: #fff;
  border-bottom: 1px solid var(--hrp-border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.layout__header-left,
.layout__header-right {
  display: flex;
  align-items: center;
  gap: 10px;
}

.layout__label {
  color: var(--hrp-muted);
  font-size: 13px;
}

.layout__dot {
  margin-right: 2px;
  vertical-align: -2px;
}

.layout__user {
  display: flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  outline: none;
  font-size: 13px;
}

.layout__main {
  padding: 0;
  overflow: auto;
  background: var(--hrp-bg);
}
</style>
