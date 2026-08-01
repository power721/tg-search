package config

type RuntimeSettings struct {
	Sync      RuntimeSyncSettings     `json:"sync"`
	Storage   RuntimeStorageSettings  `json:"storage"`
	Telegram  RuntimeTelegramSettings `json:"telegram"`
	AI        AIConfig                `json:"ai"`
	LinkCheck LinkCheckConfig         `json:"link_check"`
}

type RuntimeSyncSettings struct {
	Workers                 int      `json:"workers"`
	HistoryBatchSize        int      `json:"history_batch_size"`
	TelegramRequestInterval Duration `json:"telegram_request_interval"`
}

type RuntimeStorageSettings struct {
	MaxDBSize     Size `json:"max_db_size"`
	MaxMediaCache Size `json:"max_media_cache"`
}

type RuntimeTelegramSettings struct {
	Proxy            string                  `json:"proxy"`
	ReconnectTimeout Duration                `json:"reconnect_timeout"`
	DialTimeout      Duration                `json:"dial_timeout"`
	RateLimit        TelegramRateLimitConfig `json:"rate_limit"`
	Stream           TelegramStreamConfig    `json:"stream"`
	Media            TelegramMediaConfig     `json:"media"`
}

func RuntimeSettingsFromConfig(cfg Config) RuntimeSettings {
	return RuntimeSettings{
		Sync: RuntimeSyncSettings{
			Workers:                 cfg.Sync.Workers,
			HistoryBatchSize:        cfg.Sync.HistoryBatchSize,
			TelegramRequestInterval: cfg.Sync.TelegramRequestInterval,
		},
		Storage: RuntimeStorageSettings{
			MaxDBSize:     cfg.Storage.MaxDBSize,
			MaxMediaCache: cfg.Storage.MaxMediaCache,
		},
		Telegram: RuntimeTelegramSettings{
			Proxy:            cfg.Telegram.Proxy,
			ReconnectTimeout: cfg.Telegram.ReconnectTimeout,
			DialTimeout:      cfg.Telegram.DialTimeout,
			RateLimit:        cfg.Telegram.RateLimit,
			Stream:           cfg.Telegram.Stream,
			Media:            cfg.Telegram.Media,
		},
		AI:        cfg.AI,
		LinkCheck: cfg.LinkCheck,
	}
}

func ApplyRuntimeSettings(cfg Config, settings RuntimeSettings) (Config, error) {
	applyDefaults(&cfg)
	cfg.Sync.Workers = settings.Sync.Workers
	cfg.Sync.HistoryBatchSize = settings.Sync.HistoryBatchSize
	cfg.Sync.TelegramRequestInterval = settings.Sync.TelegramRequestInterval
	cfg.Storage.MaxDBSize = settings.Storage.MaxDBSize
	cfg.Storage.MaxMediaCache = settings.Storage.MaxMediaCache
	cfg.Telegram.Proxy = settings.Telegram.Proxy
	cfg.Telegram.ReconnectTimeout = settings.Telegram.ReconnectTimeout
	cfg.Telegram.DialTimeout = settings.Telegram.DialTimeout
	cfg.Telegram.RateLimit = settings.Telegram.RateLimit
	cfg.Telegram.Stream = settings.Telegram.Stream
	cfg.Telegram.Media = settings.Telegram.Media
	cfg.AI = settings.AI
	cfg.LinkCheck = settings.LinkCheck
	if err := validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

type RuntimeSettingsResponse struct {
	Sync      RuntimeSyncSettings       `json:"sync"`
	Storage   RuntimeStorageSettings    `json:"storage"`
	Telegram  RuntimeTelegramSettings   `json:"telegram"`
	AI        RuntimeAISettingsResponse `json:"ai"`
	LinkCheck LinkCheckConfig           `json:"link_check"`
}

type RuntimeAISettingsResponse struct {
	MediaMetadata AIMediaMetadataSettingsResponse `json:"media_metadata"`
}

type AIMediaMetadataSettingsResponse struct {
	Enabled         bool                                      `json:"enabled"`
	Provider        string                                    `json:"provider"`
	BaseURL         string                                    `json:"base_url"`
	Model           string                                    `json:"model"`
	FallbackEnabled bool                                      `json:"fallback_enabled"`
	APIKeySet       bool                                      `json:"api_key_set"`
	Providers       []AIMediaMetadataProviderSettingsResponse `json:"providers"`
}

type AIMediaMetadataProviderSettingsResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	Enabled   bool   `json:"enabled"`
	APIKeySet bool   `json:"api_key_set"`
}

func PreserveRuntimeSecrets(incoming RuntimeSettings, existing RuntimeSettings) RuntimeSettings {
	if incoming.AI.MediaMetadata.APIKey == "" {
		incoming.AI.MediaMetadata.APIKey = existing.AI.MediaMetadata.APIKey
	}
	existingProviderKeys := map[string]string{}
	for _, provider := range existing.AI.MediaMetadata.EffectiveProviders() {
		if provider.ID != "" && provider.APIKey != "" {
			existingProviderKeys[provider.ID] = provider.APIKey
		}
	}
	for i := range incoming.AI.MediaMetadata.Providers {
		provider := &incoming.AI.MediaMetadata.Providers[i]
		if provider.APIKey == "" && provider.ID != "" {
			provider.APIKey = existingProviderKeys[provider.ID]
		}
	}
	return incoming
}

func RedactRuntimeSettings(settings RuntimeSettings) RuntimeSettingsResponse {
	return RuntimeSettingsResponse{
		Sync:      settings.Sync,
		Storage:   settings.Storage,
		Telegram:  settings.Telegram,
		LinkCheck: settings.LinkCheck,
		AI: RuntimeAISettingsResponse{
			MediaMetadata: AIMediaMetadataSettingsResponse{
				Enabled:         settings.AI.MediaMetadata.Enabled,
				Provider:        settings.AI.MediaMetadata.Provider,
				BaseURL:         settings.AI.MediaMetadata.BaseURL,
				Model:           settings.AI.MediaMetadata.Model,
				FallbackEnabled: settings.AI.MediaMetadata.FallbackEnabled,
				APIKeySet:       settings.AI.MediaMetadata.APIKey != "",
				Providers:       redactAIMediaMetadataProviders(settings.AI.MediaMetadata.EffectiveProviders()),
			},
		},
	}
}

func redactAIMediaMetadataProviders(providers []AIMediaMetadataProviderSettings) []AIMediaMetadataProviderSettingsResponse {
	out := make([]AIMediaMetadataProviderSettingsResponse, 0, len(providers))
	for _, provider := range providers {
		out = append(out, AIMediaMetadataProviderSettingsResponse{
			ID:        provider.ID,
			Name:      provider.Name,
			Provider:  provider.Provider,
			BaseURL:   provider.BaseURL,
			Model:     provider.Model,
			Enabled:   provider.Enabled,
			APIKeySet: provider.APIKey != "",
		})
	}
	return out
}
