// Guards the filename workaround in PreviewView. The viewer's normalizeFilename()
// treats `name` as a URL and cuts it at '#'/'?' — a file called "#x.mp4" then
// becomes "preview.bin" and the viewer picks the binary inspector. PreviewView
// percent-encodes those two characters before handing the name over; this script
// asserts both halves of that contract.
import { getExtension, normalizeFilename } from '../node_modules/@file-viewer/core/dist/source/index.js'

const escapeViewerName = (name) => name.replace(/#/g, '%23').replace(/\?/g, '%3F')

const cases = [
  ['#知识分享官#【通信课】-第2讲.mp4', 'mp4'],
  ['普通视频.mp4', 'mp4'],
  ['a?b.pdf', 'pdf'],
]

let failed = 0
for (const [name, wantExt] of cases) {
  const escaped = escapeViewerName(name)
  const ext = getExtension(escaped)
  const shown = normalizeFilename(escaped)
  const ok = ext === wantExt && shown === name
  if (!ok) failed++
  console.log(`${ok ? 'ok  ' : 'FAIL'} ${JSON.stringify(name)} → ext=${ext} name=${JSON.stringify(shown)}`)
}

// Document why the workaround exists: the raw name normalises to the fallback
// ("preview.bin"), which is what turned the field video into a .bin.
const rawShown = normalizeFilename('#知识分享官#【通信课】-第2讲.mp4')
console.log(`note: unescaped name normalises to ${JSON.stringify(rawShown)} (viewer falls back to preview.bin)`)
if (rawShown !== 'preview.bin') {
  console.log('note: library parsing changed; re-check whether the workaround is still needed')
}

if (failed) process.exit(1)
