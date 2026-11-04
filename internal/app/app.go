// Package app wires the running server: open the database, read the port, and
// serve until asked to stop. Both the console mode and the Windows service
// mode drive this same type, so the two paths cannot drift apart.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"land/internal/auth"
	"land/internal/files"
	"land/internal/httpapi"
	"land/internal/logs"
	"land/internal/store"
	"land/internal/trash"
	"land/internal/upload"
)

// App is a configured, not-yet-started server.
type App struct {
	Store  *store.Store
	Logs   *logs.Writer
	server *httpapi.Server
	Logger *slog.Logger

	version string
	http    *http.Server
	listen  net.Listener
	addr    string

	// done is closed once the serve goroutine has exited; serveErr is written
	// before that close and read only afterwards. Serve, Shutdown and Done are
	// therefore all pure readers of the same signal instead of competing
	// receivers.
	done     chan struct{}
	serveErr error

	// stopSweep ends the periodic session sweep started in Start.
	stopSweep chan struct{}
}

// New opens the database at dbPath (default: next to the executable) and
// prepares the HTTP server. It does not bind a port yet.
func New(dbPath, version string, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if dbPath == "" {
		p, err := DefaultDBPath()
		if err != nil {
			return nil, err
		}
		dbPath = p
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}

	created, err := auth.BootstrapAdmin(context.Background(), st)
	if err != nil {
		st.Close()
		return nil, err
	}
	if created {
		logger.Info("首次启动：已创建 admin 账号，运行 lanDisk.exe reset-password 获取口令")
	}

	// One-time: old audit rows stored percent-encoded query strings; rewrite them
	// so the log viewer and CSV export show real file names.
	if v, ok, err := st.Setting(context.Background(), store.SettingKeyLogPathsDecoded); err == nil && (!ok || v != "1") {
		if n, err := st.DecodeLogPaths(context.Background()); err != nil {
			logger.Warn("解码历史日志路径失败", "error", err)
		} else {
			if n > 0 {
				logger.Info("已解码历史日志路径", "count", n)
			}
			_ = st.SetSetting(context.Background(), store.SettingKeyLogPathsDecoded, "1")
		}
	}

	port, err := st.SettingInt(context.Background(), store.KeyPort, 80)
	if err != nil {
		st.Close()
		return nil, err
	}

	logWriter := logs.New(st, logger)

	server := &httpapi.Server{
		Store:    st,
		Version:  version,
		Logger:   logger,
		Throttle: auth.NewThrottle(),
		Logs:     logWriter,
		IPFilter: httpapi.NewIPFilter(),
		Limiter:  httpapi.NewConcurrencyLimiter(64),
	}
	if err := server.LoadRuntimeLimits(context.Background()); err != nil {
		logger.Warn("读取限速/并发配置失败，使用默认值", "error", err)
	}
	srv := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}

	return &App{
		server:  server,
		Store:   st,
		Logs:    logWriter,
		Logger:  logger,
		version: version,
		http:    srv,
		addr:    net.JoinHostPort("", strconv.Itoa(port)),
	}, nil
}

// Database filename next to the executable. 1.0.2 renamed it from land.db to
// landisk.db so the data file matches the product name; the old file is migrated
// in place on first start.
const (
	DBFileName       = "landisk.db"
	legacyDBFileName = "land.db"
)

// DefaultDBPath keeps every runtime artifact next to the executable: the data
// directory holds only lanDisk.exe and landisk.db.
func DefaultDBPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	return ensureDBFileName(dir), nil
}

// ensureDBFileName returns <dir>/landisk.db, renaming a legacy land.db (and its
// WAL/SHM side files) when the new name does not exist yet. Upgrading is then a
// matter of replacing the exe: the data follows the name change automatically.
func ensureDBFileName(dir string) string {
	current := filepath.Join(dir, DBFileName)
	if _, err := os.Stat(current); err == nil {
		return current
	}
	legacy := filepath.Join(dir, legacyDBFileName)
	if _, err := os.Stat(legacy); err != nil {
		return current // neither exists: a fresh database will be created
	}
	// Rename the database first; the side files only matter if the rename worked.
	if err := os.Rename(legacy, current); err != nil {
		return legacy
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Rename(legacy+suffix, current+suffix)
	}
	return current
}

// Addr is the address the app listens on.
func (a *App) Addr() string { return a.addr }

// Start binds the port and begins serving in the background.
func (a *App) Start() error {
	ln, err := net.Listen("tcp", a.addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败（端口可能被占用%s）：%w\n急救：lanDisk.exe config set %s 8080",
			a.addr, PortOwnerHint(a.addr), err, store.KeyPort)
	}
	a.listen = ln
	a.done = make(chan struct{})
	a.stopSweep = make(chan struct{})
	go a.sweepSessions()
	go func() {
		a.Logger.Info("lan disk 已启动", "addr", a.addr, "db", a.Store.Path(), "version", a.version)
		err := a.http.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		a.serveErr = err
		close(a.done)
	}()
	return nil
}

// Done is closed once the HTTP server has stopped (cleanly or not).
func (a *App) Done() <-chan struct{} {
	if a.done == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return a.done
}

// Err reports why the server stopped; it is valid only after Done is closed.
func (a *App) Err() error { return a.serveErr }

// Serve blocks until the HTTP server stops and reports why.
func (a *App) Serve() error {
	if a.done == nil {
		return errors.New("app: Start must be called before Serve")
	}
	<-a.done
	return a.serveErr
}

// Shutdown stops the HTTP server and closes the database. It waits for the
// serve goroutine to exit so callers never see a half-torn-down server.
func (a *App) Shutdown(ctx context.Context) error {
	var firstErr error
	if a.stopSweep != nil {
		close(a.stopSweep)
		a.stopSweep = nil
	}
	if err := a.http.Shutdown(ctx); err != nil {
		// A timed-out drain means handlers may still be running: report it
		// instead of pretending the stop was clean.
		firstErr = fmt.Errorf("关闭 HTTP 服务：%w", err)
	}
	if a.done != nil {
		select {
		case <-a.done:
			if a.serveErr != nil && firstErr == nil {
				firstErr = a.serveErr
			}
		case <-ctx.Done():
			if firstErr == nil {
				firstErr = fmt.Errorf("等待 HTTP 服务退出：%w", ctx.Err())
			}
		}
	}
	// Flush IP-rule hit counters before the store closes; the same call also
	// reloads settings, which is harmless during teardown.
	if a.server != nil {
		a.server.RefreshRuntimeLimits(context.Background())
	}
	if a.Logs != nil {
		a.Logs.Close()
	}
	if err := a.Store.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// sweepSessions is the periodic maintenance loop: settings/limits refresh, trash
// and log retention, stale upload sessions. Login sessions are deliberately NOT
// purged — the admin stays signed in until they log out.
func (a *App) sweepSessions() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopSweep:
			return
		case <-ticker.C:
			a.refreshRuntime()
			a.purgeTrash()
			a.purgeLogs()
			if removed, err := upload.Cleanup(a.Store, time.Now()); err != nil {
				a.Logger.Error("清理过期上传失败", "error", err)
			} else if removed > 0 {
				a.Logger.Info("清理过期上传", "count", removed)
			}
		}
	}
}

// purgeTrash deletes recycle-bin entries older than the configured retention.
func (a *App) purgeTrash() {
	ctx := context.Background()
	days, err := a.Store.SettingInt(ctx, store.KeyTrashRetentionDays, 90)
	if err != nil {
		a.Logger.Error("读取回收站保留期失败", "error", err)
		return
	}
	shares, err := a.Store.ListShares(ctx)
	if err != nil {
		a.Logger.Error("清理回收站：读取共享失败", "error", err)
		return
	}
	roots := map[int64]files.Root{}
	for _, sh := range shares {
		root, err := files.RootFor(sh)
		if err != nil {
			continue
		}
		roots[sh.ID] = root
	}
	removed, err := trash.Purge(ctx, a.Store, roots, days, time.Now())
	if err != nil {
		a.Logger.Error("清理回收站失败", "error", err)
		return
	}
	if removed > 0 {
		a.Logger.Info("清理回收站", "count", removed, "retention_days", days)
	}
}

// purgeLogs deletes entries beyond the configured retention window.
func (a *App) purgeLogs() {
	ctx := context.Background()
	days, err := a.Store.SettingInt(ctx, store.KeyLogRetentionDays, 90)
	if err != nil {
		a.Logger.Error("读取日志保留期失败", "error", err)
		return
	}
	if days <= 0 {
		return
	}
	removed, err := a.Store.PurgeLogs(ctx, time.Now().AddDate(0, 0, -days))
	if err != nil {
		a.Logger.Error("清理日志失败", "error", err)
		return
	}
	if removed > 0 {
		a.Logger.Info("清理日志", "count", removed, "retention_days", days)
	}
}

// refreshRuntime re-applies settings that may change while the service runs.
func (a *App) refreshRuntime() {
	if a.server != nil {
		a.server.RefreshRuntimeLimits(context.Background())
	}
}
