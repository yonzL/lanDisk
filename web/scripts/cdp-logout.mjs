// Checks that logging out from a share page updates the page in place: the
// account menu disappears and the admin-only upload buttons go away, without a
// manual reload.
// Usage: bun scripts/cdp-logout.mjs <baseUrl> <username> <password>
const [baseUrl, username, password] = process.argv.slice(2)
const port = 9339

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

const snapshot = async () => {
  const res = await send('Runtime.evaluate', {
    expression: `JSON.stringify({
      path: location.pathname,
      accountMenu: !!document.querySelector('.land-user'),
      loginButton: [...document.querySelectorAll('header button')].some(b => b.textContent.trim() === '登录'),
      toolbar: [...document.querySelectorAll('.land-toolbar button')].map(b => b.textContent.trim()).filter(Boolean),
    })`,
    returnByValue: true,
  })
  return res.result?.result?.value
}

await new Promise((resolve) => ws.addEventListener('open', resolve))
await send('Page.enable')
await send('Runtime.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })

// Log in through the form (the flow under test for issue 2), then open a share.
await send('Page.navigate', { url: `${baseUrl}/login` })
await Bun.sleep(1500)
await send('Runtime.evaluate', {
  expression: `(() => {
    const set = (el, value) => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set
      setter.call(el, value)
      el.dispatchEvent(new Event('input', { bubbles: true }))
    }
    set(document.querySelector('input[autocomplete="username"]'), ${JSON.stringify(username)})
    set(document.querySelector('input[autocomplete="current-password"]'), ${JSON.stringify(password)})
    document.querySelector('form button').click()
    return true
  })()`,
  returnByValue: true,
})
await Bun.sleep(2500)
await send('Page.navigate', { url: `${baseUrl}/s/1` })
await Bun.sleep(2500)
console.log('logged in on share:', await snapshot())

// Open the account menu and click 退出登录.
await send('Runtime.evaluate', {
  expression: `document.querySelector('.land-user').click()`,
  returnByValue: true,
})
await Bun.sleep(800)
await send('Runtime.evaluate', {
  expression: `(() => {
    const item = [...document.querySelectorAll('.el-dropdown-menu__item')].find(el => el.textContent.includes('退出登录'))
    if (!item) return 'menu item missing'
    item.click()
    return 'clicked'
  })()`,
  returnByValue: true,
})
await Bun.sleep(2500)
console.log('after logout:', await snapshot())

ws.close()
edge.kill()
process.exit(0)
