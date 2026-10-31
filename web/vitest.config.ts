import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vitest/config'

/**
 * 前端单测配置（vitest）。
 *
 * 与 vite.config.ts 保持同源的是 plugins 与 '@' 别名；
 * 测试专属的只有 test 段：
 *   - environment 用 happy-dom：组件测试需要 document/window
 *     （client.ts 的 401 广播走 window.dispatchEvent）。
 *   - include 约定：与被测文件同目录的 __tests__/*.test.ts。
 *   - 单测不碰网络：client 的测试用自定义 axios adapter，不发真实请求。
 */
export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  test: {
    environment: 'happy-dom',
    include: ['src/**/__tests__/**/*.test.ts'],
  },
})
