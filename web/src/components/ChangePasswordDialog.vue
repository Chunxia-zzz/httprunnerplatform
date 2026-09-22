<script setup lang="ts">
/**
 * 修改密码弹窗（本人操作，需验旧密码）。
 *
 * 与管理员重置他人密码是两条完全不同的路径，刻意分成两个入口：
 *   - 这里走 `POST /auth/password`，验旧密码，**保留当前会话**；
 *   - 管理员重置走 `POST /users/{id}/password`，不验旧密码，且会吊销对方全部会话。
 * 合并成一个弹窗的话，"要不要填原密码"会变成一个后端才知道的条件，
 * 前端只能靠猜。
 *
 * 改成功后当前会话仍然有效，所以不需要重新登录 —— 但其它设备上的
 * 会话已经被后端踢掉了，这一点必须在提示里讲清楚。
 */
import { reactive, ref, watch } from 'vue'
import { type FormInstance, type FormRules } from 'element-plus'

import { authApi } from '@/api'
import { useAuthStore } from '@/stores/auth'
import { notifyError, notifyOk } from '@/utils/error'
import { passwordValidator } from '@/utils/password'

const visible = defineModel<boolean>({ required: true })

const auth = useAuthStore()
const formRef = ref<FormInstance>()
const submitting = ref(false)

const form = reactive({
  old_password: '',
  new_password: '',
  confirm_password: '',
})

const rules: FormRules = {
  old_password: [{ required: true, message: '请输入原密码', trigger: 'blur' }],
  new_password: [
    { required: true, validator: passwordValidator(auth.principal?.username ?? ''), trigger: 'blur' },
  ],
  confirm_password: [
    {
      required: true,
      validator: (_r: unknown, value: string, cb: (e?: Error) => void) => {
        if (!value) return cb(new Error('请再次输入新密码'))
        // 在提交前拦下不一致，而不是让用户提交后才发现密码被设成了打错的那个
        if (value !== form.new_password) return cb(new Error('两次输入的新密码不一致'))
        cb()
      },
      trigger: 'blur',
    },
  ],
}

function reset() {
  form.old_password = ''
  form.new_password = ''
  form.confirm_password = ''
  formRef.value?.clearValidate()
}

// 每次打开都清空：留着上一次的密码在输入框里既不安全也让人困惑。
watch(visible, (v) => {
  if (v) reset()
})

async function submit() {
  if (!formRef.value) return
  const ok = await formRef.value.validate().catch(() => false)
  if (!ok) return

  submitting.value = true
  try {
    await authApi.changePassword({
      old_password: form.old_password,
      new_password: form.new_password,
    })
    visible.value = false
    notifyOk('密码已修改，其它设备上的登录已失效')
  } catch (e) {
    // 40000：原密码不正确 / 新密码不合规 / 新旧密码相同
    notifyError(e, '修改密码失败')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <el-dialog v-model="visible" title="修改密码" width="460px" destroy-on-close>
    <el-alert type="info" :closable="false" show-icon class="tip">
      当前登录仍然有效；其它设备上的会话会被立即登出。
    </el-alert>

    <el-form ref="formRef" :model="form" :rules="rules" label-width="104px" class="form">
      <el-form-item label="账号">
        <span class="hrp-mono">{{ auth.principal?.username }}</span>
      </el-form-item>
      <el-form-item label="原密码" prop="old_password">
        <el-input v-model="form.old_password" type="password" show-password autocomplete="current-password" />
      </el-form-item>
      <el-form-item label="新密码" prop="new_password">
        <el-input v-model="form.new_password" type="password" show-password autocomplete="new-password" />
        <div class="hrp-muted form-tip">至少 8 位，不含空白字符；不能与账号名相同或使用常见弱密码。</div>
      </el-form-item>
      <el-form-item label="确认新密码" prop="confirm_password">
        <el-input v-model="form.confirm_password" type="password" show-password autocomplete="new-password" />
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="submit">确认修改</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.tip {
  margin-bottom: 16px;
}

.form {
  margin-top: 4px;
}

.form-tip {
  font-size: 12px;
  line-height: 1.6;
  margin-top: 4px;
}
</style>
