package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomic_HappyPath(t *testing.T) {
	dir := t.TempDir()
	data := []byte("hello, media")

	if err := WriteAtomic(dir, "photo.jpg", data); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	dest := filepath.Join(dir, "photo.jpg")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("written content = %q, want %q", got, data)
	}

	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	// os.CreateTemp creates 0600; WriteAtomic must chmod to 0644 or Apache
	// (a third user, reading through the "other" bits) 403s every upload.
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("file mode = %o, want %o", got, 0o644)
	}

	// No leftover temp file.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected exactly 1 file in dir, got %v", names)
	}
}

func TestWriteAtomic_NoPartialFileOnFailure(t *testing.T) {
	// A directory that does not exist: CreateTemp fails immediately, before
	// any bytes are written anywhere.
	dir := filepath.Join(t.TempDir(), "does-not-exist")

	if err := WriteAtomic(dir, "photo.jpg", []byte("data")); err == nil {
		t.Fatal("expected an error writing into a nonexistent directory")
	}

	if _, err := os.Stat(filepath.Join(dir, "photo.jpg")); !os.IsNotExist(err) {
		t.Errorf("expected no destination file, stat returned: %v", err)
	}
}

func TestWriteAtomic_OverwritesExistingFileAtomically(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(dest, []byte("old content, longer than the new content"), 0o644); err != nil {
		t.Fatalf("seeding existing file: %v", err)
	}

	if err := WriteAtomic(dir, "photo.jpg", []byte("new")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("content = %q, want %q — a same-directory rename must fully replace the old file, never leave a truncated mix", got, "new")
	}
}

func TestWriteAtomic_DoesNotLeaveTempFileOnRenameOfSameName(t *testing.T) {
	dir := t.TempDir()
	if err := WriteAtomic(dir, "a.jpg", []byte("one")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteAtomic(dir, "b.jpg", []byte("two")); err != nil {
		t.Fatalf("second write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 2 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected exactly 2 files, got %v", names)
	}
}

func TestCheckWritable(t *testing.T) {
	t.Run("writable dir", func(t *testing.T) {
		dir := t.TempDir()
		if err := CheckWritable(dir); err != nil {
			t.Errorf("CheckWritable(%q): %v", dir, err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("CheckWritable left a probe file behind: %v", entries)
		}
	})

	t.Run("unconfigured", func(t *testing.T) {
		if err := CheckWritable(""); err == nil {
			t.Error("expected an error for an empty (unconfigured) directory")
		}
	})

	t.Run("nonexistent dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "does-not-exist")
		if err := CheckWritable(dir); err == nil {
			t.Error("expected an error for a nonexistent directory")
		}
	})
}
