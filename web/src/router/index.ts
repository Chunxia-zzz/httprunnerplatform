import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

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
        path: 'runs',
        name: 'runs',
        component: () => import('@/views/RunListView.vue'),
        meta: { title: '执行记录' },
      },
      {
        path: 'runs/:id(\\d+)',
        name: 'run-detail',
        component: () => import('@/views/RunDetailView.vue'),
        meta: { title: '执行详情' },
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
  return true
})

router.afterEach((to) => {
  const title = to.meta.title as string | undefined
  document.title = title ? `${title} · 接口自动化测试平台` : '接口自动化测试平台'
})

export default router
