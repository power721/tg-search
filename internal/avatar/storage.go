package avatar

import (
	"fmt"
	"os"
	"path/filepath"
)

// AvatarPath returns the relative path for an avatar file.
// typ is "account" or "channel".
func AvatarPath(typ string, id int64, photoID int64) string {
	return filepath.Join("avatars", typ, fmt.Sprintf("%d", id), fmt.Sprintf("%d.jpg", photoID))
}

// AvatarAbsolutePath returns the absolute path for an avatar file.
func AvatarAbsolutePath(storageRoot string, typ string, id int64, photoID int64) string {
	return filepath.Join(storageRoot, AvatarPath(typ, id, photoID))
}

// FileExists returns true if the file exists at the given path.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WriteAvatarFile writes avatar data atomically to the given path.
// Creates parent directories if needed.
func WriteAvatarFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create avatar dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".avatar-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit avatar file: %w", err)
	}
	return nil
}
