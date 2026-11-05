// End-to-end check of the login flow in a real browser: fill the form, submit,
// and report whether the page behind it reflects the new session without a manual
// reload (the header switches to the account menu, and a write-permitted share
// gains its upload buttons).
// Usage: bun scripts/cdp-login.mjs <baseUrl> <username> <password>
const [baseUrl, username, password] = process.argv.slice(2)
const port = 9338

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
ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data)
  if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg)
    pending.delete(msg.id)
  }
})
const send = (method, params = {}) =>
  new Promise((resolve) => {
    const id = nextId++
    pending.set(id, resolve)
    ws.send(JSON.stringify({ id, method, params }))
  })

await new Promise((resolve) => ws.addEventListener('open', resolve))
await send('Page.enable')
await send('Runtime.enable')
await send('Network.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })

// 1. Anonymous visit to the share.
await send('Page.navigate', { url: `${baseUrl}/s/1` })
await Bun.sleep(2500)
const before = await send('Runtime.evaluate', {
  expression: `JSON.stringify({
    path: location.pathname,
    accountMenu: !!document.querySelector('.land-user'),
    uploadButtons: [...document.querySelectorAll('.land-toolbar button')].map(b => b.textContent.trim()).filter(Boolean),
  })`,
  returnByValue: true,
})
console.log('anonymous:', before.result?.result?.value)

// 2. Log in through the real form.
await send('Page.navigate', { url: `${baseUrl}/login` })
await Bun.sleep(1500)
await send('Runtime.evaluate', {
  expression: `(() => {
    const set = (el, value) => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
      setter.call(el, value)
      el.dispatchEvent(new Event('input', { bubbles: true }))
    }
    const user = document.querySelector('input[autocomplete="username"]')
    const pass = document.querySelector('input[autocomplete="current-password"]')
    if (!user || !pass) return 'inputs missing'
    set(user, ${JSON.stringify(username)})
    set(pass, ${JSON.stringify(password)})
    const submit = document.querySelector('form button') || [...document.querySelectorAll('button')].find(b => b.textContent.includes('登录'))
    submit.click()
    return 'submitted'
  })()`,
  returnByValue: true,
})
await Bun.sleep(3000)

const after = await send('Runtime.evaluate', {
  expression: `JSON.stringify({
    path: location.pathname,
    accountMenu: !!document.querySelector('.land-user'),
    uploadButtons: [...document.querySelectorAll('.land-toolbar button')].map(b => b.textContent.trim()).filter(Boolean),
  })`,
  returnByValue: true,
})
console.log('after login:', after.result?.result?.value)

ws.close()
edge.kill()
process.exit(0)
