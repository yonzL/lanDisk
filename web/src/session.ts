import { reactive } from 'vue'
import { authApi, setUnauthorizedHandler, type Session } from './api'

// One shared session state for the header, the login page and the router guard.
// The server is the source of truth; this is a cache with a refresh.
export const session = reactive<Session>({ authenticated: false })

/** Fired when the server stops accepting our cookie; the shell redirects to login. */
export const SESSION_EXPIRED_EVENT = 'land:session-expired'

let loaded = false

export async function refreshSession(): Promise<Session> {
  const next = await authApi.session()
  apply(next)
  loaded = true
  return next
}

export async function ensureSession(): Promise<Session> {
  if (loaded) return session
  return refreshSession()
}

export function apply(next: Session) {
  session.authenticated = next.authenticated
  session.must_change = next.must_change ?? false
  session.username = next.username
}

export async function login(username: string, password: string) {
  apply(await authApi.login(username, password))
  loaded = true
}

export async function logout() {
  apply(await authApi.logout())
  loaded = true
}

export function forgetSession() {
  loaded = false
  apply({ authenticated: false })
}

// The admin stays signed in until they log out, so the only way a live session
// ends under them is a 401 (credentials changed elsewhere, database replaced,
// server reset). Drop the cache and let the shell send them to the login page.
setUnauthorizedHandler(() => {
  forgetSession()
  window.dispatchEvent(new CustomEvent(SESSION_EXPIRED_EVENT))
})
