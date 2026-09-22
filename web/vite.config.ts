import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'

/**
 * 后端地址。默认与 configs/config.example.yaml 的 server.port 一致。
 * 通过环境变量 HRP_API_TARGET 覆盖，方便同时开多套后端做联调。
 */
const apiTarget = process.env.HRP_API_TARGET || 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    host: '127.0.0.1',
    port: 5173,
    strictPort: true,
    // ⭐ 走 dev server 反向代理，而不是依赖后端 CORS。
    //
    // 两条原因：
    //   1. 后端的 CORS 只在 server.mode=debug 时放开，且白名单写死了 5173；
    //   2. 登录态是 HttpOnly + SameSite=Lax 的 Cookie。跨端口直连时
    //      浏览器对 127.0.0.1 与 localhost 的「同站」判定不一致，
    //      会出现"登录成功但后续请求 401"这种极难排查的现象。
    // 代理之后所有请求都是同源，Cookie 与预检问题一并消失。
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    // 交给 Go 的 go:embed 内嵌时不需要 sourcemap
    sourcemap: false,
    chunkSizeWarningLimit: 1500,
  },
})
