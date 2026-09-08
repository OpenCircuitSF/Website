package media

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// WriteAtomic writes data to <dir>/<name> without ever leaving a partial or
// zero-length file behind for Apache to serve, and without silently losing
// data (issues/0433.md Plan §4):
//
//  1. Create a temp file IN dir — never under os.TempDir(): opencircuit's
//     systemd unit sets PrivateTmp=true, so /tmp is a different mount and a
//     cross-device os.Rename would fail with EXDEV.
//  2. Write data to it.
//  3. Sync it. This step is not optional: without it, a buffered write can
//     report success and then lose the data when the file is later closed
//     or the process dies, especially under ENOSPC.
//  4. Chmod it 0644 — os.CreateTemp creates 0600, and Apache reads the file
//     as a third user (neither the owner nor the group) through the
//     "other" permission bits, so an un-chmodded upload would 403.
//  5. Close it, then os.Rename it into place. A same-directory rename is
//     atomic on the filesystem, so a concurrent reader never observes a
//     half-written file and an existing file at the destination is never
//     left truncated.
//
// Every error path removes the temp file. ENOSPC can surface from the write
// or from Sync — callers should check IsOutOfSpace(err) and respond with
// something naming disk space rather than a generic failure.
func WriteAtomic(dir, name string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".upload-*.tmp")
	if err != nil {
		return fmt.Errorf("media: creating temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("media: writing %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("media: syncing %s: %w", tmpPath, err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("media: chmod %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("media: closing %s: %w", tmpPath, err)
	}

	dest := filepath.Join(dir, name)
	if err := os.Rename(tmpPath, dest); err != nil {
		return fmt.Errorf("media: renaming into place: %w", err)
	}
	renamed = true
	return nil
}

// IsOutOfSpace reports whether err (directly or wrapped) is ENOSPC — "no
// space left on device" — surfaced from WriteAtomic's write or sync step.
func IsOutOfSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}

// CheckWritable reports whether dir is configured and writable by actually
// writing and removing a small probe file — the check issues/0433.md's plan
// specifies for the startup warning (no statfs preflight is used anywhere in
// this package; see media.go's package doc comment on why a real
// write/remove is the only check that matters). An empty dir is reported as
// not configured rather than attempting anything.
func CheckWritable(dir string) error {
	if dir == "" {
		return errors.New("media: directory not configured")
	}
	f, err := os.CreateTemp(dir, ".probe-*.tmp")
	if err != nil {
		return fmt.Errorf("media: %s is not writable: %w", dir, err)
	}
	name := f.Name()
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(name)
		return fmt.Errorf("media: closing probe file in %s: %w", dir, cerr)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("media: removing probe file in %s: %w", dir, err)
	}
	return nil
}
