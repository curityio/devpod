package file

import (
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to name so that a concurrent reader sees either
// the old or the new content, never a truncated file: it writes a temporary
// file in the same directory and renames it over name. If the rename fails,
// e.g. on Windows while another process has name open, it falls back to
// os.WriteFile.
func WriteFileAtomic(name string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".tmp-*")
	if err != nil {
		return os.WriteFile(name, data, perm)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(tmpName, perm)
	}
	if err == nil {
		err = os.Rename(tmpName, name)
	}
	if err != nil {
		return os.WriteFile(name, data, perm)
	}

	return nil
}
