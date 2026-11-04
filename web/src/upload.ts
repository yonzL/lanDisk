import { ApiError, uploadsApi, type UploadSession } from './api'

// Resumable uploads: the session id is remembered per (share, dir, name, size)
// so a reload can continue where it stopped instead of re-sending everything.
const storageKey = (shareId: number, path: string, file: File) =>
  `land.upload.${shareId}.${path}.${file.name}.${file.size}`

export interface UploadProgress {
  name: string
  sent: number
  total: number
  done: boolean
  error?: string
}

export async function uploadFile(
  shareId: number,
  path: string,
  file: File,
  onProgress: (p: UploadProgress) => void,
): Promise<void> {
  const key = storageKey(shareId, path, file)
  const report = (sent: number, done = false, error?: string) =>
    onProgress({ name: file.name, sent, total: file.size, done, error })

  let session: UploadSession | null = null
  const remembered = localStorage.getItem(key)
  if (remembered) {
    try {
      session = await uploadsApi.status(remembered)
    } catch {
      localStorage.removeItem(key)
      session = null
    }
  }
  if (!session) {
    session = await uploadsApi.begin({ share_id: shareId, path, name: file.name, size: file.size })
    localStorage.setItem(key, session.id)
  }

  const chunkSize = session.chunk_size
  const received = new Set(session.received)
  const totalChunks = Math.ceil(file.size / chunkSize)
  let sent = received.size * chunkSize
  report(Math.min(sent, file.size))

  for (let index = 0; index < totalChunks; index++) {
    if (received.has(index)) continue
    const start = index * chunkSize
    const blob = file.slice(start, Math.min(start + chunkSize, file.size))
    try {
      await uploadsApi.chunk(session.id, index, blob)
    } catch (error) {
      // 4xx means the server refused this upload for good (permission revoked,
      // bad name): discard the session so its part file cannot linger. Network
      // failures keep the id for resume.
      if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
        await uploadsApi.abort(session.id).catch(() => undefined)
        localStorage.removeItem(key)
      }
      throw error
    }
    sent += blob.size
    report(Math.min(sent, file.size))
  }
  await uploadsApi.complete(session.id)
  localStorage.removeItem(key)
  report(file.size, true)
}
