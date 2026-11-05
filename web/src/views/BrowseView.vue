<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  ApiError,
  listApi,
  notesApi,
  opsApi,
  searchApi,
  siteApi,
  trashApi,
  type Entry,
  sharesApi,
  type ListResponse,
  type PublicShare,
  type SearchResponse,
} from '../api'
import { uploadFile, type UploadProgress } from '../upload'
import { session } from '../session'
import DirPickerDialog from '../components/DirPickerDialog.vue'

// Columns are fixed. Drag-to-reorder used to be offered here, but a reordered
// header never survived a re-render reliably, so the feature is gone rather than
// half-working; widths follow a 2:2:1:2:2 ratio via Element Plus min-width so
// the table still adapts to the window.
type SortKey = 'name' | 'size' | 'mod_time'

const route = useRoute()
const router = useRouter()

// The virtual root: every shared directory is a folder here, so "/" is the only
// home the product needs. Inside a share the same view lists files.
const isRoot = computed(() => route.name === 'root')
const shares = ref<PublicShare[]>([])

const shareId = computed(() => Number(route.params.id))
const currentPath = computed(() => {
  const raw = route.params.pathMatch
  const joined = Array.isArray(raw) ? raw.join('/') : (raw ?? '')
  return joined.replace(/^\/+|\/+$/g, '')
})

const listing = ref<ListResponse | null>(null)
const noteDialog = ref({ visible: false, path: '', name: '', note: '', saving: false })
const searchQuery = ref('')
const searching = ref(false)
const searchResult = ref<SearchResponse | null>(null)
const uploads = ref<UploadProgress[]>([])
const fileInput = ref<HTMLInputElement | null>(null)
const dirInput = ref<HTMLInputElement | null>(null)
const loading = ref(false)
const error = ref('')
const page = ref(1)
const pageSize = ref(loadPageSize())
const sort = ref<string>('name')
const order = ref<'asc' | 'desc'>('asc')
const pickerVisible = ref(false)
const siteVersion = ref('')

function loadPageSize(): number {
  const raw = Number(localStorage.getItem('land.pageSize'))
  return [50, 100, 200].includes(raw) ? raw : 100
}

function savePageSize(size: number) {
  localStorage.setItem('land.pageSize', String(size))
}


async function load() {
  loading.value = true
  error.value = ''
  try {
    if (isRoot.value) {
      shares.value = await sharesApi.public()
      listing.value = null
      return
    }
    if (!Number.isFinite(shareId.value) || shareId.value <= 0) return
    shares.value = []
    listing.value = await listApi.list(shareId.value, {
      path: currentPath.value,
      page: page.value,
      pageSize: pageSize.value,
      sort: sort.value,
      order: order.value,
    })
  } catch (e) {
    listing.value = null
    shares.value = []
    error.value = e instanceof ApiError ? (e.status === 404 ? '目录不存在或无权访问' : e.message) : '加载失败'
  } finally {
    loading.value = false
  }
}

async function runSearch() {
  const q = searchQuery.value.trim()
  if (!q) {
    clearSearch()
    return
  }
  searching.value = true
  error.value = ''
  const requestedPath = currentPath.value
  try {
    const result = await searchApi.find(shareId.value, requestedPath, q)
    if (currentPath.value !== requestedPath) return
    searchResult.value = result
  } catch (e) {
    if (currentPath.value !== requestedPath) return
    searchResult.value = null
    error.value = e instanceof ApiError ? e.message : '搜索失败'
  } finally {
    searching.value = false
  }
}

function clearSearch() {
  searchResult.value = null
  searchQuery.value = ''
}

function pickFiles() {
  fileInput.value?.click()
}

function pickDirectory() {
  dirInput.value?.click()
}

function percent(p: UploadProgress) {
  if (p.total === 0) return p.done ? 100 : 0
  return Math.floor((p.sent / p.total) * 100)
}

// ensureDir creates a nested directory path inside the share, tolerating the
// "already exists" conflict that a second upload into the same folder produces.
async function ensureDir(path: string) {
  const segments = path.split('/').filter(Boolean)
  let acc = ''
  for (const segment of segments) {
    try {
      await opsApi.mkdir(shareId.value, acc, segment)
    } catch (e) {
      if (!(e instanceof ApiError && e.status === 409)) throw e
    }
    acc = acc ? `${acc}/${segment}` : segment
  }
}

async function onFilesPicked(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  for (const file of files) {
    uploads.value = [{ name: file.name, sent: 0, total: file.size, done: false }]
    try {
      await uploadFile(shareId.value, currentPath.value, file, (progress) => {
        uploads.value = [progress]
      })
      ElMessage.success(`${file.name} 上传完成`)
    } catch (e) {
      const message = e instanceof ApiError ? e.message : e instanceof Error ? e.message : '上传失败'
      uploads.value = [{ name: file.name, sent: 0, total: file.size, done: false, error: message }]
      ElMessage.error(`${file.name} 上传失败：${message}`)
    }
  }
  await load()
}

async function onDirectoryPicked(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  if (!files.length) return

  // Files carry "dir/sub/file.ext" relative paths; create the folders first, then
  // upload each file into its own directory.
  const byDir = new Map<string, File[]>()
  for (const file of files) {
    const relative = (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name
    const parts = relative.split('/')
    parts.pop()
    const dir = parts.join('/')
    const bucket = byDir.get(dir) ?? []
    bucket.push(file)
    byDir.set(dir, bucket)
  }

  uploads.value = []
  let done = 0
  const total = files.length
  for (const [dir, bucket] of byDir) {
    try {
      if (dir) await ensureDir(dir)
    } catch (e) {
      ElMessage.error(`创建目录 ${dir} 失败：${e instanceof ApiError ? e.message : '未知错误'}`)
      continue
    }
    for (const file of bucket) {
      uploads.value = [{ name: `${dir ? dir + '/' : ''}${file.name}`, sent: 0, total: file.size, done: false }]
      try {
        await uploadFile(shareId.value, dir, file, (progress) => {
          uploads.value = [
            {
              name: `${dir ? dir + '/' : ''}${progress.name}`,
              sent: progress.sent,
              total: progress.total,
              done: progress.done,
              error: progress.error,
            },
          ]
        })
      } catch (e) {
        ElMessage.error(`${file.name} 上传失败：${e instanceof ApiError ? e.message : '未知错误'}`)
      }
      done++
    }
  }
  ElMessage.success(`目录上传完成（${done}/${total}）`)
  await load()
}

function enter(entry: { name: string; path: string; is_dir: boolean }) {
  // Only directories navigate: double-clicking a file must not push a route
  // whose listing then 404s (preview/download land in their own steps).
  if (!entry.is_dir) return
  const target = `/s/${shareId.value}/${entry.path.split('/').map(encodeURIComponent).join('/')}`
  void router.push(target)
}

function goTo(path: string) {
  const suffix = path ? `/${path.split('/').map(encodeURIComponent).join('/')}` : ''
  void router.push(`/s/${shareId.value}${suffix}`)
}

const breadcrumbs = computed(() => {
  const segments = currentPath.value ? currentPath.value.split('/') : []
  return segments.map((name, i) => ({ name, path: segments.slice(0, i + 1).join('/') }))
})

function onSort(key: SortKey) {
  if (sort.value === key) {
    order.value = order.value === 'asc' ? 'desc' : 'asc'
  } else {
    sort.value = key
    order.value = 'asc'
  }
  page.value = 1
  void load()
}

function humanSize(bytes: number, isDir: boolean) {
  if (isDir) return '—'
  if (bytes < 1024) return `${bytes}B`
  const units = ['K', 'M', 'G', 'T']
  let value = bytes / 1024
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(value >= 10 ? 1 : 2)}${units[unit]}`
}

function humanTime(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

function onPageChange(next: number) {
  page.value = next
  void load()
}

function onSizeChange(size: number) {
  pageSize.value = size
  savePageSize(size)
  page.value = 1
  void load()
}

const ops = reactive({
  mkdir: { visible: false, name: '', saving: false },
  rename: { visible: false, path: '', name: '', saving: false },
  move: { visible: false, path: '', to: '', saving: false },
})

function openMkdir() {
  ops.mkdir = { visible: true, name: '', saving: false }
}

async function submitMkdir() {
  ops.mkdir.saving = true
  try {
    await opsApi.mkdir(shareId.value, currentPath.value, ops.mkdir.name)
    ops.mkdir.visible = false
    ElMessage.success('已创建文件夹')
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '创建失败')
  } finally {
    ops.mkdir.saving = false
  }
}

async function removeEntry(entry: { path: string; name: string }) {
  try {
    await ElMessageBox.confirm(`删除「${entry.name}」？它会进入回收站，可随时恢复。`, '确认删除', {
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await trashApi.remove(shareId.value, entry.path)
    ElMessage.success('已移入回收站')
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '删除失败')
  }
}

function openRename(entry: { path: string; name: string }) {
  ops.rename = { visible: true, path: entry.path, name: entry.name, saving: false }
}

async function submitRename() {
  ops.rename.saving = true
  try {
    await opsApi.rename(shareId.value, ops.rename.path, ops.rename.name)
    ops.rename.visible = false
    ElMessage.success('已重命名')
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '重命名失败')
  } finally {
    ops.rename.saving = false
  }
}

function openMove(entry: { path: string }) {
  ops.move = { visible: true, path: entry.path, to: currentPath.value, saving: false }
}

async function submitMove() {
  ops.move.saving = true
  try {
    await opsApi.move(shareId.value, ops.move.path, ops.move.to.trim())
    ops.move.visible = false
    ElMessage.success('已移动')
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '移动失败')
  } finally {
    ops.move.saving = false
  }
}

function openNote(entry: { path: string; name: string; note: string }) {
  noteDialog.value = { visible: true, path: entry.path, name: entry.name, note: entry.note, saving: false }
}

async function saveNote() {
  const d = noteDialog.value
  d.saving = true
  try {
    await notesApi.set(shareId.value, d.path, d.note)
    d.visible = false
    ElMessage.success('备注已保存')
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '保存失败')
  } finally {
    d.saving = false
  }
}

function rawUrl(entry: { path: string }) {
  return `/api/shares/${shareId.value}/raw?path=${encodeURIComponent(entry.path)}`
}

// Preview opens in its own tab (full window, its own history) — a viewer inside
// a narrow list page wastes the space the files need. The new window's opener is
// cleared so it cannot script this page; if the browser blocks the popup, the
// same tab navigates instead of the click doing nothing.
function preview(entry: { path: string }) {
  const suffix = entry.path.split('/').map(encodeURIComponent).join('/')
  const url = `/s/${shareId.value}/preview/${suffix}`
  const win = window.open(url, '_blank')
  if (win) {
    win.opener = null
    return
  }
  void router.push(url)
}

function downloadZip() {
  window.location.href = `/api/shares/${shareId.value}/zip?path=${encodeURIComponent(currentPath.value)}`
}

function downloadZipOf(path: string) {
  window.location.href = `/api/shares/${shareId.value}/zip?path=${encodeURIComponent(path)}`
}

// Remix Icon class per entry; the icons ship with the bundle, so the page stays
// offline-capable (no icon CDN).
function fileIcon(entry: Entry) {
  if (entry.is_dir) return 'ri-folder-3-fill land-icon--folder'
  const ext = entry.name.split('.').pop()?.toLowerCase() ?? ''
  if (ext === 'pdf') return 'ri-file-pdf-2-fill land-icon--pdf'
  if (['doc', 'docx', 'rtf', 'odt'].includes(ext)) return 'ri-file-word-2-fill land-icon--word'
  if (['xls', 'xlsx', 'csv', 'ods'].includes(ext)) return 'ri-file-excel-2-fill land-icon--excel'
  if (['ppt', 'pptx', 'odp'].includes(ext)) return 'ri-file-ppt-2-fill land-icon--ppt'
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'svg', 'heic'].includes(ext)) return 'ri-image-2-fill land-icon--image'
  if (['zip', 'rar', '7z', 'tar', 'gz', 'xz', 'bz2'].includes(ext)) return 'ri-file-zip-fill land-icon--zip'
  if (['mp4', 'mkv', 'avi', 'mov', 'wmv', 'webm'].includes(ext)) return 'ri-movie-2-fill land-icon--video'
  if (['mp3', 'wav', 'flac', 'aac', 'ogg'].includes(ext)) return 'ri-music-2-fill land-icon--audio'
  if (['dwg', 'dxf', 'step', 'stp', 'igs', 'dwf'].includes(ext)) return 'ri-ruler-2-fill land-icon--cad'
  if (['md', 'txt', 'log', 'json', 'xml', 'yaml', 'yml', 'go', 'ts', 'js'].includes(ext)) return 'ri-file-text-fill land-icon--text'
  if (['exe', 'msi', 'dll', 'bat'].includes(ext)) return 'ri-file-code-fill land-icon--code'
  return 'ri-file-3-fill land-icon--file'
}

const rows = computed(() => (searchResult.value ? searchResult.value.entries : (listing.value?.entries ?? [])))
const writable = computed(() => listing.value?.writable ?? false)
const shareName = computed(() => listing.value?.share.name ?? '')

function openShare(share: PublicShare) {
  void router.push(`/s/${share.id}`)
}

function downloadShareZip(share: PublicShare) {
  window.location.href = `/api/shares/${share.id}/zip?path=`
}

watch([currentPath, isRoot], () => {
  page.value = 1
  clearSearch()
  void load()
})
onMounted(async () => {
  // The footer shows the running build so a deployment can be identified at a
  // glance; a failure here just leaves it blank.
  try {
    siteVersion.value = (await siteApi.get()).version
  } catch {
    // ignore: the listing is the primary content
  }
  await load()
})
</script>

<template>
  <el-card shadow="never" class="land-list-card">
    <div class="land-toolbar">
      <!-- The virtual root only lists shares; upload/mkdir/search need a real
           directory, so those controls appear once you are inside a share. -->
      <template v-if="!isRoot">
        <el-button v-if="writable" size="small" @click="pickFiles">
          <i class="ri-upload-cloud-2-line" /> 上传文件
        </el-button>
        <el-button v-if="writable" size="small" @click="pickDirectory">
          <i class="ri-folder-upload-line" /> 上传目录
        </el-button>
        <el-button v-if="writable" size="small" @click="openMkdir">
          <i class="ri-folder-add-line" /> 新建目录
        </el-button>
        <input ref="fileInput" type="file" multiple hidden @change="onFilesPicked" />
        <input ref="dirInput" type="file" webkitdirectory hidden @change="onDirectoryPicked" />

        <div class="land-toolbar__search">
          <el-input
            v-model="searchQuery"
            size="small"
            placeholder="在当前页面中搜索…"
            clearable
            @keyup.enter="runSearch"
            @clear="clearSearch"
          >
            <template #append>
              <el-button :loading="searching" @click="runSearch">搜索</el-button>
            </template>
          </el-input>
        </div>
      </template>

      <div style="flex: 1" />
      <el-button text size="small" :loading="loading" @click="load">
        <i class="ri-refresh-line" /> 刷新
      </el-button>
      <el-button v-if="!isRoot" text size="small" @click="downloadZip">
        <i class="ri-file-zip-line" /> 打包下载
      </el-button>
    </div>

    <div class="land-breadcrumb">
      <span v-if="isRoot" class="land-breadcrumb__current">根目录</span>
      <template v-else>
        <!-- The virtual root is the top of the tree, so a share is never the
             first crumb: users need a way back to "/" from inside a share. -->
        <a class="land-breadcrumb__link" @click="router.push('/')">根目录</a>
        <span class="land-breadcrumb__sep">/</span>
        <a class="land-breadcrumb__link" @click="goTo('')">{{ shareName || '共享' }}</a>
        <template v-for="crumb in breadcrumbs" :key="crumb.path">
          <span class="land-breadcrumb__sep">/</span>
          <a class="land-breadcrumb__link" @click="goTo(crumb.path)">{{ crumb.name }}</a>
        </template>
      </template>
    </div>

    <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon />

    <el-alert
      v-if="searchResult"
      :type="searchResult.timed_out ? 'warning' : searchResult.truncated ? 'info' : 'success'"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
      :title="`搜索 “${searchResult.query}” 命中 ${searchResult.entries.length} 项${searchResult.truncated ? '（已达上限，仅显示前 200 项）' : ''}${searchResult.depth_limited ? '（目录过深，部分层级未搜索）' : ''}${searchResult.timed_out ? '（超时，结果不完整）' : ''}`"
    >
      <el-button text size="small" @click="clearSearch">返回目录</el-button>
    </el-alert>

    <div v-if="uploads.length" style="margin-bottom: 12px">
      <div v-for="u in uploads" :key="u.name" style="margin-bottom: 8px">
        <div class="land-muted">
          {{ u.name }} · {{ percent(u) }}%
          <span v-if="u.error" style="color: var(--el-color-danger)">失败：{{ u.error }}</span>
        </div>
        <el-progress
          :percentage="percent(u)"
          :status="u.error ? 'exception' : u.done ? 'success' : undefined"
          :stroke-width="8"
        />
      </div>
    </div>

    <!-- Virtual root: each shared directory is a folder row. -->
    <el-table
      v-if="isRoot"
      :data="shares"
      v-loading="loading"
      empty-text="还没有共享目录"
      class="land-filetable"
      @row-dblclick="openShare"
    >
      <el-table-column label="文件夹" min-width="260">
        <template #default="{ row }">
          <i class="land-fileicon ri-folder-3-fill land-icon--folder" />
          <a class="land-file-link" @click="openShare(row)">{{ row.name }}</a>
        </template>
      </el-table-column>
      <el-table-column label="挂载路径" min-width="260">
        <template #default="{ row }"><span class="land-muted">{{ row.virtual_path }}</span></template>
      </el-table-column>
      <el-table-column label="操作" min-width="240" align="right">
        <template #default="{ row }">
          <el-button class="land-act land-act--view" size="small" title="打开" @click="openShare(row)">
            <i class="ri-folder-open-line" />
          </el-button>
          <el-button
            class="land-act land-act--download"
            size="small"
            title="打包下载整个共享"
            @click="downloadShareZip(row)"
          >
            <i class="ri-file-zip-line" />
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-table
      v-else
      :data="rows"
      v-loading="loading || searching"
      :empty-text="searchResult ? '没有匹配的文件名' : '这个目录是空的'"
      class="land-filetable"
      @row-dblclick="enter"
    >
      <el-table-column label="文件" min-width="260">
        <template #header>
          <span class="land-th">文件<span class="land-sort" :class="{ 'is-active': sort === 'name' }" @click="onSort('name')">
            {{ sort === 'name' ? (order === 'asc' ? '▲' : '▼') : '↕' }}
          </span></span>
        </template>
        <template #default="{ row }">
          <i class="land-fileicon" :class="fileIcon(row)" />
          <a v-if="!row.is_dir" class="land-file-link" @click="preview(row)">{{ row.name }}</a>
          <a v-else class="land-file-link" @click="enter(row)">{{ row.name }}</a>
        </template>
      </el-table-column>

      <el-table-column label="备注" min-width="260">
        <template #default="{ row }">
          <span class="land-muted">
            {{ row.note || '—' }}
            <el-button v-if="session.authenticated" text size="small" title="编辑备注" @click="openNote(row)">
              <i class="ri-sticky-note-line" />
            </el-button>
          </span>
        </template>
      </el-table-column>

      <el-table-column label="操作" min-width="240" align="right">
        <template #default="{ row }">
          <el-button
            v-if="!row.is_dir"
            class="land-act land-act--view"
            size="small"
            title="预览（新标签页）"
            @click="preview(row)"
          >
            <i class="ri-eye-line" />
          </el-button>
          <el-button
            v-if="row.is_dir"
            class="land-act land-act--download"
            size="small"
            title="打包下载"
            @click="downloadZipOf(row.path)"
          >
            <i class="ri-file-zip-line" />
          </el-button>
          <a v-if="!row.is_dir" :href="rawUrl(row)" class="land-act land-act--download" title="下载">
            <i class="ri-download-2-line" />
          </a>
          <el-button
            v-if="session.authenticated"
            class="land-act land-act--edit"
            size="small"
            title="重命名"
            @click="openRename(row)"
          >
            <i class="ri-edit-line" />
          </el-button>
          <el-button
            v-if="session.authenticated"
            class="land-act land-act--move"
            size="small"
            title="移动"
            @click="openMove(row)"
          >
            <i class="ri-drag-move-2-line" />
          </el-button>
          <el-button
            v-if="session.authenticated"
            class="land-act land-act--delete"
            size="small"
            title="删除"
            @click="removeEntry(row)"
          >
            <i class="ri-delete-bin-line" />
          </el-button>
        </template>
      </el-table-column>

      <el-table-column label="大小" min-width="130" align="right">
        <template #header>
          <span class="land-th">大小<span class="land-sort" :class="{ 'is-active': sort === 'size' }" @click="onSort('size')">
            {{ sort === 'size' ? (order === 'asc' ? '▲' : '▼') : '↕' }}
          </span></span>
        </template>
        <template #default="{ row }">
          <span class="land-muted">{{ humanSize(row.size, row.is_dir) }}</span>
        </template>
      </el-table-column>

      <el-table-column label="修改时间" min-width="260">
        <template #header>
          <span class="land-th">修改时间<span class="land-sort" :class="{ 'is-active': sort === 'mod_time' }" @click="onSort('mod_time')">
            {{ sort === 'mod_time' ? (order === 'asc' ? '▲' : '▼') : '↕' }}
          </span></span>
        </template>
        <template #default="{ row }">
          <span class="land-muted">{{ humanTime(row.mod_time) }}</span>
        </template>
      </el-table-column>

    </el-table>

    <el-pagination
      v-if="!isRoot && !searchResult && (listing?.total ?? 0) > pageSize"
      style="margin-top: 16px; justify-content: flex-end"
      layout="total, prev, pager, next, sizes"
      :total="listing?.total ?? 0"
      :page-size="pageSize"
      :current-page="page"
      :page-sizes="[50, 100, 200]"
      @current-change="onPageChange"
      @size-change="onSizeChange"
    />

    <div class="land-footer">
      <span>lan disk · {{ siteVersion }}</span>
      <span>简体中文</span>
    </div>
  </el-card>

  <el-dialog v-model="noteDialog.visible" :title="`备注 · ${noteDialog.name}`" width="480px">
    <el-input
      v-model="noteDialog.note"
      type="textarea"
      :rows="4"
      maxlength="2000"
      show-word-limit
      placeholder="例如：2024-03 终版，勿引用旧版"
    />
    <p class="land-muted" style="margin: 8px 0 0">清空即可删除备注；重命名或移动后备注会跟随文件。</p>
    <template #footer>
      <el-button @click="noteDialog.visible = false">取消</el-button>
      <el-button type="primary" :loading="noteDialog.saving" @click="saveNote">保存</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="ops.mkdir.visible" title="新建目录" width="420px">
    <el-input v-model="ops.mkdir.name" placeholder="目录名" @keyup.enter="submitMkdir" />
    <template #footer>
      <el-button @click="ops.mkdir.visible = false">取消</el-button>
      <el-button type="primary" :loading="ops.mkdir.saving" @click="submitMkdir">创建</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="ops.rename.visible" title="重命名" width="420px">
    <el-input v-model="ops.rename.name" @keyup.enter="submitRename" />
    <template #footer>
      <el-button @click="ops.rename.visible = false">取消</el-button>
      <el-button type="primary" :loading="ops.rename.saving" @click="submitRename">保存</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="ops.move.visible" title="移动到目录" width="480px">
    <el-input v-model="ops.move.to" placeholder="目标目录（留空表示共享根目录）">
      <template #append>
        <el-button @click="pickerVisible = true">浏览…</el-button>
      </template>
    </el-input>
    <p class="land-muted" style="margin: 8px 0 0">只能在同一共享内移动；目标目录必须已存在。</p>
    <template #footer>
      <el-button @click="ops.move.visible = false">取消</el-button>
      <el-button type="primary" :loading="ops.move.saving" @click="submitMove">移动</el-button>
    </template>
  </el-dialog>

  <DirPickerDialog
    v-model="pickerVisible"
    mode="share"
    title="选择目标目录"
    :share-id="shareId"
    :start-path="ops.move.to"
    @confirm="(path: string) => (ops.move.to = path)"
  />
</template>
