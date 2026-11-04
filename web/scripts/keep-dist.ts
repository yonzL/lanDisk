// Keeps web/dist non-empty after `vite build`, so `//go:embed all:dist`
// compiles on a clean clone. Vite wipes dist (emptyOutDir), so this runs as a
// post-build step rather than relying on a committed file surviving a build.
await Bun.write('dist/.gitkeep', '')
