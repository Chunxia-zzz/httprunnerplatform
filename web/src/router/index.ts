import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { ElMessage } from 'element-plus'

import { useAuthStore } from '@/stores/auth'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true, title: '登录' },
  },
  {
    path: '/',
    component: () => import('@/layouts/MainLayout.vue'),
    children: [
      { path: '', redirect: { name: 'cases' } },
      {
        path: 'projects',
        name: 'projects',
        component: () => import('@/views/ProjectListView.vue'),
        meta: { title: '项目管理' },
      },
      {
        path: 'environments',
        name: 'environments',
        component: () => import('@/views/EnvironmentListView.vue'),
        meta: { title: '环境管理' },
      },
      {
        // 参数化数据集：用例的数据驱动来源。挂在用例管理之后，
        // 因为它是「用某个用例会引用的资源」，而不是独立执行入口。
        path: 'datasets',
        name: 'datasets',
        component: () => import('@/views/ParamListView.vue'),
        meta: { title: '参数化数据集' },
      },
      {
        path: 'cases',
        name: 'cases',
        component: () => import('@/views/CaseListView.vue'),
        meta: { title: '用例管理' },
      },
      {
        // 新建与编辑共用同一个编辑器组件：字段与校验完全一致，
        // 分成两个组件必然出现"新建能填的字段编辑页没有"这类漂移。
        path: 'cases/new',
        name: 'case-new',
        component: () => import('@/views/CaseEditorView.vue'),
        meta: { title: '新建用例' },
      },
      {
        path: 'cases/:id(\\d+)/edit',
        name: 'case-edit',
        component: () => import('@/views/CaseEditorView.vue'),
        meta: { title: '编辑用例' },
      },
      {
        // ⭐ 编排层三个页面按「用例 → 用例集 → 计划」的顺序排，
        // 与执行时的组合顺序一致：计划挂用例集、用例集挂用例。
        path: 'suites',
        name: 'suites',
        component: () => import('@/views/SuiteListView.vue'),
        meta: { title: '用例集' },
      },
      {
        path: 'plans',
        name: 'plans',
        component: () => import('@/views/PlanListView.vue'),
        meta: { title: '测试计划' },
      },
      {
        path: 'tokens',
        name: 'tokens',
        component: () => import('@/views/TokenListView.vue'),
        meta: { title: 'CI 令牌' },
      },
      {
        path: 'runs',
        name: 'runs',
        component: () => import('@/views/RunListView.vue'),
        meta: { title: '执行记录' },
      },
      {
        // 统计看板（M4）：读执行历史，挂在执行记录之后。
        path: 'stats',
        name: 'stats',
        component: () => import('@/views/StatsDashboardView.vue'),
        meta: { title: '统计看板' },
      },
      {
        path: 'runs/:id(\\d+)',
        name: 'run-detail',
        component: () => import('@/views/RunDetailView.vue'),
        meta: { title: '执行详情' },
      },
      {
        // 账号管理：后端所有 /users 端点都挂了 RequireAdmin，
        // 这里加 meta.admin 只为把入口藏起来并给出可读提示 ——
        // 直接输 URL 时兜住，而不是让用户看到一屏 40300。
        path: 'users',
        name: 'users',
        component: () => import('@/views/UserListView.vue'),
        meta: { title: '账号管理', admin: true },
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: { name: 'cases' } },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

/**
 * 全局守卫。
 *
 * 冷启动时先探测一次会话（`resolve()`），否则刷新页面会被
 * 立刻踢回登录页 —— 因为 pinia 里的用户是空的，而 Cookie 其实还在。
 */
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.resolved) {
    await auth.resolve()
  }

  if (to.meta.public) {
    return auth.isLoggedIn ? { name: 'cases' } : true
  }
  if (!auth.isLoggedIn) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  // 管理员专属页面。这里只是"不给出点不动的入口"，
  // 真实判定始终在后端的 RequireAdmin 上 —— 前端隐藏永远不是权限控制。
  if (to.meta.admin && !auth.isAdmin) {
    ElMessage.warning('该页面仅管理员可访问')
    return { name: 'cases' }
  }
  return true
})

router.afterEach((to) => {
  const title = to.meta.title as string | undefined
  document.title = title ? `${title} · 接口自动化测试平台` : '接口自动化测试平台'
})

export default router
