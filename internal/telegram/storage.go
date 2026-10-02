package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"currency-converter-bot/internal/fsutil"
)

// jsonStore serializes saves of one JSON file. The snapshot is taken while the
// save lock is held, so a slower save can never overwrite newer state with an
// older copy, and every save goes through an atomic rename.
type jsonStore struct {
	mu       sync.Mutex
	path     string
	disabled error
}

func newJSONStore(path string) *jsonStore {
	return &jsonStore{path: strings.TrimSpace(path)}
}

func (s *jsonStore) save(snapshot func() any) error {
	if s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disabled != nil {
		return fmt.Errorf("saving %s is disabled: %w", s.path, s.disabled)
	}
	raw, err := json.MarshalIndent(snapshot(), "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.path, raw, 0o600)
}

// load passes the file content to decode; a missing file is not an error.
// A file that cannot be decoded is moved aside and kept for manual recovery,
// so the next save does not silently replace everyone's data with an empty
// state. If it cannot be moved, saving is disabled for the same reason.
func (s *jsonStore) load(decode func([]byte) error) error {
	if s.path == "" {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.disable(err)
		return err
	}
	if err := decode(raw); err != nil {
		target := s.path + ".corrupt-" + time.Now().UTC().Format("20060102T150405")
		if moveErr := os.Rename(s.path, target); moveErr != nil {
			s.disable(err)
			return fmt.Errorf("decode %s: %w; could not move it aside, saving disabled: %v", s.path, err, moveErr)
		}
		return fmt.Errorf("decode %s: %w; moved to %s", s.path, err, target)
	}
	return nil
}

func (s *jsonStore) disable(err error) {
	s.mu.Lock()
	s.disabled = err
	s.mu.Unlock()
}
