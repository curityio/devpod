package file

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestWriteFileAtomic(t *testing.T) {
	name := filepath.Join(t.TempDir(), "workspace.json")

	if err := WriteFileAtomic(name, []byte(`{"id":"a"}`), 0600); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := WriteFileAtomic(name, []byte(`{"id":"b"}`), 0644); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != `{"id":"b"}` {
		t.Errorf("content = %q, want %q", got, `{"id":"b"}`)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0644 {
			t.Errorf("mode = %v, want 0644", info.Mode().Perm())
		}
	}

	entries, err := os.ReadDir(filepath.Dir(name))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want only the target file", len(entries))
	}
}

// TestWriteFileAtomic_ReadersNeverSeeEmptyFile checks that a reader racing
// with writers always reads one complete version of the file.
func TestWriteFileAtomic_ReadersNeverSeeEmptyFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the rename may fall back to a plain write while the reader has the file open")
	}
	name := filepath.Join(t.TempDir(), "workspace.json")
	a := bytes.Repeat([]byte("a"), 64*1024)
	b := bytes.Repeat([]byte("b"), 64*1024)
	if err := WriteFileAtomic(name, a, 0644); err != nil {
		t.Fatalf("initial write: %v", err)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			data := a
			if i%2 == 1 {
				data = b
			}
			if err := WriteFileAtomic(name, data, 0644); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	}()

	for i := 0; i < 2000; i++ {
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
			t.Fatalf("read a partial file of %d bytes", len(got))
		}
	}
	close(done)
	wg.Wait()
}
