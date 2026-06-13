package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"tg-search/internal/config"
	"tg-search/internal/storage"
)

func TestAvatarCacheGetWithNilCache(t *testing.T) {
	h := handlers{
		deps: Dependencies{
			Logger:      zap.NewNop(),
			AvatarCache: nil,
		},
	}

	entry, hit := h.avatarCacheGet(context.Background(), "test-key")
	if hit {
		t.Errorf("expected cache miss with nil cache, got hit")
	}
	if len(entry.Data) != 0 {
		t.Errorf("expected empty data with nil cache, got %d bytes", len(entry.Data))
	}
}

func TestAvatarCacheSetWithNilCache(t *testing.T) {
	h := handlers{
		deps: Dependencies{
			Logger:      zap.NewNop(),
			AvatarCache: nil,
		},
	}

	// Should not panic
	h.avatarCacheSet(context.Background(), "test-key", []byte("test-data"))
}

func TestAvatarCacheBackwardCompatibility(t *testing.T) {
	// Test that legacy avatar cache helpers still work for media_proxy.go
	tempDir := t.TempDir()
	avatarDir := filepath.Join(tempDir, "avatars")

	cfg := config.Config{
		Storage: config.StorageConfig{
			Path:          tempDir,
			MaxMediaCache: config.Size(100 * 1024 * 1024),
		},
	}

	avatarCache := storage.NewMediaCacheWithOptions(storage.MediaCacheOptions{
		Root:     avatarDir,
		MaxBytes: int64(cfg.Storage.MaxMediaCache),
		TTL:      30 * 24 * 3600 * 1000000000, // 30 days in nanoseconds
	})

	h := handlers{
		deps: Dependencies{
			Logger:      zap.NewNop(),
			AvatarCache: avatarCache,
		},
	}

	testKey := "test-avatar:123:456"
	testData := []byte("fake-avatar-image-data")

	// Test cache set
	h.avatarCacheSet(context.Background(), testKey, testData)

	// Verify cache file was created in avatars directory
	if _, err := os.Stat(avatarDir); os.IsNotExist(err) {
		t.Fatalf("avatars directory was not created")
	}

	// Test cache get
	entry, hit := h.avatarCacheGet(context.Background(), testKey)
	if !hit {
		t.Fatalf("expected cache hit, got miss")
	}
	if string(entry.Data) != string(testData) {
		t.Fatalf("expected cached data %q, got %q", testData, entry.Data)
	}
}
