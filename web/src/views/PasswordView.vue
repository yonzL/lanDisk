<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { ApiError, authApi } from '../api'
import { session, forgetSession } from '../session'

const router = useRouter()
const current = ref('')
const username = ref('')
const next = ref('')
const confirm = ref('')
const error = ref('')
const busy = ref(false)

onMounted(() => {
  username.value = session.username ?? 'admin'
})

async function submit() {
  error.value = ''
  if (next.value.length < 8) {
    error.value = '新口令至少 8 位'
    return
  }
  if (next.value !== confirm.value) {
    error.value = '两次输入的新口令不一致'
    return
  }
  const nextUser = username.value.trim()
  if (nextUser.length < 2) {
    error.value = '用户名至少 2 个字符'
    return
  }
  busy.value = true
  try {
    const res = await authApi.changeCredentials({
      // On the first login the session was just created with this password, so
      // the server does not ask for it again; the field is hidden above.
      currentPassword: session.must_change ? '' : current.value,
      newUsername: nextUser,
      newPassword: next.value,
    })
    // The server drops every session on a credential change, so this client is
    // logged out too: send the operator to the login page with the new name.
    const previousUser = session.username ?? 'admin'
    forgetSession()
    ElMessage.success(
      res.username && res.username !== previousUser
        ? `账号已改为 ${res.username}，请重新登录`
        : '已保存，请重新登录',
    )
    await router.push({ name: 'login', query: { username: res.username } })
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '修改失败'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <el-card shadow="never" style="max-width: 480px; margin: 48px auto">
    <template #header>{{ session.must_change ? '首次登录：请设置用户名与口令' : '修改用户名与口令' }}</template>
    <el-form label-position="top" @submit.prevent="submit">
      <el-form-item v-if="!session.must_change" label="当前口令">
        <el-input v-model="current" type="password" show-password autocomplete="current-password" />
      </el-form-item>
      <el-form-item label="用户名（登录名，默认 admin）">
        <el-input v-model="username" autocomplete="username" />
      </el-form-item>
      <el-form-item label="新口令（至少 8 位）">
        <el-input v-model="next" type="password" show-password autocomplete="new-password" />
      </el-form-item>
      <el-form-item label="确认新口令">
        <el-input v-model="confirm" type="password" show-password autocomplete="new-password" @keyup.enter="submit" />
      </el-form-item>
      <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon style="margin-bottom: 12px" />
      <el-button type="primary" native-type="submit" :loading="busy" style="width: 100%">保存</el-button>
      <p class="land-muted" style="margin: 12px 0 0">
        {{ session.must_change ? '首次登录只需设置新口令，不用再输一遍初始口令。' : '保存后所有已登录会话会失效，需要用新的用户名与口令重新登录。' }}
      </p>
    </el-form>
  </el-card>
</template>
