// Single place that knows how the backend talks: JSON in, JSON out, and the
// {code, message} error shape every endpoint uses.

export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message)
    this.name = 'ApiError'
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json', ...(init.body ? { 'Content-Type': 'application/json' } : {}) },
    ...init,
  })
  if (!res.ok) {
    if (res.status === 401) onUnauthorized?.()
    let code = 'http_' + res.status
    let message = res.statusText || '请求失败'
    try {
      const body = (await res.json()) as { code?: string; message?: string }
      if (body.code) code = body.code
      if (body.message) message = body.message
    } catch {
      // Non-JSON error body: keep the status-based message.
    }
    throw new ApiError(res.status, code, message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export interface Health {
  status: string
  version: string
  shares: number
  time: number
}

export const api = {
  health: () => request<Health>('/api/health'),
}

export interface Session {
  authenticated: boolean
  must_change?: boolean
  username?: string
}

export const authApi = {
  session: () => request<Session>('/api/session'),
  login: (username: string, password: string) =>
    request<Session>('/api/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  logout: () => request<Session>('/api/logout', { method: 'POST' }),
  changeCredentials: (payload: { currentPassword: string; newUsername: string; newPassword: string }) =>
    request<Session>('/api/password', {
      method: 'POST',
      body: JSON.stringify({
        current_password: payload.currentPassword,
        new_username: payload.newUsername,
        new_password: payload.newPassword,
      }),
    }),
}

// The session cache must die when the server says the session is gone (idle
// expiry, password rescue). session.ts registers the reset here to avoid an
// import cycle between api and session state.
let onUnauthorized: (() => void) | null = null

export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

export interface Share {
  id: number
  name: string
  virtual_path: string
  root: string
  sort: number
}

export interface Rule {
  share_id: number
  rel_path: string
  perm: 'none' | 'r' | 'rw'
}

export interface ShareView {
  share: Share
  rules: Rule[]
}

export interface PublicShare {
  id: number
  name: string
  virtual_path: string
}

export const sharesApi = {
  public: () => request<PublicShare[]>('/api/shares'),
  list: () => request<ShareView[]>('/api/admin/shares'),
  create: (payload: { name: string; virtual_path: string; root: string; root_perm: string }) =>
    request<Share>('/api/admin/shares', { method: 'POST', body: JSON.stringify(payload) }),
  update: (id: number, payload: { name: string; virtual_path: string; sort: number }) =>
    request<Share>(`/api/admin/shares/${id}`, { method: 'PUT', body: JSON.stringify(payload) }),
  remove: (id: number) => request<{ status: string }>(`/api/admin/shares/${id}`, { method: 'DELETE' }),
  setRule: (id: number, payload: { rel_path: string; perm: string }) =>
    request<Rule>(`/api/admin/shares/${id}/rules`, { method: 'POST', body: JSON.stringify(payload) }),
  deleteRule: (id: number, relPath: string) =>
    request<{ status: string }>(
      `/api/admin/shares/${id}/rules?rel_path=${encodeURIComponent(relPath)}`,
      { method: 'DELETE' },
    ),
}

export interface Entry {
  name: string
  path: string
  is_dir: boolean
  size: number
  mod_time: string
  note: string
}

export interface ListResponse {
  share: PublicShare
  path: string
  entries: Entry[]
  total: number
  page: number
  page_size: number
  writable: boolean
}

export const listApi = {
  list: (
    shareId: number,
    params: { path?: string; page?: number; pageSize?: number; sort?: string; order?: string },
  ) => {
    const q = new URLSearchParams()
    if (params.path) q.set('path', params.path)
    if (params.page) q.set('page', String(params.page))
    if (params.pageSize) q.set('page_size', String(params.pageSize))
    if (params.sort) q.set('sort', params.sort)
    if (params.order) q.set('order', params.order)
    return request<ListResponse>(`/api/shares/${shareId}/list?${q.toString()}`)
  },
}

export const notesApi = {
  set: (shareId: number, path: string, note: string) =>
    request<{ path: string; note: string }>('/api/admin/notes', {
      method: 'POST',
      body: JSON.stringify({ share_id: shareId, path, note }),
    }),
}

export interface SearchResponse {
  path: string
  query: string
  entries: Entry[]
  truncated: boolean
  timed_out: boolean
  depth_limited: boolean
  visited: number
}

export const searchApi = {
  find: (shareId: number, path: string, q: string) =>
    request<SearchResponse>(
      `/api/shares/${shareId}/search?path=${encodeURIComponent(path)}&q=${encodeURIComponent(q)}`,
    ),
}

export interface UploadSession {
  id: string
  chunk_size: number
  size: number
  received: number[]
  name: string
}

export const uploadsApi = {
  begin: (payload: { share_id: number; path: string; name: string; size: number }) =>
    request<UploadSession>('/api/uploads', { method: 'POST', body: JSON.stringify(payload) }),
  status: (id: string) => request<UploadSession>(`/api/uploads/${id}`),
  chunk: async (id: string, index: number, data: Blob) => {
    const res = await fetch(`/api/uploads/${id}/${index}`, { method: 'PUT', body: data })
    if (!res.ok) throw new ApiError(res.status, 'chunk_failed', `分块 ${index} 上传失败（${res.status}）`)
    return (await res.json()) as UploadSession
  },
  complete: (id: string) =>
    request<{ path: string; name: string }>(`/api/uploads/${id}/complete`, { method: 'POST' }),
  abort: (id: string) => request<{ status: string }>(`/api/uploads/${id}`, { method: 'DELETE' }),
}

export const opsApi = {
  mkdir: (shareId: number, path: string, name: string) =>
    request<{ path: string }>(`/api/shares/${shareId}/mkdir`, {
      method: 'POST',
      body: JSON.stringify({ path, name }),
    }),
  rename: (shareId: number, path: string, name: string) =>
    request<{ path: string }>(`/api/shares/${shareId}/rename`, {
      method: 'POST',
      body: JSON.stringify({ path, name }),
    }),
  move: (shareId: number, path: string, to: string) =>
    request<{ path: string }>(`/api/shares/${shareId}/move`, {
      method: 'POST',
      body: JSON.stringify({ path, to }),
    }),
}

export const trashApi = {
  remove: (shareId: number, path: string) =>
    request<{ status: string; id: number; origin: string }>(`/api/shares/${shareId}/delete`, {
      method: 'POST',
      body: JSON.stringify({ path }),
    }),
  list: () =>
    request<{ id: number; share_id: number; origin: string; deleted_at: number; ip: string }[]>(
      '/api/admin/trash',
    ),
  restore: (id: number, force = false) =>
    request<{ path: string }>(`/api/admin/trash/${id}/restore${force ? '?force=1' : ''}`, {
      method: 'POST',
    }),
  clear: () => request<{ removed: number }>('/api/admin/trash/clear', { method: 'POST' }),
}

export interface LogEntry {
  id: number
  ts: number
  ip: string
  host: string
  action: string
  share_id: number
  path: string
  result: string
  detail: string
}

export interface HostEntry {
  ip: string
  hostname: string
  source: string
  note: string
}

export const logsApi = {
  list: (params: { from?: string; to?: string; ip?: string; action?: string; page?: number; pageSize?: number }) => {
    const q = new URLSearchParams()
    if (params.from) q.set('from', params.from)
    if (params.to) q.set('to', params.to)
    if (params.ip) q.set('ip', params.ip)
    if (params.action) q.set('action', params.action)
    if (params.page) q.set('page', String(params.page))
    if (params.pageSize) q.set('page_size', String(params.pageSize))
    return request<{ entries: LogEntry[]; total: number; page: number; page_size: number }>(
      `/api/admin/logs?${q.toString()}`,
    )
  },
  exportUrl: (params: { from?: string; to?: string; ip?: string; action?: string }) => {
    const q = new URLSearchParams()
    if (params.from) q.set('from', params.from)
    if (params.to) q.set('to', params.to)
    if (params.ip) q.set('ip', params.ip)
    if (params.action) q.set('action', params.action)
    return `/api/admin/logs/export?${q.toString()}`
  },
  hosts: () => request<HostEntry[]>('/api/admin/hosts'),
  setHost: (payload: { ip: string; hostname: string; note?: string }) =>
    request<HostEntry>('/api/admin/hosts', { method: 'POST', body: JSON.stringify(payload) }),
  deleteHost: (ip: string) =>
    request<{ status: string }>(`/api/admin/hosts/${encodeURIComponent(ip)}`, { method: 'DELETE' }),
}

export interface Site {
  title: string
  notice: string
  version: string
}

export const siteApi = {
  get: () => request<Site>('/api/site'),
}

export interface IPRule {
  cidr: string
  kind: 'allow' | 'deny'
  note: string
  hits: number
  last_hit_at: number
}

export const settingsApi = {
  get: () => request<{ values: Record<string, string>; keys: string[] }>('/api/admin/settings'),
  put: (values: Record<string, string>) =>
    request<{ updated: string[] }>('/api/admin/settings', {
      method: 'PUT',
      body: JSON.stringify({ values }),
    }),
  ipRules: () => request<IPRule[]>('/api/admin/ip-rules'),
  setIPRule: (payload: { cidr: string; kind: string; note?: string }) =>
    request<{ cidr: string; kind: string }>('/api/admin/ip-rules', {
      method: 'POST',
      body: JSON.stringify(payload),
    }),
  deleteIPRule: (cidr: string) =>
    request<{ status: string }>(`/api/admin/ip-rules?cidr=${encodeURIComponent(cidr)}`, {
      method: 'DELETE',
    }),
}

export interface FSEntry {
  name: string
  path: string
  is_dir: boolean
}

export interface FSList {
  path: string
  parent: string
  entries: FSEntry[]
}

export const fsApi = {
  list: (path: string) => request<FSList>(`/api/admin/fs/list?path=${encodeURIComponent(path)}`),
  mkdir: (path: string, name: string) =>
    request<FSEntry>('/api/admin/fs/mkdir', {
      method: 'POST',
      body: JSON.stringify({ path, name }),
    }),
}
