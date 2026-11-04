// Package trash implements the recycle bin: deletes move into
// <share root>/.land-trash instead of vanishing, and can be restored or purged.
// The bin lives inside the share so a normal delete is a same-volume rename.
package trash

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"land/internal/files"
	"land/internal/store"
)

// DirName is the hidden recycle-bin directory inside every share root.
const DirName = ".land-trash"

// ErrTargetExists means the original path is occupied again.
var ErrTargetExists = errors.New("trash: original path already exists")

// Delete moves relPath into the share's recycle bin and records it.
func Delete(ctx context.Context, st *store.Store, root files.Root, shareID int64, relPath, ip string, now time.Time) (store.TrashEntry, error) {
	abs, cleanRel, err := root.Resolve(relPath)
	if err != nil {
		return store.TrashEntry{}, err
	}
	if cleanRel == "" {
		return store.TrashEntry{}, files.ErrInvalidPath
	}
	if _, err := os.Lstat(abs); err != nil {
		return store.TrashEntry{}, err
	}

	if err := os.MkdirAll(filepath.Join(root.Dir, DirName), 0o755); err != nil {
		return store.TrashEntry{}, fmt.Errorf("创建回收站目录失败：%w", err)
	}
	// Reserve the row first so the on-disk name carries its id; a failed move
	// removes the row again.
	placeholder := fmt.Sprintf("pending-%d", now.UnixNano())
	id, err := st.CreateTrash(ctx, shareID, cleanRel, placeholder, ip, now)
	if err != nil {
		return store.TrashEntry{}, err
	}
	trashName := fmt.Sprintf("%d_%s", id, path.Base(cleanRel))
	trashRel := DirName + "/" + trashName
	trashAbs := filepath.Join(root.Dir, DirName, trashName)

	if err := moveOrCopy(abs, trashAbs); err != nil {
		// The row would point at nothing.
		_ = st.DeleteTrashEntry(ctx, id)
		return store.TrashEntry{}, err
	}
	if err := st.SetTrashPath(ctx, id, trashRel); err != nil {
		return store.TrashEntry{}, err
	}
	return st.TrashByID(ctx, id)
}

// Restore puts an entry back where it came from. force allows overwriting a file
// that appeared at the original path in the meantime; without it a conflict is
// reported so nothing is silently replaced.
func Restore(ctx context.Context, st *store.Store, root files.Root, id int64, force bool) (string, error) {
	entry, err := st.TrashByID(ctx, id)
	if err != nil {
		return "", err
	}
	trashAbs, err := root.ResolveTrash(entry.TrashRelPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(trashAbs); err != nil {
		// The row outlived its file (interrupted purge): reap it.
		_ = st.DeleteTrashEntry(ctx, id)
		return "", fmt.Errorf("回收站中的文件已不存在，已清理该记录")
	}

	targetAbs, cleanOrig, err := root.Resolve(entry.OrigRelPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(targetAbs); err == nil {
		if !force {
			return "", fmt.Errorf("%w：%s", ErrTargetExists, entry.OrigRelPath)
		}
		if err := os.RemoveAll(targetAbs); err != nil {
			return "", fmt.Errorf("覆盖目标失败：%w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(targetAbs), 0o755); err != nil {
		return "", fmt.Errorf("创建目标目录失败：%w", err)
	}
	if err := moveOrCopy(trashAbs, targetAbs); err != nil {
		return "", err
	}
	if err := st.DeleteTrashEntry(ctx, id); err != nil {
		return "", err
	}
	return cleanOrig, nil
}

// Purge deletes entries older than retention and returns how many were removed.
func Purge(ctx context.Context, st *store.Store, roots map[int64]files.Root, retentionDays int, now time.Time) (int, error) {
	if retentionDays <= 0 {
		retentionDays = 90
	}
	expired, err := st.ExpiredTrash(ctx, now.AddDate(0, 0, -retentionDays))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range expired {
		root, ok := roots[entry.ShareID]
		if ok {
			abs, err := root.ResolveTrash(entry.TrashRelPath)
			if err == nil {
				if err := os.RemoveAll(abs); err != nil {
					continue
				}
			}
		}
		if err := st.DeleteTrashEntry(ctx, entry.ID); err != nil {
			continue
		}
		removed++
	}
	return removed, nil
}

// moveOrCopy renames within a volume and falls back to copy+verify+delete when
// the source sits behind a junction on another volume (rename would fail with
// ERROR_NOT_SAME_DEVICE). The source is only removed after the copy verified.
func moveOrCopy(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := copyTree(src, dst); err != nil {
			os.RemoveAll(dst)
			return err
		}
	} else {
		if err := copyFile(src, dst); err != nil {
			os.Remove(dst)
			return err
		}
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("复制完成但删除源失败：%w", err)
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, fi.Mode().Perm())
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	copied, err := os.Stat(dst)
	if err != nil {
		return err
	}
	if copied.Size() != info.Size() {
		return fmt.Errorf("复制后大小不一致：%d != %d", copied.Size(), info.Size())
	}
	return nil
}

// IsTrashPath reports whether a relative path points inside the recycle bin.
func IsTrashPath(rel string) bool {
	rel = strings.TrimPrefix(rel, "/")
	return rel == DirName || strings.HasPrefix(rel, DirName+"/")
}

// Empty removes every recycle-bin entry (for one share, or all when shareID is
// 0). It is what "清空回收站" calls: retention does not apply to an explicit empty.
func Empty(ctx context.Context, st *store.Store, roots map[int64]files.Root, shareID int64) (int, error) {
	entries, err := st.ListTrash(ctx, shareID)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, entry := range entries {
		if root, ok := roots[entry.ShareID]; ok {
			if abs, err := root.ResolveTrash(entry.TrashRelPath); err == nil {
				if err := os.RemoveAll(abs); err != nil {
					continue
				}
			}
		}
		if err := st.DeleteTrashEntry(ctx, entry.ID); err != nil {
			continue
		}
		removed++
	}
	return removed, nil
}
