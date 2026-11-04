package files

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrExists is returned when a create/rename target is already taken.
var ErrExists = errors.New("files: target already exists")

// ValidateName checks a single path segment the user wants to create or rename
// to. It rejects separators, reserved names and Windows-illegal characters —
// everything that could escape the directory or forge a hidden entry.
func ValidateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", ErrInvalidPath
	}
	if strings.ContainsAny(name, `/\:*?"<>|`) || strings.ContainsRune(name, 0) {
		return "", ErrInvalidPath
	}
	// Windows drops trailing dots/spaces; a name that only differs by them would
	// silently collide with an existing entry.
	if strings.TrimRight(name, ". ") != name {
		return "", ErrInvalidPath
	}
	if IsReservedName(name) || isDeviceName(name) {
		return "", ErrInvalidPath
	}
	return name, nil
}

// Mkdir creates one directory inside dirRel.
func (r Root) Mkdir(dirRel, name string) (string, error) {
	clean, err := ValidateName(name)
	if err != nil {
		return "", err
	}
	dirAbs, dirClean, err := r.Resolve(dirRel)
	if err != nil {
		return "", err
	}
	target := filepath.Join(dirAbs, clean)
	if !withinRoot(r.Dir, target) {
		return "", ErrInvalidPath
	}
	if _, err := os.Stat(target); err == nil {
		return "", ErrExists
	}
	if err := os.Mkdir(target, 0o755); err != nil {
		return "", fmt.Errorf("创建目录失败：%w", err)
	}
	return path.Join(dirClean, clean), nil
}

// Rename changes an entry's name inside its current directory.
func (r Root) Rename(relPath, newName string) (string, error) {
	clean, err := ValidateName(newName)
	if err != nil {
		return "", err
	}
	abs, cleanRel, err := r.Resolve(relPath)
	if err != nil {
		return "", err
	}
	if cleanRel == "" {
		return "", ErrInvalidPath // the share root itself cannot be renamed
	}
	if _, err := os.Stat(abs); err != nil {
		return "", err
	}
	target := filepath.Join(filepath.Dir(abs), clean)
	if strings.EqualFold(target, abs) {
		// Same name (possibly different case): Windows refuses the rename, and
		// there is nothing to do anyway.
		return cleanRel, nil
	}
	if _, err := os.Stat(target); err == nil {
		return "", ErrExists
	}
	if err := os.Rename(abs, target); err != nil {
		return "", fmt.Errorf("重命名失败：%w", err)
	}
	return path.Join(path.Dir(cleanRel), clean), nil
}

// Move relocates an entry into another directory of the same share.
func (r Root) Move(relPath, toDirRel string) (string, error) {
	abs, cleanRel, err := r.Resolve(relPath)
	if err != nil {
		return "", err
	}
	if cleanRel == "" {
		return "", ErrInvalidPath
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	toAbs, toClean, err := r.Resolve(toDirRel)
	if err != nil {
		return "", err
	}
	toInfo, err := os.Stat(toAbs)
	if err != nil {
		return "", err
	}
	if !toInfo.IsDir() {
		return "", ErrNotDir
	}
	// Moving a directory into itself (or its own subtree) would detach it from
	// the tree; refuse before touching the filesystem.
	if info.IsDir() && (strings.EqualFold(toAbs, abs) || withinRoot(abs, toAbs)) {
		return "", ErrInvalidPath
	}
	target := filepath.Join(toAbs, path.Base(cleanRel))
	if strings.EqualFold(filepath.Dir(abs), toAbs) {
		// Already there: nothing to do, and renaming onto itself is a no-op.
		return cleanRel, nil
	}
	if _, err := os.Stat(target); err == nil {
		return "", ErrExists
	}
	if err := os.Rename(abs, target); err != nil {
		return "", fmt.Errorf("移动失败：%w", err)
	}
	return path.Join(toClean, path.Base(cleanRel)), nil
}

// windowsDeviceNames are reserved by the OS regardless of extension: a file
// called "CON" or "aux.txt" cannot be created on Windows.
var windowsDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// isDeviceName reports whether the segment (ignoring any extension) collides
// with a Windows device name.
func isDeviceName(name string) bool {
	base := strings.ToLower(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	return windowsDeviceNames[base]
}
