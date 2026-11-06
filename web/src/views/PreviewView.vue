<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

// FileViewer is loaded on demand: nothing of it ships in the first-screen bundle.
type ViewerController = import('@file-viewer/core/browser').ViewerController

const route = useRoute()
const router = useRouter()

const shareId = computed(() => Number(route.params.id))
const filePath = computed(() => {
  const raw = route.params.pathMatch
  const joined = Array.isArray(raw) ? raw.join('/') : (raw ?? '')
  return joined.replace(/^\/+|\/+$/g, '')
})
const fileName = computed(() => filePath.value.split('/').pop() ?? '')
const dirPath = computed(() => {
  const parts = filePath.value.split('/')
  parts.pop()
  return parts.join('/')
})
// Download keeps the attachment response; the viewer gets the streaming endpoint
// (inline + correct MIME + Range) so large video starts playing immediately
// instead of being buffered in full first.
const rawUrl = computed(
  () => `/api/shares/${shareId.value}/raw?path=${encodeURIComponent(filePath.value)}`,
)
const streamUrl = computed(
  () => `/api/shares/${shareId.value}/content?path=${encodeURIComponent(filePath.value)}`,
)

// Formats no Chromium-based browser can decode (WMV/WMA, RealVideo, AVI, FLV …).
// Showing the reason and a download button beats a demuxer error message.
const UNDECODABLE = /\.(wmv|wma|asf|avi|rmvb?|flv|mpg|mpeg|vob|ts|m2ts|3gp|ogm|divx|xvid)$/i
const undecodable = computed(() => UNDECODABLE.test(fileName.value))

// Video and audio are played by a native media element instead of FileViewer's
// media renderer: that renderer is handed the whole file as a buffer, so a 2 GB
// video would download completely before the first frame. A native <video src>
// streams over Range requests and starts immediately.
const PLAYABLE_VIDEO = /\.(mp4|m4v|webm|ogv|mov|mkv)$/i
const PLAYABLE_AUDIO = /\.(mp3|wav|m4a|aac|flac|ogg|opus)$/i
const mediaKind = computed<'video' | 'audio' | ''>(() => {
  if (PLAYABLE_VIDEO.test(fileName.value)) return 'video'
  if (PLAYABLE_AUDIO.test(fileName.value)) return 'audio'
  return ''
})
const mediaError = ref(false)

const host = ref<HTMLElement | null>(null)
const wrapper = ref<HTMLElement | null>(null)
const loading = ref(true)
const error = ref('')
const fullscreen = ref(false)
let controller: ViewerController | null = null

// Every runtime asset is named explicitly. Relying on the "global asset base"
// alone was fragile: in a field deployment the PDF worker resolved to
// /vendor/pdf/pdf.worker.mjs (site root, missing the /file-viewer prefix) and
// PDF preview died with "Setting up fake worker failed". Explicit URLs make the
// path independent of the document base, the router base and any proxy prefix.
const ASSET = '/file-viewer/'
const PDF = ASSET + 'vendor/pdf/'
const CAD_BASE = ASSET + 'wasm/cad/0.8.3/'
const MODEL_BASE = ASSET + 'wasm/model/'

// escapeViewerName keeps '#' and '?' out of the filename the viewer parses; it
// percent-decodes names before deciding the format, so the original name survives.
function escapeViewerName(name: string) {
  return name.replace(/#/g, '%23').replace(/\?/g, '%3F')
}

async function mount() {
  if (undecodable.value || mediaKind.value || !host.value) return
  loading.value = true
  error.value = ''
  controller?.destroy()
  controller = null
  try {
    const [{ mountViewer }, { standardRenderers, setDefaultFullAssetBaseUrl }, { engineeringRenderers }] =
      await Promise.all([
        import('@file-viewer/web'),
        import('@file-viewer/preset-standard'),
        import('@file-viewer/preset-engineering'),
      ])

    // Keep the global base too: it covers renderers whose assets we cannot name.
    setDefaultFullAssetBaseUrl('/file-viewer')

    const preset = {
      id: 'land-disk',
      label: 'lan disk 预览',
      renderers: [...standardRenderers.renderers, ...engineeringRenderers.renderers],
    }
    controller = mountViewer(host.value, {
      url: streamUrl.value,
      // The viewer derives the format from `name`, but it splits the string on
      // '#' and '?' as if it were a URL. A file literally called "#说明.pdf" then
      // becomes "" and falls back to preview.bin (binary inspector). Percent-
      // encoding those two characters keeps the real name once the viewer
      // decodes it, and the extension is read from the encoded form either way.
      name: escapeViewerName(fileName.value),
      options: {
        preset,
        theme: 'light',
        pdf: {
          assetBaseUrl: ASSET,
          workerUrl: PDF + 'pdf.worker.mjs',
          cMapUrl: PDF + 'cmaps/',
          wasmUrl: PDF + 'wasm/',
          standardFontDataUrl: PDF + 'standard_fonts/',
        },
        docx: {
          workerUrl: ASSET + 'vendor/docx/docx.worker.js',
          workerJsZipUrl: ASSET + 'vendor/docx/jszip.min.js',
        },
        presentation: {
          workerUrl: ASSET + 'vendor/pptx/pptx.worker.js',
        },
        spreadsheet: {
          workerUrl: ASSET + 'vendor/xlsx/sheet.worker.js',
        },
        cad: {
          workerUrl: CAD_BASE + 'dwg-worker.js',
          wasmPath: CAD_BASE,
          dwfWasmUrl: CAD_BASE + 'dwfv-render.wasm',
        },
        model: {
          workerUrl: MODEL_BASE + 'occt-worker.js',
          runtimeUrl: MODEL_BASE + 'occt-import-js.js',
          wasmUrl: MODEL_BASE + 'occt-import-js.wasm',
        },
        archive: {
          // Only the worker URL: the libarchive bundle resolves libarchive.wasm
          // relative to its own location, and the renderer's wasm-URL patch does
          // not match this bundle's emscripten `locateFile` (it would break it).
          workerUrl: ASSET + 'vendor/libarchive/worker-bundle.js',
        },
      },
    })
  } catch (e) {
    error.value = e instanceof Error ? e.message : '预览器加载失败'
  } finally {
    loading.value = false
  }
}

function syncFullscreen() {
  fullscreen.value = document.fullscreenElement === wrapper.value
}

async function toggleFullscreen() {
  try {
    if (document.fullscreenElement) {
      await document.exitFullscreen()
    } else {
      await wrapper.value?.requestFullscreen()
    }
  } catch {
    // Fullscreen can be refused (permissions, iframe); the page is already
    // full-bleed on its own, so there is nothing else to fall back to.
  }
  syncFullscreen()
}

onMounted(() => {
  void mount()
  document.addEventListener('fullscreenchange', syncFullscreen)
})
watch(() => route.fullPath, mount)
onBeforeUnmount(() => {
  controller?.destroy()
  document.removeEventListener('fullscreenchange', syncFullscreen)
})

function back() {
  const suffix = dirPath.value ? `/${dirPath.value.split('/').map(encodeURIComponent).join('/')}` : ''
  void router.push(`/s/${shareId.value}${suffix}`)
}
</script>

<template>
  <div ref="wrapper" class="land-preview">
    <div class="land-preview__bar">
      <el-button text @click="back">← 返回目录</el-button>
      <span class="land-preview__name">{{ fileName }}</span>
      <div style="flex: 1" />
      <el-button text @click="toggleFullscreen">
        {{ fullscreen ? '退出全屏' : '全屏' }}
      </el-button>
      <a :href="rawUrl" class="land-preview__download">下载</a>
    </div>

    <el-alert v-if="error" type="error" :closable="false" :title="error" show-icon />

    <!-- Formats Chromium cannot decode: say so, and offer the download. -->
    <div v-if="undecodable" class="land-preview__unsupported">
      <i class="ri-file-video-line" />
      <h3>浏览器无法播放该格式</h3>
      <p class="land-muted">
        {{ fileName }} 使用了浏览器不支持的解码（如 WMV / RealVideo / AVI 等）。
        请下载后用本地播放器打开。
      </p>
      <a :href="rawUrl" class="el-button el-button--primary">下载文件</a>
    </div>

    <!-- Native media elements stream over Range; the FileViewer media renderer
         would buffer the whole file first. -->
    <template v-else-if="mediaKind">
      <video
        v-if="mediaKind === 'video'"
        class="land-media"
        :src="streamUrl"
        controls
        autoplay
        preload="metadata"
        @error="mediaError = true"
      />
      <audio v-else class="land-media land-media--audio" :src="streamUrl" controls autoplay preload="metadata" @error="mediaError = true" />
      <el-alert
        v-if="mediaError"
        type="warning"
        :closable="false"
        show-icon
        title="无法播放：可能是浏览器不支持的编码（如 H.265 / ProRes / 无损音频），请下载后用本地播放器打开"
      />
    </template>

    <div v-else v-loading="loading" ref="host" class="land-preview__host" />
  </div>
</template>

<style scoped>
.land-preview {
  display: flex;
  flex-direction: column;
  height: calc(100vh - 76px);
}
.land-preview:fullscreen {
  height: 100vh;
  padding: 0 12px 12px;
  background: #fff;
}
.land-preview__bar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 0;
  flex: 0 0 auto;
}
.land-preview__name {
  font-weight: 600;
}
.land-preview__download {
  color: var(--el-color-primary);
  text-decoration: none;
}
.land-preview__host {
  flex: 1 1 auto;
  min-height: 0;
  background: #fff;
  border: 1px solid var(--land-border);
  border-radius: 8px;
  overflow: hidden;
}
</style>
