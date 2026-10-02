package fsutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteFileAtomicReplacesContentWithPerm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if err := WriteFileAtomic(path, []byte(`{"a":1}`), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if err := WriteFileAtomic(path, []byte(`{"a":2}`), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != `{"a":2}` {
		t.Fatalf("content = %q, %v", raw, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("perm = %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestWriteFileAtomicConcurrentWritersNeverTear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			payload := fmt.Sprintf(`{"writer":%d,"padding":%q}`, i, strings.Repeat("x", 4096))
			if err := WriteFileAtomic(path, []byte(payload), 0o600); err != nil {
				t.Errorf("WriteFileAtomic: %v", err)
			}
		}()
	}
	wg.Wait()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("file is torn after concurrent writes: %v", err)
	}
}
