import js from '@eslint/js'
import globals from 'globals'
import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'

/**
 * ESLint 9 flat config。
 *
 * 选型说明（务实优先，见 docs/项目进度检查报告.md P1）：
 *   - vue 用 flat/essential 而不是 recommended：essential 只管「会出错的」，
 *     recommended 里的模板风格类规则（属性顺序、每行属性数等）对存量 15 个页面
 *     会一次报几百条，等价于教大家无视 lint。风格收敛留给 Prettier 接管。
 *   - @typescript-eslint/no-explicit-any 暂关：存量代码有多处 Element Plus
 *     类型擦除点的合法 any（见接口契约.md 第 8 节第 5 条），收紧留待专项。
 */
export default tseslint.config(
  { ignores: ['dist/**', 'node_modules/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/essential'],
  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: { parser: tseslint.parser },
    },
  },
  {
    languageOptions: {
      globals: { ...globals.browser },
    },
    rules: {
      '@typescript-eslint/no-explicit-any': 'off',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      'no-console': ['error', { allow: ['warn', 'error'] }],
    },
  },
)
