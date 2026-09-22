<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'

import { ApiError } from '@/api'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const formRef = ref<FormInstance>()
const form = reactive({ username: '', password: '' })

const rules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }],
}

async function onSubmit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  try {
    await auth.login(form.username.trim(), form.password)
    ElMessage.success('登录成功')
    const redirect = route.query.redirect as string | undefined
    await router.push(redirect || { name: 'cases' })
  } catch (e) {
    // 后端刻意不区分「用户不存在」与「密码错误」，直接把 message 展示出来即可。
    const msg = e instanceof ApiError ? e.message : '登录失败，请稍后重试'
    ElMessage.error(msg)
  }
}
</script>

<template>
  <div class="login">
    <el-card class="login__card" shadow="always">
      <div class="login__head">
        <span class="login__mark">hrp</span>
        <h1 class="login__title">接口自动化测试平台</h1>
        <p class="login__sub">基于 HttpRunner v4.3.6 · 用例编辑 / 执行 / 失败归因</p>
      </div>

      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" @submit.prevent="onSubmit">
        <el-form-item label="用户名" prop="username">
          <el-input v-model="form.username" placeholder="admin" autocomplete="username" />
        </el-form-item>
        <el-form-item label="密码" prop="password">
          <el-input
            v-model="form.password"
            type="password"
            show-password
            placeholder="请输入密码"
            autocomplete="current-password"
            @keyup.enter="onSubmit"
          />
        </el-form-item>
        <el-button type="primary" class="login__submit" :loading="auth.loading" @click="onSubmit">
          登录
        </el-button>
      </el-form>

      <p class="login__hint">
        首次部署请先执行 <code>httprunnerplatform migrate</code> 初始化数据库与管理员账号。
      </p>
    </el-card>
  </div>
</template>

<style scoped>
.login {
  height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  background: linear-gradient(160deg, #eef2f8 0%, #f7f9fc 60%, #eaeef5 100%);
}

.login__card {
  width: 380px;
  padding: 8px 6px;
}

.login__head {
  text-align: center;
  margin-bottom: 18px;
}

.login__mark {
  display: inline-block;
  font-family: var(--hrp-mono);
  font-weight: 700;
  font-size: 13px;
  color: var(--el-color-primary);
  background: var(--el-color-primary-light-9);
  border-radius: 6px;
  padding: 3px 10px;
}

.login__title {
  margin: 12px 0 4px;
  font-size: 19px;
}

.login__sub {
  margin: 0;
  font-size: 12px;
  color: var(--hrp-muted);
}

.login__submit {
  width: 100%;
}

.login__hint {
  margin: 16px 0 0;
  font-size: 12px;
  color: var(--hrp-muted);
  line-height: 1.7;
}

.login__hint code {
  font-family: var(--hrp-mono);
  background: #f2f3f5;
  border-radius: 3px;
  padding: 1px 4px;
}
</style>
