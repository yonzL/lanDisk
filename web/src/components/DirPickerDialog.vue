<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { ApiError, fsApi, listApi, type FSEntry } from '../api'

// One picker for both jobs:
//  - mode="machine": browse the server's drives (only an admin can create a share)
//  - mode="share":   stay inside one share (choosing a move destination)
const props = defineProps<{
  modelValue: boolean
  mode: 'machine' | 'share'
  startPath?: string
  shareId?: number
  title?: string
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'confirm', path: string): void
}>()

type Crumb = { name: string; path: string }

const current = ref('')
const entries = ref<FSEntry[]>([])
const loading = ref(false)
const error = ref('')
const creating = ref(false)
const newName = ref('')

const visible = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const crumbs = computed<Crumb[]>(() => {
  if (props.mode === 'share') {
    const parts = current.value ? current.value.split('/') : []
    const list: Crumb[] = [{ name: '根目录', path: '' }]
    parts.forEach((part, i) => list.push({ name: part, path: parts.slice(0, i + 1).join('/') }))
    return list
  }
  if (!current.value) return []
  const out: Crumb[] = []
  const normalized = current.value.replace(/\\/g, '/')
  const hasDrive = /^[a-zA-Z]:/.test(normalized)
  const pieces = normalized.split('/').filter(Boolean)
  let acc = ''
  for (const piece of pieces) {
    if (!acc && hasDrive) {
      acc = piece + '/'
      out.push({ name: piece.toUpperCase() + '\\', path: acc })
      continue
    }
    acc = acc ? acc.replace(/\/$/, '') + '/' + piece : piece
    out.push({ name: piece, path: acc })
  }
  return out
})

async function open() {
  current.value = props.startPath ?? ''
  newName.value = ''
  error.value = ''
  await load(current.value)
}

async function load(path: string) {
  loading.value = true
  error.value = ''
  try {
    if (props.mode === 'share' && props.shareId) {
      const res = await listApi.list(props.shareId, { path, pageSize: 500 })
      current.value = res.path
      entries.value = res.entries
        .filter((e) => e.is_dir)
        .map((e) => ({ name: e.name, path: e.path, is_dir: true }))
    } else {
      const res = await fsApi.list(path)
      current.value = res.path
      entries.value = res.entries
    }
  } catch (e) {
    entries.value = []
    error.value = e instanceof ApiError ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

function enter(entry: FSEntry) {
  void load(entry.path)
}

function up() {
  if (props.mode === 'share') {
    const parts = current.value.split('/').filter(Boolean)
    parts.pop()
    void load(parts.join('/'))
    return
  }
  const parent = current.value.replace(/[/\\][^/\\]*$/, '')
  void load(parent === current.value ? '' : parent)
}

async function createDir() {
  const name = newName.value.trim()
  if (!name) {
    ElMessage.warning('请填写目录名')
    return
  }
  creating.value = true
  try {
    if (props.mode === 'share' && props.shareId) {
      const { opsApi } = await import('../api')
      await opsApi.mkdir(props.shareId, current.value, name)
    } else {
      await fsApi.mkdir(current.value, name)
    }
    newName.value = ''
    await load(current.value)
    ElMessage.success('已创建目录')
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '创建失败')
  } finally {
    creating.value = false
  }
}

function confirm() {
  if (props.mode === 'machine' && !current.value) {
    ElMessage.warning('请先进入一个目录')
    return
  }
  emit('confirm', current.value)
  visible.value = false
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) void open()
  },
)
</script>

<template>
  <el-dialog v-model="visible" :title="title ?? '选择目录'" width="640px" @open="open">
    <div class="dirpicker">
      <div class="dirpicker__bar">
        <el-button text :disabled="!current" @click="up">↑ 上一级</el-button>
        <span class="dirpicker__path">{{ current || '此电脑' }}</span>
      </div>

      <el-breadcrumb separator="/" class="dirpicker__crumbs">
        <el-breadcrumb-item v-if="mode === 'machine'">
          <a href="javascript:void(0)" @click="load('')">此电脑</a>
        </el-breadcrumb-item>
        <el-breadcrumb-item v-for="crumb in crumbs" :key="crumb.path">
          <a href="javascript:void(0)" @click="load(crumb.path)">{{ crumb.name }}</a>
        </el-breadcrumb-item>
      </el-breadcrumb>

      <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon style="margin-bottom: 8px" />

      <div class="dirpicker__list" v-loading="loading">
        <div
          v-for="entry in entries"
          :key="entry.path"
          class="dirpicker__row"
          @dblclick="enter(entry)"
        >
          <i class="ri-folder-3-fill dirpicker__icon" />
          <span class="dirpicker__name" @click="enter(entry)">{{ entry.name }}</span>
          <el-button text size="small" @click="enter(entry)">进入</el-button>
        </div>
        <el-empty v-if="!entries.length && !loading" description="没有子目录" :image-size="60" />
      </div>

      <div class="dirpicker__new">
        <el-input v-model="newName" placeholder="在此目录下新建文件夹" size="small" @keyup.enter="createDir" />
        <el-button size="small" :loading="creating" @click="createDir">新建</el-button>
      </div>
    </div>

    <template #footer>
      <span class="land-muted" style="float: left">选中：{{ current || '（未选择）' }}</span>
      <el-button @click="visible = false">取消</el-button>
      <el-button type="primary" @click="confirm">选择此目录</el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.dirpicker__bar {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.dirpicker__path {
  font-size: 13px;
  color: var(--land-muted);
  word-break: break-all;
}
.dirpicker__crumbs {
  margin-bottom: 8px;
}
.dirpicker__list {
  height: 280px;
  overflow: auto;
  border: 1px solid var(--land-border);
  border-radius: 6px;
}
.dirpicker__row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 10px;
  border-bottom: 1px solid #f2f3f5;
  cursor: default;
}
.dirpicker__row:hover {
  background: #f5f7fa;
}
.dirpicker__icon {
  font-size: 16px;
}
.dirpicker__name {
  flex: 1;
  cursor: pointer;
  font-size: 14px;
}
.dirpicker__new {
  display: flex;
  gap: 8px;
  margin-top: 10px;
}
</style>
