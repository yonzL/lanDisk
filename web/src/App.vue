<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { siteApi, type Site } from './api'
import { SESSION_EXPIRED_EVENT, ensureSession, logout, session } from './session'

const router = useRouter()
const route = useRoute()
const site = ref<Site>({ title: 'lan disk', notice: '', version: '' })

// The announcement is one line in the header. It only animates when the text is
// actually wider than its slot, so a short notice sits still instead of showing
// the duplicated copy a marquee loop needs.
const tickerViewport = ref<HTMLElement | null>(null)
const tickerText = ref<HTMLElement | null>(null)
const tickerScrolls = ref(false)
let viewportObserver: ResizeObserver | undefined

async function measureTicker() {
  if (!site.value.notice) {
    tickerScrolls.value = false
    return
  }
  tickerScrolls.value = false // measure with a single copy
  await nextTick()
  const viewport = tickerViewport.value
  const text = tickerText.value
  if (!viewport || !text) return
  tickerScrolls.value = text.scrollWidth > viewport.clientWidth + 1
}

onMounted(async () => {
  // A failed probe just means "not logged in"; the pages handle that state.
  ensureSession().catch(() => undefined)
  try {
    const info = await siteApi.get()
    if (info.title) site.value.title = info.title
    site.value.notice = info.notice
    document.title = site.value.title
  } catch {
    // Keep the defaults when the server is unreachable.
  }
  window.addEventListener(SESSION_EXPIRED_EVENT, onSessionExpired)
  window.addEventListener('resize', measureTicker)
  await measureTicker()
  if (tickerViewport.value && typeof ResizeObserver !== 'undefined') {
    viewportObserver = new ResizeObserver(() => void measureTicker())
    viewportObserver.observe(tickerViewport.value)
  }
})

onBeforeUnmount(() => {
  window.removeEventListener(SESSION_EXPIRED_EVENT, onSessionExpired)
  window.removeEventListener('resize', measureTicker)
  viewportObserver?.disconnect()
})

watch(() => site.value.notice, () => void measureTicker())

function onSessionExpired() {
  ElMessage.warning('登录状态已失效，请重新登录')
  void router.push({ name: 'login', query: { redirect: route.fullPath } })
}

async function signOut() {
  await logout()
  await router.push('/')
}

// One entry point for every admin page: the account button opens this menu.
async function onUserCommand(command: string) {
  switch (command) {
    case 'shares':
      await router.push('/admin/shares')
      break
    case 'trash':
      await router.push('/admin/trash')
      break
    case 'logs':
      await router.push('/admin/logs')
      break
    case 'settings':
      await router.push('/admin/settings')
      break
    case 'password':
      await router.push('/admin/password')
      break
    case 'logout':
      await signOut()
      break
  }
}
</script>


<template>
  <header class="land-header">
    <router-link to="/" class="land-header__brand">
      <div class="land-header__logo">L</div>
      <div class="land-header__title">{{ site.title }}</div>
    </router-link>

    <!-- Announcement: one line, sharing the header row with the account button.
         It is plain text (no HTML) and only scrolls when it overflows. -->
    <div v-if="site.notice" class="land-ticker" :title="site.notice">
      <i class="ri-notification-3-line land-ticker__icon" />
      <div ref="tickerViewport" class="land-ticker__viewport">
        <div class="land-ticker__track" :class="{ 'is-scrolling': tickerScrolls }">
          <span ref="tickerText" class="land-ticker__text">{{ site.notice }}</span>
          <span v-if="tickerScrolls" class="land-ticker__text" aria-hidden="true">{{ site.notice }}</span>
        </div>
      </div>
    </div>

    <div class="land-header__spacer" />

    <el-dropdown v-if="session.authenticated" trigger="click" @command="onUserCommand">
      <span class="land-user">
        <i class="ri-user-3-line" />
        {{ session.username ?? 'admin' }}
        <i class="ri-arrow-down-s-line" />
      </span>
      <template #dropdown>
        <el-dropdown-menu>
          <el-dropdown-item command="shares">
            <i class="ri-folder-settings-line" /> 共享目录
          </el-dropdown-item>
          <el-dropdown-item command="trash">
            <i class="ri-delete-bin-6-line" /> 回收站
          </el-dropdown-item>
          <el-dropdown-item command="logs">
            <i class="ri-file-list-3-line" /> 访问日志
          </el-dropdown-item>
          <el-dropdown-item command="settings">
            <i class="ri-settings-3-line" /> 站点设置
          </el-dropdown-item>
          <el-dropdown-item command="password" divided>
            <i class="ri-lock-password-line" /> 修改口令
          </el-dropdown-item>
          <el-dropdown-item command="logout">
            <i class="ri-logout-box-r-line" /> 退出登录
          </el-dropdown-item>
        </el-dropdown-menu>
      </template>
    </el-dropdown>

    <el-button v-else text @click="router.push('/login')">登录</el-button>
  </header>


  <main :class="['land-main', { 'land-main--full': route.name === 'preview' }]">
    <router-view />
  </main>
</template>
