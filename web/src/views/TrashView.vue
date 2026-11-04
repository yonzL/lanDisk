<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ApiError, trashApi } from '../api'

interface TrashItem {
  id: number
  share_id: number
  origin: string
  deleted_at: number
  ip: string
}

const items = ref<TrashItem[]>([])
const loading = ref(false)
const error = ref('')

async function load() {
  loading.value = true
  error.value = ''
  try {
    items.value = await trashApi.list()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

function humanTime(unix: number) {
  const d = new Date(unix * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

async function restore(item: TrashItem) {
  try {
    await trashApi.restore(item.id)
    ElMessage.success('已恢复')
  } catch (e) {
    if (e instanceof ApiError && e.code === 'target_exists') {
      try {
        await ElMessageBox.confirm('原位置已有同名文件，恢复会覆盖它。继续？', '目标已存在', { type: 'warning' })
      } catch {
        return
      }
      try {
        await trashApi.restore(item.id, true)
        ElMessage.success('已覆盖恢复')
      } catch (err) {
        ElMessage.error(err instanceof ApiError ? err.message : '恢复失败')
        return
      }
    } else {
      ElMessage.error(e instanceof ApiError ? e.message : '恢复失败')
      return
    }
  }
  await load()
}

async function clearAll() {
  try {
    await ElMessageBox.confirm('清空回收站会永久删除其中所有内容，无法恢复。继续？', '清空回收站', {
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    const res = await trashApi.clear()
    ElMessage.success(`已永久删除 ${res.removed} 项`)
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '清空失败')
  }
}

onMounted(load)
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div style="display: flex; align-items: center">
        <span>回收站</span>
        <div style="flex: 1" />
        <el-button text :loading="loading" @click="load">刷新</el-button>
        <el-button type="danger" plain @click="clearAll">清空</el-button>
      </div>
    </template>

    <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon style="margin-bottom: 12px" />
    <p class="land-muted" style="margin-top: 0">
      删除的文件会先进入这里，超过保留期（默认 90 天，可在设置中调整）后自动永久删除。
    </p>

    <el-table :data="items" v-loading="loading" empty-text="回收站是空的">
      <el-table-column label="原位置" min-width="260">
        <template #default="{ row }"><code>{{ row.origin }}</code></template>
      </el-table-column>
      <el-table-column label="删除时间" width="170">
        <template #default="{ row }">{{ humanTime(row.deleted_at) }}</template>
      </el-table-column>
      <el-table-column label="来源 IP" width="150">
        <template #default="{ row }"><span class="land-muted">{{ row.ip }}</span></template>
      </el-table-column>
      <el-table-column label="操作" width="110" align="right">
        <template #default="{ row }">
          <el-button text type="primary" @click="restore(row)">恢复</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>
