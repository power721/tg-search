package avatar

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAvatarPath(t *testing.T) {
	tests := []struct {
		name     string
		typ      string
		id       int64
		photoID  int64
		want     string
	}{
		{"account avatar", "account", 123, 456789, "avatars/account/123/456789.jpg"},
		{"channel avatar", "channel", 999, 111222, "avatars/channel/999/111222.jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AvatarPath(tt.typ, tt.id, tt.photoID)
			if got != tt.want {
				t.Errorf("AvatarPath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAvatarAbsolutePath(t *testing.T) {
	root := "/data/tg-search"
	got := AvatarAbsolutePath(root, "account", 123, 456789)
	want := filepath.Join(root, "avatars/account/123/456789.jpg")
	if got != want {
		t.Errorf("AvatarAbsolutePath() = %v, want %v", got, want)
	}
}

func TestFileExists(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "test.jpg")

	if FileExists(path) {
		t.Error("FileExists() = true for non-existent file, want false")
	}

	if err := os.WriteFile(path, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	if !FileExists(path) {
		t.Error("FileExists() = false for existing file, want true")
	}
}

func TestWriteAvatarFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "avatars", "account", "123", "456.jpg")
	data := []byte("test avatar data")

	if err := WriteAvatarFile(path, data); err != nil {
		t.Fatalf("WriteAvatarFile() error = %v", err)
	}

	if !FileExists(path) {
		t.Error("file not created")
	}

	readData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(readData) != string(data) {
		t.Errorf("file content = %q, want %q", readData, data)
	}
}
