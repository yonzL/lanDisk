// Guards against an exe whose version number and embedded frontend disagree.
//
// `go build` embeds whatever is in web/dist at that moment (`//go:embed`), while
// the version string comes from -ldflags. Building without `bun run build` after
// touching web/src therefore produces a binary that *says* 1.2.x but serves an
// older bundle — which shows up in the field as a stale worker URL such as
// /vendor/pdf/pdf.worker.mjs. This check fails the build instead.
//
// Usage: bun scripts/check-dist-fresh.mjs
import { stat } from 'node:fs/promises'
import { readdir } from 'node:fs/promises'
import { join } from 'node:path'

const newest = async (dir) => {
  let latest = 0
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      latest = Math.max(latest, await newest(path))
      continue
    }
    const info = await stat(path)
    latest = Math.max(latest, info.mtimeMs)
  }
  return latest
}

const distIndex = 'dist/index.html'
let distTime = 0
try {
  distTime = (await stat(distIndex)).mtimeMs
} catch {
  console.error(`✗ ${distIndex} 不存在：先运行 bun run build，再 go build。`)
  process.exit(1)
}

const sources = [await newest('src')]
for (const file of ['index.html', 'package.json', 'vite.config.ts']) {
  try {
    sources.push((await stat(file)).mtimeMs)
  } catch {
    // optional file
  }
}
const newestSource = Math.max(...sources)

if (newestSource > distTime) {
  console.error(
    `✗ web/dist 比源码旧（dist ${new Date(distTime).toISOString()}，源码 ${new Date(newestSource).toISOString()}）。\n` +
      '  这个 exe 会带着旧前端与新版本号，请先 bun run build 再 go build。',
  )
  process.exit(1)
}

console.log(`ok  web/dist 不早于源码（${new Date(distTime).toISOString()}）`)
