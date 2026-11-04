// Verifies each published release: the sidecar sha256 asset must match the digest
// GitHub computed for the zip asset. Reads GH_TOKEN from the environment.
const token = process.env.GH_TOKEN
if (!token) throw new Error('GH_TOKEN missing')
const headers = { Authorization: `Bearer ${token}`, Accept: 'application/vnd.github+json' }

for (const tag of ['v1.0.0', 'v1.0.1', 'v1.0.2']) {
  const rel = await (await fetch(`https://api.github.com/repos/yonzL/lanDisk/releases/tags/${tag}`, { headers })).json()
  if (!rel.assets) {
    console.log(`${tag}: release missing (${rel.message})`)
    continue
  }
  const zip = rel.assets.find((a) => a.name.endsWith('.zip'))
  const side = rel.assets.find((a) => a.name.endsWith('.sha256'))
  let sidecar = ''
  if (side) {
    sidecar = await (
      await fetch(side.url, { headers: { ...headers, Accept: 'application/octet-stream' } })
    ).text()
  }
  const sideHash = sidecar.trim().split(/\s+/)[0]
  const zipHash = (zip?.digest ?? '').replace('sha256:', '')
  const ok = zipHash && sideHash === zipHash
  console.log(
    `${tag}: ${zip?.name} ${zip?.size}B digest=${zipHash.slice(0, 12)}… sidecar=${sideHash.slice(0, 12)}… ${ok ? 'MATCH' : 'MISMATCH'} draft=${rel.draft} prerelease=${rel.prerelease}`,
  )
}
