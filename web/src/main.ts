import { createPinia } from 'pinia'
import { createApp } from 'vue'
import ElementPlus from 'element-plus'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import * as ElementPlusIconsVue from '@element-plus/icons-vue'

import App from './App.vue'
import router from './router'
import { initUnauthorizedListener } from './stores/auth'

import 'element-plus/dist/index.css'
import './styles/main.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(ElementPlus, { locale: zhCn })

// 图标全量注册。M1 用到的图标不多，但按需注册会让
// 「模板里写了个没注册的图标」变成运行时静默空白，排查成本高于收益。
for (const [key, component] of Object.entries(ElementPlusIconsVue)) {
  app.component(key, component)
}

// 401 广播 → 跳登录页。放在这里而不是 http 层，是为了避免循环依赖。
initUnauthorizedListener(() => {
  void router.push({ name: 'login' })
})

app.mount('#app')
