// Package fsutil holds file helpers shared by the bot's JSON stores and caches.
package fsutil

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic replaces path with data so that readers and a crash at any
// moment see either the old or the new content, never a torn file. Each call
// uses its own temporary file, so concurrent writers cannot interleave bytes;
// the last rename wins. The parent directory is created with 0700.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// QuarantineFile moves an unreadable file aside so it is kept for manual
// recovery instead of being overwritten by the next save.
func QuarantineFile(path, suffix string) (string, error) {
	target := path + ".corrupt-" + suffix
	return target, os.Rename(path, target)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
