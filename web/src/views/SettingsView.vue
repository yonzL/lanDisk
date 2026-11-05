<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError, settingsApi, type IPRule } from '../api'

const form = reactive<Record<string, string>>({
  site_title: '',
  site_notice: '',
  port: '',
  log_retention_days: '',
  trash_retention_days: '',
  max_conns: '',
  download_rate_limit_kbps: '',
  upload_rate_limit_kbps: '',
})
const loading = ref(false)
const saving = ref(false)
const error = ref('')

const rules = ref<IPRule[]>([])
const ruleForm = reactive({ cidr: '', kind: 'deny', note: '', saving: false })

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = await settingsApi.get()
    for (const key of Object.keys(form)) {
      if (res.values[key] !== undefined) form[key] = res.values[key]
    }
    rules.value = await settingsApi.ipRules()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    await settingsApi.put({ ...form })
    ElMessage.success('已保存（端口修改需重启服务）')
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '保存失败')
  } finally {
    saving.value = false
  }
}

async function addRule() {
  if (!ruleForm.cidr.trim()) {
    ElMessage.warning('请填写 IP 或 CIDR')
    return
  }
  ruleForm.saving = true
  try {
    await settingsApi.setIPRule({ cidr: ruleForm.cidr.trim(), kind: ruleForm.kind, note: ruleForm.note })
    ruleForm.cidr = ''
    ruleForm.note = ''
    rules.value = await settingsApi.ipRules()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '保存失败')
  } finally {
    ruleForm.saving = false
  }
}

async function removeRule(cidr: string) {
  try {
    await settingsApi.deleteIPRule(cidr)
    rules.value = await settingsApi.ipRules()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '删除失败')
  }
}

function humanTime(unix: number) {
  if (!unix) return '—'
  const d = new Date(unix * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

onMounted(load)
</script>

<template>
  <el-card shadow="never" v-loading="loading">
    <template #header>
      <div style="display: flex; align-items: center">
        <span>站点与运行设置</span>
        <div style="flex: 1" />
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </div>
    </template>

    <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon style="margin-bottom: 12px" />

    <el-form label-position="top">
      <el-form-item label="网页标题">
        <el-input v-model="form.site_title" />
      </el-form-item>
      <el-form-item label="顶部公告（单行文字，随页头滚动显示；不支持 HTML）">
        <el-input v-model="form.site_notice" maxlength="200" show-word-limit placeholder="单行公告，顶部滚动显示" />
      </el-form-item>

      <el-divider>运行参数</el-divider>
      <el-row :gutter="16">
        <el-col :span="8">
          <el-form-item label="监听端口（修改后需重启服务）">
            <el-input v-model="form.port" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="日志保留天数">
            <el-input v-model="form.log_retention_days" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="回收站保留天数">
            <el-input v-model="form.trash_retention_days" />
          </el-form-item>
        </el-col>
      </el-row>
      <el-row :gutter="16">
        <el-col :span="8">
          <el-form-item label="最大并发连接">
            <el-input v-model="form.max_conns" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="下载限速 KB/s（0 = 不限）">
            <el-input v-model="form.download_rate_limit_kbps" />
          </el-form-item>
        </el-col>
        <el-col :span="8">
          <el-form-item label="上传限速 KB/s（0 = 不限）">
            <el-input v-model="form.upload_rate_limit_kbps" />
          </el-form-item>
        </el-col>
      </el-row>
    </el-form>
  </el-card>

  <el-card shadow="never" style="margin-top: 16px">
    <template #header>IP 访问控制</template>
    <p class="land-muted" style="margin-top: 0">
      规则立即生效：deny 优先于 allow；存在 allow 规则时即为白名单模式；本机 127.0.0.1 始终放行。
      被拒请求不写入访问日志，只累计命中次数（见下表）。
    </p>
    <el-form inline @submit.prevent="addRule">
      <el-form-item label="IP / CIDR">
        <el-input v-model="ruleForm.cidr" placeholder="192.168.1.0/24" style="width: 180px" />
      </el-form-item>
      <el-form-item label="动作">
        <el-select v-model="ruleForm.kind" style="width: 110px">
          <el-option label="拒绝" value="deny" />
          <el-option label="允许" value="allow" />
        </el-select>
      </el-form-item>
      <el-form-item label="备注">
        <el-input v-model="ruleForm.note" placeholder="可选" style="width: 160px" />
      </el-form-item>
      <el-button type="primary" :loading="ruleForm.saving" @click="addRule">添加</el-button>
    </el-form>

    <el-table :data="rules" size="small" empty-text="还没有 IP 规则">
      <el-table-column prop="cidr" label="CIDR" width="200" />
      <el-table-column label="动作" width="90">
        <template #default="{ row }">
          <el-tag :type="row.kind === 'deny' ? 'danger' : 'success'" size="small">
            {{ row.kind === 'deny' ? '拒绝' : '允许' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="note" label="备注" min-width="160" />
      <el-table-column label="命中" width="100">
        <template #default="{ row }">{{ row.hits }}</template>
      </el-table-column>
      <el-table-column label="最近命中" width="170">
        <template #default="{ row }">{{ humanTime(row.last_hit_at) }}</template>
      </el-table-column>
      <el-table-column width="80" align="right">
        <template #default="{ row }">
          <el-button text type="danger" size="small" @click="removeRule(row.cidr)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>
</template>
