// Opens a preview page, waits, then reports: console errors, the viewer's text
// content (shadow DOM included), and the asset requests it made.
// Usage: bun scripts/cdp-probe.mjs <url> [waitMs]
const [url, waitMsRaw] = process.argv.slice(2)
const waitMs = Number(waitMsRaw ?? 12000)
const port = 9337

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
      // not up yet
    }
    await Bun.sleep(200)
  }
  throw new Error('devtools endpoint did not come up')
}

const page = await findPage()
const ws = new WebSocket(page.webSocketDebuggerUrl)
let nextId = 1
const pending = new Map()
const consoleLines = []
const requests = []

ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data)
  if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg)
    pending.delete(msg.id)
    return
  }
  if (msg.method === 'Runtime.consoleAPICalled') {
    consoleLines.push(`${msg.params.type}: ${msg.params.args.map((a) => a.value ?? a.description ?? '').join(' ')}`)
  }
  if (msg.method === 'Runtime.exceptionThrown') {
    consoleLines.push(`exception: ${msg.params.exceptionDetails.text}`)
  }
  if (msg.method === 'Network.requestWillBeSent') requests.push(msg.params.request.url)
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

// Collect text from the page and any shadow roots (the viewer uses Shadow DOM).
const textProbe = await send('Runtime.evaluate', {
  expression: `(() => {
    const out = []
    const walk = (root) => {
      for (const el of root.querySelectorAll('*')) {
        if (el.shadowRoot) walk(el.shadowRoot)
        if (el.children.length === 0 && el.textContent && el.textContent.trim()) out.push(el.textContent.trim())
      }
    }
    walk(document)
    return [...new Set(out)].join(' | ')
  })()`,
  returnByValue: true,
})

console.log('--- console:')
for (const line of consoleLines.slice(0, 15)) console.log('  ', line.slice(0, 400))
console.log('--- page text (unique leaves):')
console.log('  ', (textProbe.result?.result?.value ?? '').slice(0, 1200))
console.log('--- viewer asset requests:')
for (const u of requests.filter((u) => /vendor|wasm|worker|\.mjs/.test(u)).slice(0, 10)) console.log('  ', u)

ws.close()
edge.kill()
process.exit(0)
