<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, ApiError, sharesApi, type Health, type PublicShare } from '../api'
import { session } from '../session'

const router = useRouter()
const health = ref<Health | null>(null)
const shares = ref<PublicShare[]>([])
const error = ref('')
const loading = ref(false)

async function refresh() {
  loading.value = true
  error.value = ''
  try {
    const [h, s] = await Promise.all([api.health(), sharesApi.public()])
    health.value = h
    shares.value = s
  } catch (e) {
    health.value = null
    shares.value = []
    error.value = e instanceof ApiError ? e.message : '无法连接服务'
  } finally {
    loading.value = false
  }
}

function open(share: PublicShare) {
  void router.push(`/s/${share.id}`)
}

onMounted(refresh)
</script>

<template>
  <el-alert
    v-if="session.must_change"
    type="warning"
    show-icon
    :closable="false"
    title="当前使用初始口令，请先修改后再继续"
    style="margin-bottom: 16px"
  >
    <el-button size="small" type="primary" @click="$router.push('/admin/password')">去修改</el-button>
  </el-alert>

  <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon />

  <el-empty v-else-if="!loading && shares.length === 0" description="还没有可浏览的共享目录">
    <el-button v-if="session.authenticated" type="primary" @click="$router.push('/admin/shares')">
      去添加共享目录
    </el-button>
    <span v-else class="land-muted">请联系管理员在后台添加共享目录。</span>
  </el-empty>

  <el-row v-else :gutter="16">
    <el-col v-for="share in shares" :key="share.id" :span="8" style="margin-bottom: 16px">
      <el-card shadow="hover" style="cursor: pointer" @click="open(share)">
        <div style="font-size: 15px; font-weight: 600">{{ share.name }}</div>
        <div class="land-muted" style="margin-top: 6px">{{ share.virtual_path }}</div>
      </el-card>
    </el-col>
  </el-row>

  <el-card v-if="health" shadow="never" style="margin-top: 16px">
    <div class="land-muted">
      服务状态 {{ health.status }} · 版本 {{ health.version }} · 共享 {{ health.shares }} 个
      <el-button text size="small" :loading="loading" @click="refresh">刷新</el-button>
    </div>
  </el-card>
</template>
