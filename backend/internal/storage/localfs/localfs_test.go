package localfs_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mazhit-a/mini-kedo/internal/storage"
	"github.com/mazhit-a/mini-kedo/internal/storage/localfs"
)

func newStore(t *testing.T) (*localfs.Storage, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "files")
	s, err := localfs.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return s, root
}

func TestPutOpenRoundTrip(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	data := bytes.Repeat([]byte("abc"), 100000)

	key, size, err := s.Put(ctx, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(data)) {
		t.Fatalf("size = %d, want %d", size, len(data))
	}
	rc, err := s.Open(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("content mismatch")
	}
}

func TestPutKeysAreUniqueUUIDs(t *testing.T) {
	s, _ := newStore(t)
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		key, _, err := s.Put(context.Background(), strings.NewReader("x"))
		if err != nil {
			t.Fatal(err)
		}
		if len(key) != 36 || seen[key] {
			t.Fatalf("bad or duplicate key %q", key)
		}
		seen[key] = true
	}
}

func TestDelete(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	key, _, err := s.Put(ctx, strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("second delete should be a no-op, got %v", err)
	}
}

func TestOpenMissing(t *testing.T) {
	s, _ := newStore(t)
	_, err := s.Open(context.Background(), "3f2b1c9e-5d4a-4b7e-8c1f-0a9d8e7f6b5a")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestKeyTraversalRejected(t *testing.T) {
	s, root := newStore(t)
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := []string{
		"",
		"..",
		"../secret.txt",
		"../../etc/passwd",
		"/etc/passwd",
		outside,
		"a/b",
		`..\secret.txt`,
		"secret.txt",
		"3f2b1c9e-5d4a-4b7e-8c1f-0a9d8e7f6b5a/../secret.txt",
		"3f2b1c9e-5d4a-4b7e-8c1f-0a9d8e7f6b5a\x00",
		"3F2B1C9E-5D4A-4B7E-8C1F-0A9D8E7F6B5A",
	}
	ctx := context.Background()
	for _, k := range keys {
		if _, err := s.Open(ctx, k); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("Open(%q) = %v, want ErrInvalidKey", k, err)
		}
		if err := s.Delete(ctx, k); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("Delete(%q) = %v, want ErrInvalidKey", k, err)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("file outside root was touched: %v", err)
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errors.New("boom")
	}
	n := min(len(p), f.n)
	f.n -= n
	return n, nil
}

func TestFailedPutLeavesNoFiles(t *testing.T) {
	s, root := newStore(t)
	if _, _, err := s.Put(context.Background(), &failingReader{n: 1000}); err == nil {
		t.Fatal("expected error")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("want empty root after failed put, got %d entries", len(entries))
	}
}

func TestPutCancelledContext(t *testing.T) {
	s, root := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.Put(ctx, strings.NewReader("data")); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 0 {
		t.Fatalf("want empty root, got %d entries", len(entries))
	}
}

func TestCheck(t *testing.T) {
	s, root := newStore(t)
	if err := s.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("Check left %d files behind", len(entries))
	}

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := s.Check(context.Background()); err == nil {
		t.Fatal("expected error when storage dir is gone")
	}
}
