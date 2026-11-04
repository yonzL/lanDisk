// Command land is the single binary: HTTP server, Windows service host, and
// CLI for the few operations that must work when the service will not start.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"land/internal/app"
	"land/internal/auth"
	"land/internal/store"
	"land/internal/winsvc"
)

// version is injected at build time: -X main.version=…
var version = "dev"

const usage = `lan disk — 局域网文件服务器

用法:
  lanDisk.exe [--db PATH]            前台运行（调试模式，日志打到控制台）
  lanDisk.exe install                注册为 Windows 服务并开机自启（需管理员）
  lanDisk.exe uninstall              停止并移除 Windows 服务（需管理员）
  lanDisk.exe reset-password         生成并打印新的管理员口令（本机执行）
  lanDisk.exe config get [KEY]       查看配置项（省略 KEY 则列出全部）
  lanDisk.exe config set KEY VALUE   修改配置项（服务运行中修改需重启生效）
  lanDisk.exe version                版本信息
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "land: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	dbPath, args, err := extractDB(args)
	if err != nil {
		return err
	}

	cmd := "run"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case "run":
		return cmdRun(dbPath, args)
	case "install":
		return winsvc.Install()
	case "uninstall":
		return winsvc.Uninstall()
	case "config":
		return cmdConfig(dbPath, args)
	case "reset-password":
		return cmdResetPassword(dbPath)
	case "version":
		fmt.Printf("lan disk %s\n", version)
		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("未知子命令 %q\n\n%s", cmd, usage)
	}
}

func cmdRun(dbPath string, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("未知参数 %q\n\n%s", args[0], usage)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	a, err := app.New(dbPath, version, logger)
	if err != nil {
		return err
	}

	// Started by the SCM: hand control to the service handler, which reports
	// start/stop back to Windows and logs to the event log.
	if winsvc.IsService() {
		return winsvc.Run(a)
	}

	if err := a.Start(); err != nil {
		a.Shutdown(context.Background())
		return err
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-serveResult(a):
		return err
	case sig := <-stop:
		logger.Info("收到退出信号，正在关闭", "signal", sig.String())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.Shutdown(ctx)
}

// serveResult adapts Serve into a channel so the signal path stays a plain
// select without a second goroutine leaking on exit.
func serveResult(a *app.App) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- a.Serve() }()
	return ch
}

func cmdConfig(dbPath string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法：lanDisk.exe config get|set …")
	}
	action, rest := args[0], args[1:]

	if dbPath == "" {
		p, err := app.DefaultDBPath()
		if err != nil {
			return err
		}
		dbPath = p
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	switch action {
	case "get":
		if len(rest) == 0 {
			all, err := st.Settings(ctx)
			if err != nil {
				return err
			}
			for _, k := range sortedKeys(all) {
				fmt.Printf("%-24s %s\n", k, all[k])
			}
			return nil
		}
		v, ok, err := st.Setting(ctx, rest[0])
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("未知配置项 %q", rest[0])
		}
		fmt.Println(v)
		return nil
	case "set":
		if len(rest) != 2 {
			return fmt.Errorf("用法：lanDisk.exe config set KEY VALUE")
		}
		key, value := rest[0], rest[1]
		if !store.KnownSetting(key) {
			return fmt.Errorf("未知配置项 %q", key)
		}
		if err := st.SetSetting(ctx, key, value); err != nil {
			return err
		}
		fmt.Printf("%s = %s\n（若服务正在运行，需重启服务生效）\n", key, value)
		return nil
	default:
		return fmt.Errorf("用法：lanDisk.exe config get|set …")
	}
}

// extractDB pulls the --db flag out of anywhere in the argument list, so both
// `lanDisk.exe config set port 8080 --db d:\land.db` and `lanDisk.exe --db … config
// set port 8080` work. A trailing --db with no value is an error: silently
// falling back to the default database would write to the wrong file.
func extractDB(args []string) (dbPath string, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--db" || a == "-db":
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("%s 后面缺少数据库路径", a)
			}
			dbPath = args[i+1]
			i++
		case strings.HasPrefix(a, "--db="):
			dbPath = strings.TrimPrefix(a, "--db=")
		case strings.HasPrefix(a, "-db="):
			dbPath = strings.TrimPrefix(a, "-db=")
		default:
			rest = append(rest, a)
		}
	}
	return dbPath, rest, nil
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// cmdResetPassword exists because no password is ever written to disk: local
// access to the machine is the only way back in.
func cmdResetPassword(dbPath string) error {
	if dbPath == "" {
		p, err := app.DefaultDBPath()
		if err != nil {
			return err
		}
		dbPath = p
	}
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()

	password := auth.RandomPassword()
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	// The account may have been renamed, so look it up by identity, not by the
	// default name: creating a second "admin" would lock the operator out of the
	// renamed one.
	user, err := st.SingleAdmin(ctx)
	if errors.Is(err, store.ErrNotFound) {
		if _, err := st.CreateAdmin(ctx, hash, true); err != nil {
			return err
		}
		fmt.Printf("已创建管理员账号 %s\n新口令：%s\n（首次登录后必须修改）\n", store.AdminUsername, password)
		return nil
	}
	if err != nil {
		return err
	}
	if err := st.SetPassword(ctx, user.ID, hash, true); err != nil {
		return err
	}
	fmt.Printf("管理员账号 %s 的新口令：%s\n（已置为“首次登录必须修改”；服务无需重启）\n", user.Username, password)
	return nil
}
