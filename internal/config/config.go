package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultPath = "/data/tg-search/config.yaml"

var (
	defaultConfigPath = DefaultPath
	localConfigPath   = "config.yaml"
)

type Config struct {
	Server        ServerConfig        `yaml:"server" json:"server"`
	Sync          SyncConfig          `yaml:"sync" json:"sync"`
	Storage       StorageConfig       `yaml:"storage" json:"storage"`
	Telegram      TelegramConfig      `yaml:"telegram" json:"telegram"`
	AI            AIConfig            `yaml:"ai" json:"ai"`
	Bot           BotConfig           `yaml:"bot" json:"bot"`
	TaskRetention TaskRetentionConfig `yaml:"task_retention" json:"task_retention"`
	LinkCheck     LinkCheckConfig     `yaml:"link_check" json:"link_check"`
}

// LinkCheckConfig tunes the /api/check/links pan-link checker.
type LinkCheckConfig struct {
	Concurrency int      `yaml:"concurrency" json:"concurrency"` // flat worker-pool size
	CacheTTL    Duration `yaml:"cache_ttl" json:"cache_ttl"`     // TTL for memoized definitive results (0 disables)
}

type ServerConfig struct {
	Host string `yaml:"host" json:"host"`
	Port int    `yaml:"port" json:"port"`
}

type SyncConfig struct {
	Workers                 int      `yaml:"workers" json:"workers"`
	HistoryBatchSize        int      `yaml:"history_batch_size" json:"history_batch_size"`
	TelegramRequestInterval Duration `yaml:"telegram_request_interval" json:"telegram_request_interval"`
}

type StorageConfig struct {
	Path          string `yaml:"path" json:"path"`
	MaxDBSize     Size   `yaml:"max_db_size" json:"max_db_size"`
	MaxMediaCache Size   `yaml:"max_media_cache" json:"max_media_cache"`
}

type TelegramConfig struct {
	Proxy            string                  `yaml:"proxy" json:"proxy"`
	ReconnectTimeout Duration                `yaml:"reconnect_timeout" json:"reconnect_timeout"`
	DialTimeout      Duration                `yaml:"dial_timeout" json:"dial_timeout"`
	RateLimit        TelegramRateLimitConfig `yaml:"rate_limit" json:"rate_limit"`
	Stream           TelegramStreamConfig    `yaml:"stream" json:"stream"`
	Media            TelegramMediaConfig     `yaml:"media" json:"media"`
}

type TelegramRateLimitConfig struct {
	Enabled       bool `yaml:"enabled" json:"enabled"`
	RatePerSecond int  `yaml:"rate_per_second" json:"rate_per_second"`
	Burst         int  `yaml:"burst" json:"burst"`
}

type TelegramStreamConfig struct {
	Concurrency  int      `yaml:"concurrency" json:"concurrency"`
	Buffers      int      `yaml:"buffers" json:"buffers"`
	ChunkTimeout Duration `yaml:"chunk_timeout" json:"chunk_timeout"`
}

type TelegramMediaConfig struct {
	Concurrency int `yaml:"concurrency" json:"concurrency"`
}

type AIConfig struct {
	MediaMetadata AIMediaMetadataSettings `yaml:"media_metadata" json:"media_metadata"`
}

type AIMediaMetadataSettings struct {
	Enabled         bool                              `yaml:"enabled" json:"enabled"`
	Provider        string                            `yaml:"provider" json:"provider,omitempty"`
	BaseURL         string                            `yaml:"base_url" json:"base_url"`
	APIKey          string                            `yaml:"api_key" json:"api_key,omitempty"`
	Model           string                            `yaml:"model" json:"model"`
	FallbackEnabled bool                              `yaml:"fallback_enabled" json:"fallback_enabled"`
	Providers       []AIMediaMetadataProviderSettings `yaml:"providers" json:"providers,omitempty"`
}

type AIMediaMetadataProviderSettings struct {
	ID       string `yaml:"id" json:"id"`
	Name     string `yaml:"name" json:"name,omitempty"`
	Provider string `yaml:"provider" json:"provider"`
	BaseURL  string `yaml:"base_url" json:"base_url"`
	APIKey   string `yaml:"api_key" json:"api_key,omitempty"`
	Model    string `yaml:"model" json:"model"`
	Enabled  bool   `yaml:"enabled" json:"enabled"`
}

func (s AIMediaMetadataSettings) EffectiveProviders() []AIMediaMetadataProviderSettings {
	if len(s.Providers) > 0 {
		out := make([]AIMediaMetadataProviderSettings, 0, len(s.Providers))
		for _, provider := range s.Providers {
			if provider.ID == "" {
				provider.ID = provider.Provider
			}
			out = append(out, provider)
		}
		return out
	}
	if strings.TrimSpace(s.Provider) == "" && strings.TrimSpace(s.BaseURL) == "" && strings.TrimSpace(s.Model) == "" {
		return nil
	}
	id := strings.TrimSpace(s.Provider)
	if id == "" {
		id = "openai_compatible"
	}
	return []AIMediaMetadataProviderSettings{{
		ID:       id,
		Provider: s.Provider,
		BaseURL:  s.BaseURL,
		APIKey:   s.APIKey,
		Model:    s.Model,
		Enabled:  true,
	}}
}

type BotConfig struct {
	Enabled      bool     `yaml:"enabled" json:"enabled"`
	Token        string   `yaml:"token" json:"-"`
	PollInterval Duration `yaml:"poll_interval" json:"poll_interval"`
}

type TaskRetentionConfig struct {
	SucceededDays    int `yaml:"succeeded_days" json:"succeeded_days"`
	FailedDays       int `yaml:"failed_days" json:"failed_days"`
	CanceledDays     int `yaml:"canceled_days" json:"canceled_days"`
	PausedDays       int `yaml:"paused_days" json:"paused_days"`
	FloodWaitDays    int `yaml:"flood_wait_days" json:"flood_wait_days"`
	ReconnectingDays int `yaml:"reconnecting_days" json:"reconnecting_days"`
}

func Load(path string) (Config, error) {
	cfg := defaultConfig()
	resolved, err := resolvePath(path)
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", resolved, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", resolved, err)
	}
	applyDefaults(&cfg)
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Address(cfg Config) string {
	return fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
}

func DatabasePath(cfg Config) string {
	return filepath.Join(cfg.Storage.Path, "tg-search.db")
}

func EnsureRuntimeDirs(cfg Config) error {
	if cfg.Storage.Path == "" {
		return errors.New("storage.path is required")
	}
	for _, path := range RuntimeDirs(cfg) {
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("create runtime directory %s: %w", path, err)
		}
	}
	return ValidateRuntimeDirs(cfg)
}

func ValidateRuntimeDirs(cfg Config) error {
	if cfg.Storage.Path == "" {
		return errors.New("storage.path is required")
	}
	for _, path := range RuntimeDirs(cfg) {
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("runtime directory %s is missing", path)
		}
		if err != nil {
			return fmt.Errorf("stat runtime directory %s: %w", path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("runtime path %s is not a directory", path)
		}
		if err := probeWritable(path); err != nil {
			return fmt.Errorf("runtime directory %s is not writable: %w", path, err)
		}
	}
	return nil
}

func RuntimeDirs(cfg Config) []string {
	root := cfg.Storage.Path
	return []string{
		root,
		filepath.Join(root, "sessions"),
		filepath.Join(root, "logs"),
		filepath.Join(root, "backup"),
		filepath.Join(root, "uploads"),
		filepath.Join(root, "index"),
		filepath.Join(root, "thumbnails"),
		filepath.Join(root, "avatars"),
	}
}

func probeWritable(dir string) error {
	file, err := os.CreateTemp(dir, ".tg-search-write-test-*")
	if err != nil {
		return err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func defaultConfig() Config {
	return Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 9900,
		},
		Sync: SyncConfig{
			Workers:                 5,
			HistoryBatchSize:        100,
			TelegramRequestInterval: Duration(2 * time.Second),
		},
		Storage: StorageConfig{
			Path:          "/data/tg-search",
			MaxDBSize:     Size(10 * sizeGB),
			MaxMediaCache: Size(20 * sizeGB),
		},
		Telegram: TelegramConfig{
			ReconnectTimeout: Duration(5 * time.Minute),
			DialTimeout:      Duration(10 * time.Second),
			RateLimit: TelegramRateLimitConfig{
				Enabled:       true,
				RatePerSecond: 10,
				Burst:         5,
			},
			Stream: TelegramStreamConfig{
				Concurrency:  2,
				Buffers:      4,
				ChunkTimeout: Duration(20 * time.Second),
			},
			Media: TelegramMediaConfig{
				Concurrency: 2,
			},
		},
		Bot: BotConfig{
			PollInterval: Duration(3 * time.Second),
		},
		TaskRetention: TaskRetentionConfig{
			SucceededDays:    7,
			FailedDays:       30,
			CanceledDays:     7,
			PausedDays:       30,
			FloodWaitDays:    30,
			ReconnectingDays: 7,
		},
		LinkCheck: LinkCheckConfig{
			Concurrency: 128,
			CacheTTL:    Duration(5 * time.Minute),
		},
	}
}

func resolvePath(path string) (string, error) {
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		cfg := localRuntimeConfig()
		if filepath.Clean(path) == filepath.Clean(DefaultPath) {
			cfg = dockerRuntimeConfig()
		}
		if err := writeGeneratedConfig(path, cfg); err != nil {
			return "", err
		}
		return path, nil
	}
	if _, err := os.Stat(defaultConfigPath); err == nil {
		return defaultConfigPath, nil
	}
	if _, err := os.Stat(localConfigPath); err == nil {
		return localConfigPath, nil
	}
	if err := writeGeneratedConfig(localConfigPath, localRuntimeConfig()); err != nil {
		return "", fmt.Errorf("generate default config: %w", err)
	}
	return localConfigPath, nil
}

func applyDefaults(cfg *Config) {
	defaults := defaultConfig()
	if cfg.Server.Host == "" {
		cfg.Server.Host = defaults.Server.Host
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = defaults.Server.Port
	}
	if cfg.Sync.Workers == 0 {
		cfg.Sync.Workers = defaults.Sync.Workers
	}
	if cfg.Sync.HistoryBatchSize == 0 {
		cfg.Sync.HistoryBatchSize = defaults.Sync.HistoryBatchSize
	}
	if cfg.Sync.TelegramRequestInterval == 0 {
		cfg.Sync.TelegramRequestInterval = defaults.Sync.TelegramRequestInterval
	}
	if cfg.Storage.Path == "" {
		cfg.Storage.Path = defaults.Storage.Path
	}
	if cfg.Storage.MaxDBSize == 0 {
		cfg.Storage.MaxDBSize = defaults.Storage.MaxDBSize
	}
	if cfg.Storage.MaxMediaCache == 0 {
		cfg.Storage.MaxMediaCache = defaults.Storage.MaxMediaCache
	}
	if cfg.Telegram.ReconnectTimeout == 0 {
		cfg.Telegram.ReconnectTimeout = defaults.Telegram.ReconnectTimeout
	}
	if cfg.Telegram.DialTimeout == 0 {
		cfg.Telegram.DialTimeout = defaults.Telegram.DialTimeout
	}
	if cfg.Telegram.RateLimit == (TelegramRateLimitConfig{}) {
		cfg.Telegram.RateLimit = defaults.Telegram.RateLimit
	}
	if cfg.Telegram.RateLimit.RatePerSecond == 0 {
		cfg.Telegram.RateLimit.RatePerSecond = defaults.Telegram.RateLimit.RatePerSecond
	}
	if cfg.Telegram.RateLimit.Burst == 0 {
		cfg.Telegram.RateLimit.Burst = defaults.Telegram.RateLimit.Burst
	}
	stream := cfg.Telegram.Stream
	if cfg.Telegram.Stream.Concurrency < 1 {
		if stream == (TelegramStreamConfig{}) {
			cfg.Telegram.Stream.Concurrency = defaults.Telegram.Stream.Concurrency
		} else {
			cfg.Telegram.Stream.Concurrency = 1
		}
	}
	if cfg.Telegram.Stream.Buffers < 1 {
		if stream == (TelegramStreamConfig{}) {
			cfg.Telegram.Stream.Buffers = defaults.Telegram.Stream.Buffers
		} else {
			cfg.Telegram.Stream.Buffers = 1
		}
	}
	if cfg.Telegram.Stream.ChunkTimeout.Std() <= 0 {
		cfg.Telegram.Stream.ChunkTimeout = defaults.Telegram.Stream.ChunkTimeout
	}
	if cfg.Telegram.Media.Concurrency == 0 {
		cfg.Telegram.Media.Concurrency = defaults.Telegram.Media.Concurrency
	}
	if cfg.Bot.PollInterval == 0 {
		cfg.Bot.PollInterval = defaults.Bot.PollInterval
	}
	if cfg.TaskRetention == (TaskRetentionConfig{}) {
		cfg.TaskRetention = defaults.TaskRetention
	}
	if cfg.LinkCheck.Concurrency < 1 {
		cfg.LinkCheck.Concurrency = defaults.LinkCheck.Concurrency
	}
	if cfg.LinkCheck.CacheTTL < 0 {
		cfg.LinkCheck.CacheTTL = defaults.LinkCheck.CacheTTL
	}
}

func validate(cfg Config) error {
	if cfg.Server.Host == "" {
		return errors.New("server.host is required")
	}
	if cfg.Server.Port <= 0 || cfg.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if cfg.Sync.Workers <= 0 {
		return errors.New("sync.workers must be greater than zero")
	}
	if cfg.Sync.HistoryBatchSize <= 0 {
		return errors.New("sync.history_batch_size must be greater than zero")
	}
	if cfg.Sync.TelegramRequestInterval < 0 {
		return errors.New("sync.telegram_request_interval must be non-negative")
	}
	if cfg.Storage.Path == "" {
		return errors.New("storage.path is required")
	}
	if cfg.Storage.MaxDBSize < minStorageLimitSize {
		return errors.New("storage.max_db_size must be at least 100MB")
	}
	if cfg.Storage.MaxMediaCache < minStorageLimitSize {
		return errors.New("storage.max_media_cache must be at least 100MB")
	}
	if cfg.Telegram.ReconnectTimeout <= 0 {
		return errors.New("telegram.reconnect_timeout must be greater than zero")
	}
	if cfg.Telegram.DialTimeout <= 0 {
		return errors.New("telegram.dial_timeout must be greater than zero")
	}
	if cfg.Telegram.RateLimit.RatePerSecond <= 0 {
		return errors.New("telegram.rate_limit.rate_per_second must be greater than zero")
	}
	if cfg.Telegram.RateLimit.Burst <= 0 {
		return errors.New("telegram.rate_limit.burst must be greater than zero")
	}
	if cfg.Telegram.Media.Concurrency <= 0 {
		return errors.New("telegram.media.concurrency must be greater than zero")
	}
	if cfg.AI.MediaMetadata.Enabled {
		if len(cfg.AI.MediaMetadata.Providers) == 0 {
			if strings.TrimSpace(cfg.AI.MediaMetadata.BaseURL) == "" {
				return errors.New("ai.media_metadata.base_url is required when ai.media_metadata.enabled is true")
			}
			if aiMediaMetadataRequiresAPIKey(cfg.AI.MediaMetadata) && strings.TrimSpace(cfg.AI.MediaMetadata.APIKey) == "" {
				return errors.New("ai.media_metadata.api_key is required when ai.media_metadata.enabled is true")
			}
			if strings.TrimSpace(cfg.AI.MediaMetadata.Model) == "" {
				return errors.New("ai.media_metadata.model is required when ai.media_metadata.enabled is true")
			}
		}
		for i, provider := range cfg.AI.MediaMetadata.Providers {
			if !provider.Enabled {
				continue
			}
			if strings.TrimSpace(provider.BaseURL) == "" {
				return fmt.Errorf("ai.media_metadata.providers[%d].base_url is required when ai.media_metadata.enabled is true", i)
			}
			if aiMediaMetadataProviderRequiresAPIKey(provider) && strings.TrimSpace(provider.APIKey) == "" {
				return fmt.Errorf("ai.media_metadata.providers[%d].api_key is required when ai.media_metadata.enabled is true", i)
			}
			if strings.TrimSpace(provider.Model) == "" {
				return fmt.Errorf("ai.media_metadata.providers[%d].model is required when ai.media_metadata.enabled is true", i)
			}
		}
	}
	if cfg.Bot.Enabled && cfg.Bot.Token == "" {
		return errors.New("bot.token is required when bot.enabled is true")
	}
	if cfg.Bot.PollInterval <= 0 {
		return errors.New("bot.poll_interval must be greater than zero")
	}
	if cfg.TaskRetention.SucceededDays < 0 {
		return errors.New("task_retention.succeeded_days must be >= 0")
	}
	if cfg.TaskRetention.FailedDays < 0 {
		return errors.New("task_retention.failed_days must be >= 0")
	}
	if cfg.TaskRetention.CanceledDays < 0 {
		return errors.New("task_retention.canceled_days must be >= 0")
	}
	if cfg.TaskRetention.PausedDays < 0 {
		return errors.New("task_retention.paused_days must be >= 0")
	}
	if cfg.TaskRetention.FloodWaitDays < 0 {
		return errors.New("task_retention.flood_wait_days must be >= 0")
	}
	if cfg.TaskRetention.ReconnectingDays < 0 {
		return errors.New("task_retention.reconnecting_days must be >= 0")
	}
	return nil
}

func aiMediaMetadataRequiresAPIKey(settings AIMediaMetadataSettings) bool {
	return strings.TrimSpace(settings.Provider) != "ollama"
}

func aiMediaMetadataProviderRequiresAPIKey(settings AIMediaMetadataProviderSettings) bool {
	return strings.TrimSpace(settings.Provider) != "ollama"
}

func writeGeneratedConfig(path string, cfg Config) error {
	data, err := yaml.Marshal(generatedFileConfig{
		Server: cfg.Server,
		Storage: generatedStorageConfig{
			Path: cfg.Storage.Path,
		},
	})
	if err != nil {
		return fmt.Errorf("marshal generated config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write generated config %s: %w", path, err)
	}
	return nil
}

type generatedFileConfig struct {
	Server  ServerConfig           `yaml:"server"`
	Storage generatedStorageConfig `yaml:"storage"`
}

type generatedStorageConfig struct {
	Path string `yaml:"path"`
}

func dockerRuntimeConfig() Config {
	cfg := generatedConfig()
	cfg.Server.Host = "0.0.0.0"
	cfg.Storage.Path = "/data/tg-search"
	return cfg
}

func localRuntimeConfig() Config {
	cfg := generatedConfig()
	cfg.Server.Host = "127.0.0.1"
	cfg.Storage.Path = "data"
	return cfg
}

func generatedConfig() Config {
	return defaultConfig()
}
