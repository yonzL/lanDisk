//go:build !windows

// Package winsvc on non-Windows platforms keeps the console mode working and
// reports that service registration is Windows-only.
package winsvc

import (
	"errors"

	"land/internal/app"
)

// IsService is always false outside Windows.
func IsService() bool { return false }

// Run is unreachable outside Windows.
func Run(a *app.App) error { return errors.New("服务模式仅支持 Windows") }

// Install reports that service registration is Windows-only.
func Install() error { return errors.New("install 仅支持 Windows") }

// Uninstall reports that service registration is Windows-only.
func Uninstall() error { return errors.New("uninstall 仅支持 Windows") }
