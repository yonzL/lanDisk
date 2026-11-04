//go:build windows

// Package winsvc hosts the server inside the Windows Service Control Manager
// and owns service registration. Only this package talks to the SCM.
package winsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"

	"land/internal/app"
)

// Service registration details. The internal name is ASCII and space-free
// because the SCM and sc.exe handle it more predictably.
const (
	Name        = "LandDisk"
	DisplayName = "lan disk 文件服务器"
	Description = "把本机目录通过局域网 HTTP 共享给其他设备（lan disk）"
)

// IsService reports whether this process was started by the SCM rather than
// from a console.
func IsService() bool {
	isService, err := svc.IsWindowsService()
	return err == nil && isService
}

// Run hosts the app inside the SCM and blocks until the service stops.
func Run(a *app.App) error {
	elog, err := eventlog.Open(Name)
	if err != nil {
		// Without a registered source we can still serve; the SCM will show
		// the failure to start, and the reason goes to stderr.
		fmt.Fprintf(os.Stderr, "lan disk: 无法打开事件日志（%v），继续运行\n", err)
	} else {
		defer elog.Close()
	}
	return svc.Run(Name, &handler{app: a, elog: elog})
}

type handler struct {
	app  *app.App
	elog *eventlog.Log
}

func (h *handler) logf(level slog.Level, msg string, err error) {
	if h.elog == nil {
		return
	}
	text := msg
	if err != nil {
		text += ": " + err.Error()
	}
	if level >= slog.LevelError {
		h.elog.Error(1, text)
		return
	}
	h.elog.Info(1, text)
}

func (h *handler) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown

	status <- svc.Status{State: svc.StartPending}
	if err := h.app.Start(); err != nil {
		h.logf(slog.LevelError, "启动失败", err)
		return false, 1
	}
	h.logf(slog.LevelInfo, "服务已启动，监听 "+h.app.Addr(), nil)
	status <- svc.Status{State: svc.Running, Accepts: accepted}

	// The SCM channel is not the only way the service can end: if http.Serve
	// dies (listener error, port stolen) the service must report it instead of
	// claiming Running while nothing is served.
	serveFailed := make(chan error, 1)
	go func() {
		<-h.app.Done()
		serveFailed <- h.app.Err()
	}()

	for {
		select {
		case err := <-serveFailed:
			if err != nil {
				h.logf(slog.LevelError, "HTTP 服务异常退出", err)
				status <- svc.Status{State: svc.Stopped}
				return false, 1
			}
			status <- svc.Status{State: svc.Stopped}
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				if err := h.app.Shutdown(ctx); err != nil {
					h.logf(slog.LevelError, "关闭时出错", err)
				} else {
					h.logf(slog.LevelInfo, "服务已停止", nil)
				}
				cancel()
				return false, 0
			}
		}
	}
}

// Install registers the service for the current executable, then starts it.
func Install() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位可执行文件：%w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if err := ensureWritable(filepath.Dir(exe)); err != nil {
		return err
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器（需要管理员权限）：%w", err)
	}
	defer m.Disconnect()

	if existing, err := m.OpenService(Name); err == nil {
		existing.Close()
		return fmt.Errorf("服务 %s 已安装，若要重装请先运行 land.exe uninstall", Name)
	}

	s, err := m.CreateService(Name, exe, mgr.Config{
		DisplayName: DisplayName,
		Description: Description,
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return fmt.Errorf("创建服务：%w", err)
	}
	defer s.Close()

	if err := eventlog.InstallAsEventCreate(Name, eventlog.Info|eventlog.Warning|eventlog.Error); err != nil {
		// Logging is helpful but not required to serve files.
		fmt.Fprintf(os.Stderr, "警告：注册事件日志源失败（%v），服务的运行日志将只出现在事件查看器的应用程序日志中\n", err)
	}

	if err := s.Start(); err != nil {
		return fmt.Errorf("服务已安装但启动失败（用 eventvwr.msc 查看原因，或运行 land.exe config set port 8080 换端口）：%w", err)
	}
	fmt.Printf("已安装并启动服务 %q（%s）\n显示名：%s\n", Name, exe, DisplayName)
	fmt.Println("访问地址：http://<本机局域网 IP>:<配置端口>（端口默认 80，可用 land.exe config get port 查看）")
	return nil
}

// Uninstall stops and removes the service. Shared files, the recycle bin and
// land.db are left untouched.
func Uninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器（需要管理员权限）：%w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(Name)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return fmt.Errorf("服务 %s 未安装", Name)
		}
		return fmt.Errorf("打开服务：%w", err)
	}
	defer s.Close()

	if st, err := s.Query(); err == nil && st.State != svc.Stopped {
		if _, err := s.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return fmt.Errorf("停止服务：%w", err)
		}
		if err := waitStopped(s); err != nil {
			return err
		}
	}
	if err := s.Delete(); err != nil {
		return fmt.Errorf("删除服务：%w", err)
	}
	if err := eventlog.Remove(Name); err != nil {
		// The source may never have been registered.
		fmt.Fprintf(os.Stderr, "提示：移除事件日志源失败（%v）\n", err)
	}
	fmt.Printf("服务 %s 已卸载。共享文件、回收站与 land.db 未被删除。\n", Name)
	return nil
}

func waitStopped(s *mgr.Service) error {
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("查询服务状态：%w", err)
		}
		if st.State == svc.Stopped {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("等待服务停止超时，请手动确认后重试")
}

// ensureWritable fails early when the executable sits in a directory the
// service account cannot write — land.db lives next to the exe.
func ensureWritable(dir string) error {
	probe := filepath.Join(dir, ".land-write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("程序目录 %s 不可写（land.db 需要放在这里）：%w\n请把 land.exe 放到可写目录（例如 D:\\land\\）后再安装", dir, err)
	}
	f.Close()
	os.Remove(probe)
	return nil
}
