// Opens a preview URL in headless Edge, records every network request, and prints
// the ones that belong to the preview runtime (workers / wasm / vendor assets).
// Usage: bun scripts/cdp-network.mjs <url> [waitMs] [screenshot.png]
const [url, waitMsRaw, shotPath] = process.argv.slice(2)
const waitMs = Number(waitMsRaw ?? 12000)
const port = 9336

const edge = Bun.spawn(
  [
    'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
    '--headless=new',
    '--disable-gpu',
    '--no-sandbox',
    '--hide-scrollbars',
    `--remote-debugging-port=${port}`,
    `--user-data-dir=${(await Bun.$`mktemp -d`.text()).trim()}`,
    'about:blank',
  ],
  { stdout: 'ignore', stderr: 'ignore' },
)

async function findPage() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()
      const page = list.find((t) => t.type === 'page')
      if (page) return page
    } catch {
      // devtools not up yet
    }
    await Bun.sleep(200)
  }
  throw new Error('devtools endpoint did not come up')
}

const page = await findPage()
const ws = new WebSocket(page.webSocketDebuggerUrl)
let nextId = 1
const pending = new Map()
const requests = []
const failures = []
const consoleErrors = []

ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data)
  if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg)
    pending.delete(msg.id)
    return
  }
  if (msg.method === 'Network.requestWillBeSent') {
    requests.push(msg.params.request.url)
  }
  if (msg.method === 'Network.loadingFailed') {
    failures.push(`${msg.params.errorText} (${msg.params.blockedReason ?? 'no reason'})`)
  }
  if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
    consoleErrors.push(msg.params.args.map((a) => a.value ?? a.description ?? '').join(' '))
  }
})

const send = (method, params = {}) =>
  new Promise((resolve) => {
    const id = nextId++
    pending.set(id, resolve)
    ws.send(JSON.stringify({ id, method, params }))
  })

await new Promise((resolve) => ws.addEventListener('open', resolve))
await send('Runtime.enable')
await send('Page.enable')
await send('Network.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })
await send('Page.navigate', { url })
await Bun.sleep(waitMs)

const runtime = requests.filter((u) => /\/(vendor|wasm)\/|worker|cmaps|standard_fonts/.test(u))
const outside = runtime.filter((u) => !u.includes('/file-viewer/'))
const origin = new URL(url).origin

console.log(`runtime asset requests: ${runtime.length}`)
for (const u of runtime.slice(0, 12)) console.log('  ', u.replace(origin, ''))
console.log(outside.length ? `MIS-PREFIXED: ${outside.length}` : 'all runtime assets under /file-viewer/')
for (const u of outside.slice(0, 5)) console.log('   ,', u)

const bad = requests.filter((u) => /\/vendor\/|\/wasm\//.test(new URL(u).pathname) && !u.includes('/file-viewer/'))
console.log(bad.length ? `URLs missing the prefix: ${bad.length}` : 'no root-relative asset URLs')
if (failures.length) console.log('network failures:', failures.slice(0, 5))
if (consoleErrors.length) console.log('console errors:', consoleErrors.slice(0, 5))

if (shotPath) {
  const shot = await send('Page.captureScreenshot', { format: 'png' })
  await Bun.write(shotPath, Buffer.from(shot.result.data, 'base64'))
}

ws.close()
edge.kill()
process.exit(outside.length || consoleErrors.length ? 1 : 0)
