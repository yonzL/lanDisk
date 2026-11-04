<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ApiError, sharesApi, type ShareView } from '../api'
import DirPickerDialog from '../components/DirPickerDialog.vue'

const views = ref<ShareView[]>([])
const loading = ref(false)
const error = ref('')

const dialog = reactive({
  visible: false,
  editing: false,
  id: 0,
  name: '',
  virtualPath: '',
  root: '',
  rootPerm: 'r',
  sort: 0,
  saving: false,
})

const rulesDialog = reactive({ visible: false, shareId: 0, shareName: '' })
const ruleForm = reactive({ relPath: '', perm: 'rw', saving: false })

const permLabel: Record<string, string> = { none: '不可见', r: '只读', rw: '可读写' }
const pickerVisible = ref(false)
const pickerMode = ref<'create' | 'root'>('create')

async function load() {
  loading.value = true
  error.value = ''
  try {
    views.value = await sharesApi.list()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

// Creating a share starts with the folder: the picker opens first, and its
// choice prefills the name and virtual path (both remain editable).
function openCreate() {
  Object.assign(dialog, {
    visible: false,
    editing: false,
    id: 0,
    name: '',
    virtualPath: '',
    root: '',
    rootPerm: 'r',
    sort: 0,
    saving: false,
  })
  pickerMode.value = 'create'
  pickerVisible.value = true
}

// The share name defaults to the picked folder's last segment — never the full
// path. A bare drive root ("D:\") is not a usable name, so it becomes "D盘".
function basename(path: string) {
  const trimmed = path.replace(/[\\/]+$/, '')
  const parts = trimmed.split(/[\\/]/).filter(Boolean)
  const last = parts.pop() ?? ''
  if (/^[a-zA-Z]:$/.test(last)) return `${last.slice(0, 1).toUpperCase()}盘`
  return last
}

function defaultVirtualPath(name: string) {
  const safe = name.replace(/[\\/:*?"<>|]/g, '').trim()
  return safe ? `/${safe}` : ''
}

function onDirPicked(path: string) {
  if (pickerMode.value === 'create') {
    dialog.root = path
    const name = basename(path)
    dialog.name = name
    dialog.virtualPath = defaultVirtualPath(name)
    dialog.visible = true
    return
  }
  // Re-picking from inside the dialog: fill only what the operator left blank.
  dialog.root = path
  if (!dialog.name.trim()) dialog.name = basename(path)
  if (!dialog.virtualPath.trim()) dialog.virtualPath = defaultVirtualPath(dialog.name)
}

function openEdit(view: ShareView) {
  const rootRule = view.rules.find((r) => r.rel_path === '')
  Object.assign(dialog, {
    visible: true,
    editing: true,
    id: view.share.id,
    name: view.share.name,
    virtualPath: view.share.virtual_path,
    root: view.share.root,
    rootPerm: rootRule?.perm ?? 'r',
    sort: view.share.sort,
    saving: false,
  })
}

async function save() {
  dialog.saving = true
  try {
    if (dialog.editing) {
      await sharesApi.update(dialog.id, {
        name: dialog.name,
        virtual_path: dialog.virtualPath,
        sort: dialog.sort,
      })
      await sharesApi.setRule(dialog.id, { rel_path: '', perm: dialog.rootPerm })
      ElMessage.success('已保存')
    } else {
      await sharesApi.create({
        name: dialog.name,
        virtual_path: dialog.virtualPath,
        root: dialog.root,
        root_perm: dialog.rootPerm,
      })
      ElMessage.success('已创建')
    }
    dialog.visible = false
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '保存失败')
  } finally {
    dialog.saving = false
  }
}

async function remove(view: ShareView) {
  try {
    await ElMessageBox.confirm(
      `删除共享 ${view.share.name}？磁盘上的文件不会被删除，但备注与权限规则会一并移除。`,
      '确认删除',
      { type: 'warning' },
    )
  } catch {
    return
  }
  try {
    await sharesApi.remove(view.share.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '删除失败')
  }
}

function openRules(view: ShareView) {
  rulesDialog.visible = true
  rulesDialog.shareId = view.share.id
  rulesDialog.shareName = view.share.name
  ruleForm.relPath = ''
  ruleForm.perm = 'rw'
}

function rulesOf(shareId: number) {
  return views.value.find((v) => v.share.id === shareId)?.rules ?? []
}

async function addRule() {
  if (!ruleForm.relPath.trim()) {
    ElMessage.warning('请填写子目录相对路径（例如 公共）')
    return
  }
  ruleForm.saving = true
  try {
    await sharesApi.setRule(rulesDialog.shareId, {
      rel_path: ruleForm.relPath.trim(),
      perm: ruleForm.perm,
    })
    ruleForm.relPath = ''
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '保存失败')
  } finally {
    ruleForm.saving = false
  }
}

async function removeRule(relPath: string) {
  try {
    await sharesApi.deleteRule(rulesDialog.shareId, relPath)
    await load()
  } catch (e) {
    ElMessage.error(e instanceof ApiError ? e.message : '删除失败')
  }
}

onMounted(load)
</script>

<template>
  <el-card shadow="never">
    <template #header>
      <div style="display: flex; align-items: center">
        <span>共享目录</span>
        <div style="flex: 1" />
        <el-button text :loading="loading" @click="load">刷新</el-button>
        <el-button type="primary" @click="openCreate">新建共享</el-button>
      </div>
    </template>

    <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon style="margin-bottom: 12px" />

    <el-table :data="views" v-loading="loading" empty-text="还没有共享目录">
      <el-table-column label="名称" min-width="140">
        <template #default="{ row }">{{ row.share.name }}</template>
      </el-table-column>
      <el-table-column label="虚拟路径" min-width="140">
        <template #default="{ row }"><code>{{ row.share.virtual_path }}</code></template>
      </el-table-column>
      <el-table-column label="目录" min-width="240">
        <template #default="{ row }"><span class="land-muted">{{ row.share.root }}</span></template>
      </el-table-column>
      <el-table-column label="根权限" width="110">
        <template #default="{ row }">
          {{ permLabel[rulesOf(row.share.id).find((r) => r.rel_path === '')?.perm ?? 'none'] }}
        </template>
      </el-table-column>
      <el-table-column label="操作" width="230" align="right">
        <template #default="{ row }">
          <el-button text type="primary" @click="openRules(row)">权限规则</el-button>
          <el-button text @click="openEdit(row)">编辑</el-button>
          <el-button text type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
  </el-card>

  <el-dialog v-model="dialog.visible" :title="dialog.editing ? '编辑共享' : '新建共享'" width="560px">
    <el-form label-position="top">
      <el-form-item label="名称">
        <el-input v-model="dialog.name" placeholder="图纸" />
      </el-form-item>
      <el-form-item label="虚拟路径">
        <el-input v-model="dialog.virtualPath" placeholder="/图纸" />
      </el-form-item>
      <el-form-item v-if="!dialog.editing" label="共享目录（本机路径，必须已存在）">
        <div style="display: flex; gap: 8px; width: 100%">
          <el-input v-model="dialog.root" placeholder="D:\drawing" />
          <el-button @click="pickerMode = 'root'; pickerVisible = true">浏览…</el-button>
        </div>
      </el-form-item>
      <el-form-item v-else label="共享目录">
        <el-input :model-value="dialog.root" disabled />
        <div class="land-muted">目录创建后不可更改（备注、规则与回收站都按此路径记录）。</div>
      </el-form-item>
      <el-form-item label="匿名访问者对根目录的权限">
        <el-select v-model="dialog.rootPerm" style="width: 100%">
          <el-option label="只读" value="r" />
          <el-option label="可读写（可上传、可新建文件夹）" value="rw" />
          <el-option label="不可见" value="none" />
        </el-select>
      </el-form-item>
    </el-form>
    <template #footer>
      <el-button @click="dialog.visible = false">取消</el-button>
      <el-button type="primary" :loading="dialog.saving" @click="save">保存</el-button>
    </template>
  </el-dialog>

  <el-drawer v-model="rulesDialog.visible" :title="`${rulesDialog.shareName} · 权限规则`" size="480px">
    <p class="land-muted">
      规则按目录前缀生效：更深的规则覆盖上层；未匹配任何规则的目录对匿名访问者不可见。
    </p>
    <el-table :data="rulesOf(rulesDialog.shareId)" size="small">
      <el-table-column label="子目录" min-width="160">
        <template #default="{ row }">{{ row.rel_path === '' ? '（根目录）' : row.rel_path }}</template>
      </el-table-column>
      <el-table-column label="权限" width="110">
        <template #default="{ row }">{{ permLabel[row.perm] }}</template>
      </el-table-column>
      <el-table-column width="80" align="right">
        <template #default="{ row }">
          <el-button text type="danger" :disabled="row.rel_path === ''" @click="removeRule(row.rel_path)">
            删除
          </el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-divider>新增 / 覆盖子目录规则</el-divider>
    <el-form label-position="top">
      <el-form-item label="子目录相对路径">
        <el-input v-model="ruleForm.relPath" placeholder="公共/上传区" />
      </el-form-item>
      <el-form-item label="权限">
        <el-select v-model="ruleForm.perm" style="width: 100%">
          <el-option label="只读" value="r" />
          <el-option label="可读写" value="rw" />
          <el-option label="不可见" value="none" />
        </el-select>
      </el-form-item>
      <el-button type="primary" :loading="ruleForm.saving" @click="addRule">保存规则</el-button>
    </el-form>
  </el-drawer>

  <DirPickerDialog
    v-model="pickerVisible"
    mode="machine"
    title="选择共享目录"
    :start-path="dialog.root"
    @confirm="onDirPicked"
  />
</template>
