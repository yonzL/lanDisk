<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError, logsApi, type HostEntry, type LogEntry } from '../api'

const entries = ref<LogEntry[]>([])
const total = ref(0)
const loading = ref(false)
const error = ref('')
const page = ref(1)
const pageSize = ref(100)

const filters = reactive({ from: '', to: '', ip: '', action: '' })

const hostList = ref<HostEntry[]>([])
const hostForm = reactive({ ip: '', hostname: '', saving: false })

const ACTION_LABELS: Record<string, string> = {
  login: '登录',
  logout: '退出',
  password_change: '改口令',
  browse: '浏览',
  search: '搜索',
  download: '下载',
  download_zip: '打包下载',
  upload_begin: '开始上传',
  upload_chunk: '上传分块',
  upload: '上传完成',
  upload_abort: '放弃上传',
  mkdir: '新建目录',
  rename: '重命名',
  move: '移动',
  delete: '删除',
  trash_restore: '恢复',
  trash_clear: '清空回收站',
  note: '备注',
  admin_change: '配置变更',
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await logsApi.list({
      ...filters,
      page: page.value,
      pageSize: pageSize.value,
    })
    entries.value = res.entries
    total.value = res.total
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

async function loadHosts() {
  try {
    hostList.value = await logsApi.hosts()
  } catch {
    // The log table is the main content; a host list failure is not fatal.
  }
}

function search() {
  page.value = 1
  void load()
}

function exportCsv() {
  window.location.href = logsApi.exportUrl(filters)
}

function humanTime(unix: number) {
  const d = new Date(unix * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

async function addHost() {
  if (!hostForm.ip.trim() || !hostForm.hostname.trim()) {
    ElMessage.warning('IP 与主机名都不能为空')
    return
  }
  hostForm.saving = true
  try {
    await logsApi.setHost({ ip: hostForm.ip.trim(), hostname: hostForm.hostname.trim() })
    hostForm.ip = ''
    hostForm.hostname = ''
    await loadHosts()
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '保存失败')
  } finally {
    hostForm.saving = false
  }
}

async function removeHost(ip: string) {
  try {
    await logsApi.deleteHost(ip)
    await loadHosts()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '删除失败')
  }
}

function onPage(next: number) {
  page.value = next
  void load()
}

onMounted(() => {
  void load()
  void loadHosts()
})
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div style="display: flex; align-items: center">
        <span>访问日志</span>
        <div style="flex: 1" />
        <el-button text :loading="loading" @click="load">刷新</el-button>
        <el-button type="primary" plain @click="exportCsv">导出 CSV</el-button>
      </div>
    </template>

    <el-form inline @submit.prevent="search">
      <el-form-item label="从">
        <el-input v-model="filters.from" type="date" placeholder="2026-01-01" style="width: 150px" />
      </el-form-item>
      <el-form-item label="到">
        <el-input v-model="filters.to" type="date" placeholder="2026-12-31" style="width: 150px" />
      </el-form-item>
      <el-form-item label="IP">
        <el-input v-model="filters.ip" placeholder="192.168.1.31" style="width: 150px" />
      </el-form-item>
      <el-form-item label="操作">
        <el-select v-model="filters.action" clearable placeholder="全部" style="width: 140px">
          <el-option v-for="(label, key) in ACTION_LABELS" :key="key" :label="label" :value="key" />
        </el-select>
      </el-form-item>
      <el-button type="primary" @click="search">查询</el-button>
    </el-form>

    <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon style="margin-bottom: 12px" />

    <el-table :data="entries" v-loading="loading" size="small">
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ humanTime(row.ts) }}</template>
      </el-table-column>
      <el-table-column label="来源" width="200">
        <template #default="{ row }">
          <div>{{ row.host || '—' }}</div>
          <div class="land-muted">{{ row.ip }}</div>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="110">
        <template #default="{ row }">{{ ACTION_LABELS[row.action] ?? row.action }}</template>
      </el-table-column>
      <el-table-column label="对象" min-width="280">
        <template #default="{ row }"><span class="land-muted">{{ row.path }}</span></template>
      </el-table-column>
      <el-table-column label="结果" width="90">
        <template #default="{ row }">
          <el-tag :type="row.result === 'ok' ? 'success' : 'danger'" size="small">
            {{ row.result === 'ok' ? '成功' : '失败' }}
          </el-tag>
        </template>
      </el-table-column>
    </el-table>

    <el-pagination
      style="margin-top: 12px; justify-content: flex-end"
      layout="total, prev, pager, next, sizes"
      :total="total"
      :page-size="pageSize"
      :current-page="page"
      :page-sizes="[50, 100, 200]"
      @current-change="onPage"
      @size-change="(size: number) => { pageSize = size; page = 1; load() }"
    />
  </el-card>

  <el-card shadow="never" style="margin-top: 16px">
    <template #header>IP 主机名映射</template>
    <p class="land-muted" style="margin-top: 0">
      手动填写的映射优先于自动反查，而且对历史日志同样生效（导出时按最新映射取名）。
    </p>
    <el-form inline @submit.prevent="addHost">
      <el-form-item label="IP">
        <el-input v-model="hostForm.ip" placeholder="192.168.1.31" style="width: 160px" />
      </el-form-item>
      <el-form-item label="主机名">
        <el-input v-model="hostForm.hostname" placeholder="张三的笔记本" style="width: 200px" />
      </el-form-item>
      <el-button type="primary" :loading="hostForm.saving" @click="addHost">保存</el-button>
    </el-form>
    <el-table :data="hostList" size="small" empty-text="还没有手动映射">
      <el-table-column prop="ip" label="IP" width="160" />
      <el-table-column prop="hostname" label="主机名" min-width="200" />
      <el-table-column prop="source" label="来源" width="90">
        <template #default="{ row }">{{ row.source === 'manual' ? '手动' : '反查' }}</template>
      </el-table-column>
      <el-table-column width="90" align="right">
        <template #default="{ row }">
          <el-button text type="danger" size="small" @click="removeHost(row.ip)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>
