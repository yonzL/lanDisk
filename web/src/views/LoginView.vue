<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ApiError } from '../api'
import { login, session } from '../session'

const router = useRouter()
const route = useRoute()
const initialUser = typeof route.query.username === 'string' ? route.query.username : 'admin'
const username = ref(initialUser)
const password = ref('')
const error = ref('')
const busy = ref(false)

async function submit() {
  error.value = ''
  busy.value = true
  try {
    await login(username.value.trim() || 'admin', password.value)
    password.value = ''
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : ''
    if (redirect.startsWith('/')) {
      await router.push(redirect)
      return
    }
    await router.push(session.must_change ? '/admin/password' : '/')
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '登录失败'
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <el-card shadow="never" style="max-width: 420px; margin: 48px auto">
    <template #header>管理员登录</template>
    <el-form label-position="top" @submit.prevent="submit">
      <el-form-item label="用户名">
        <el-input v-model="username" autocomplete="username" @keyup.enter="submit" />
      </el-form-item>
      <el-form-item label="口令">
        <el-input
          v-model="password"
          type="password"
          show-password
          autocomplete="current-password"
          @keyup.enter="submit"
        />
      </el-form-item>
      <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon style="margin-bottom: 12px" />
      <el-button type="primary" native-type="submit" :loading="busy" style="width: 100%">登录</el-button>
      <p class="land-muted" style="margin: 12px 0 0">
        忘记口令或用户名？在服务器上运行 <code>land.exe reset-password</code> 重置。
      </p>
    </el-form>
  </el-card>
</template>
