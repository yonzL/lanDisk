// Package upload implements chunked, resumable uploads. Chunks are written
// straight into a hidden part file next to the destination, so a finished
// upload is a same-volume rename and the program directory stays clean.
package upload

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"land/internal/files"
	"land/internal/store"
)

// ChunkSize is the fixed chunk size. Every client uses the value the server
// returns when a session is created, so a change here does not break resuming.
const ChunkSize = 4 << 20 // 4 MiB

// MaxAge is how long an abandoned session (and its part file) survives.
const MaxAge = 24 * time.Hour

// Errors returned to the HTTP layer.
var (
	ErrBadTarget = errors.New("upload: invalid target name")
	ErrTooLarge  = errors.New("upload: size mismatch")
)

// Session is a live upload. Dir/Name describe the final destination; PartAbs is
// the hidden file the chunks are written into.
type Session struct {
	ID      string
	Store   *store.Store
	Row     store.Upload
	Dir     string
	Name    string
	PartAbs string
}

// NewID returns an unguessable upload id.
func NewID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic("upload: no entropy: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Begin validates the destination and creates the session + part file.
func Begin(st *store.Store, root files.Root, shareID int64, dirRel, name string, size int64) (*Session, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, `/\:*?"<>|`) || strings.HasPrefix(name, ".") {
		return nil, ErrBadTarget
	}
	if files.IsReservedName(name) {
		return nil, ErrBadTarget
	}
	if strings.Contains(strings.ToLower(name), ".landpart-") || strings.Contains(strings.ToLower(name), ".landrestore-") {
		return nil, ErrBadTarget
	}
	if size < 0 {
		return nil, ErrTooLarge
	}

	dirAbs, dirClean, err := root.Resolve(dirRel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dirAbs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, files.ErrNotDir
	}
	// Refuse to overwrite an existing directory of the same name; overwriting a
	// file is allowed (the final rename is atomic on the same volume).
	targetAbs := filepath.Join(dirAbs, name)
	if fi, err := os.Stat(targetAbs); err == nil && fi.IsDir() {
		return nil, ErrBadTarget
	}

	id := NewID()
	partAbs := filepath.Join(dirAbs, name+".landpart-"+id)
	f, err := os.OpenFile(partAbs, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("创建分块文件失败：%w", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		os.Remove(partAbs)
		return nil, fmt.Errorf("预分配失败：%w", err)
	}
	f.Close()

	chunks := numChunks(size)
	row := store.Upload{
		ID:        id,
		ShareID:   shareID,
		RelPath:   joinRel(dirClean, name),
		Size:      size,
		Chunk:     ChunkSize,
		Bitmap:    make([]byte, chunks),
		PartPath:  partAbs,
		CreatedAt: time.Now(),
	}
	if err := st.CreateUpload(context.Background(), row); err != nil {
		os.Remove(partAbs)
		return nil, err
	}
	return &Session{ID: id, Store: st, Row: row, Dir: dirAbs, Name: name, PartAbs: partAbs}, nil
}

func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

func numChunks(size int64) int64 {
	if size == 0 {
		return 0
	}
	return (size + ChunkSize - 1) / ChunkSize
}

// ChunkOffset returns the byte offset of chunk index.
func ChunkOffset(index int64) int64 { return index * ChunkSize }

// WriteChunk stores one chunk at its offset. off/size are validated by the
// caller against the session; this only performs the write and records progress.
func (s *Session) WriteChunk(index int64, data []byte) error {
	f, err := os.OpenFile(s.PartAbs, os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开分块文件失败：%w", err)
	}
	defer f.Close()
	if _, err := f.WriteAt(data, ChunkOffset(index)); err != nil {
		return fmt.Errorf("写入分块失败：%w", err)
	}
	return nil
}

// MarkChunk records that index has landed.
func (s *Session) MarkChunk(index int64) error {
	now := time.Now()
	if err := s.Store.MarkUploadChunk(context.Background(), s.ID, index, now); err != nil {
		return err
	}
	if index >= 0 && index < int64(len(s.Row.Bitmap)) {
		s.Row.Bitmap[index] = 1
	}
	s.Row.LastSeen = now
	return nil
}

// Received returns the chunk indices already stored, read from the database so
// a resumed session reflects chunks written by an earlier process.
func (s *Session) Received() []int64 {
	chunks, err := s.Store.ReceivedChunks(context.Background(), s.ID)
	if err != nil {
		return []int64{}
	}
	return chunks
}

// ChunkCount is how many chunks this upload needs.
func (s *Session) ChunkCount() int64 { return numChunks(s.Row.Size) }

// Complete verifies every chunk is present, flushes and renames the part file
// onto the destination. A rename failure (for example the destination is open in
// another program) returns a plain error that must not be shown to clients.
func (s *Session) Complete() error {
	received, err := s.Store.ReceivedChunks(context.Background(), s.ID)
	if err != nil {
		return fmt.Errorf("读取上传进度失败：%w", err)
	}
	needed := s.ChunkCount()
	if int64(len(received)) < needed {
		return ErrIncomplete{Missing: int(needed - int64(len(received)))}
	}
	f, err := os.OpenFile(s.PartAbs, os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开分块文件失败：%w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("落盘失败：%w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭分块文件失败：%w", err)
	}

	targetAbs := filepath.Join(s.Dir, s.Name)
	if err := os.Rename(s.PartAbs, targetAbs); err != nil {
		return fmt.Errorf("完成上传失败：%w", err)
	}
	return s.Store.DeleteUpload(context.Background(), s.ID)
}

// Abort removes the part file and the session.
func (s *Session) Abort() error {
	os.Remove(s.PartAbs)
	return s.Store.DeleteUpload(context.Background(), s.ID)
}

// Cleanup deletes sessions older than MaxAge together with their part files.
func Cleanup(st *store.Store, now time.Time) (int, error) {
	expired, err := st.ExpiredUploads(context.Background(), now.Add(-MaxAge))
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, u := range expired {
		if err := os.Remove(u.PartPath); err != nil && !os.IsNotExist(err) {
			continue
		}
		if err := st.DeleteUpload(context.Background(), u.ID); err != nil {
			continue
		}
		removed++
	}
	return removed, nil
}

// CopyFromReader writes r into the part file (used by tests and by the simple
// whole-file path when a client cannot chunk).
func (s *Session) CopyFromReader(r io.Reader) error {
	f, err := os.OpenFile(s.PartAbs, os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// ErrIncomplete reports how many chunks are still missing.
type ErrIncomplete struct{ Missing int }

func (e ErrIncomplete) Error() string { return fmt.Sprintf("还有 %d 个分块未上传", e.Missing) }
