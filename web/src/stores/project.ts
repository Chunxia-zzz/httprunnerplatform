import { defineStore } from 'pinia'

import { projectApi, type Project } from '@/api'

const STORAGE_KEY = 'hrp.currentProjectId'

interface State {
  list: Project[]
  total: number
  loading: boolean
  /** 当前选中的项目。用例/环境/执行列表都挂在它下面 */
  currentId: number
  loaded: boolean
}

/**
 * 项目上下文。
 *
 * 选中项目要**持久化**：刷新页面后停在"无项目"状态会让用户每次
 * 都要重新点一次；而这个选择本质上属于工作上下文，不是瞬态 UI 状态。
 */
export const useProjectStore = defineStore('project', {
  state: (): State => ({
    list: [],
    total: 0,
    loading: false,
    currentId: Number(localStorage.getItem(STORAGE_KEY) || 0),
    loaded: false,
  }),

  getters: {
    current: (s): Project | null => s.list.find((p) => p.id === s.currentId) ?? null,
    hasProject: (s) => s.currentId > 0,
  },

  actions: {
    async fetchList(keyword = '') {
      this.loading = true
      try {
        const data = await projectApi.listProjects({ keyword, page: 1, page_size: 200 })
        this.list = data.list
        this.total = data.total
        this.loaded = true

        // 选中的项目被删掉（或首次访问）时，回落到第一个项目。
        if (this.currentId && !this.list.some((p) => p.id === this.currentId)) {
          this.setCurrent(this.list[0]?.id ?? 0)
        } else if (!this.currentId && this.list.length > 0) {
          this.setCurrent(this.list[0]!.id)
        }
        return this.list
      } finally {
        this.loading = false
      }
    },

    setCurrent(id: number) {
      this.currentId = id
      if (id > 0) localStorage.setItem(STORAGE_KEY, String(id))
      else localStorage.removeItem(STORAGE_KEY)
    },
  },
})
