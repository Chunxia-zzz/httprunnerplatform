/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  // eslint-disable-next-line @typescript-eslint/no-empty-object-type -- Vite 脚手架惯用法：泛型 SFC 声明
  const component: DefineComponent<{}, {}, any>
  export default component
}
