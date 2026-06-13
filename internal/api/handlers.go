package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tg-search/internal/adminauth"
	aipkg "tg-search/internal/ai"
	"tg-search/internal/backup"
	"tg-search/internal/build"
	channelpkg "tg-search/internal/channel"
	"tg-search/internal/config"
	"tg-search/internal/logviewer"
	"tg-search/internal/messagefilter"
	"tg-search/internal/model"
	"tg-search/internal/repository"
	"tg-search/internal/resource"
	searchsvc "tg-search/internal/search"
	taskpkg "tg-search/internal/task"
	"tg-search/internal/telegram"
	webui "tg-search/internal/web"
)

type handlers struct {
	deps Dependencies
}

const adminSessionCookie = "tg_search_session"
const (
	setupAPIKeyDoneKey      = "setup.api_key_done"
	setupListenRulesDoneKey = "setup.listen_rules_done"
)
const apiLoggerKey = "api.logger"
const apiKeyIDKey = "api.key_id"
const listenHistorySyncMaxMessages = 100

func (h handlers) health(c *gin.Context) {
	resp := gin.H{
		"service": "ok",
	}
	if key := apiKeyFromRequestHeader(c.Request); key != "" {
		if h.deps.APIKeyService == nil {
			errorText(c, http.StatusServiceUnavailable, "api key service is unavailable")
			return
		}
		_, ok, err := h.deps.APIKeyService.Verify(c.Request.Context(), key)
		if err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
		if !ok {
			errorText(c, http.StatusUnauthorized, "invalid api key")
			return
		}
		resp["version"] = build.Version
	}
	c.JSON(http.StatusOK, resp)
}

func (h handlers) ready(c *gin.Context) {
	checks := gin.H{}
	ready := true

	if h.deps.BackupDB == nil {
		ready = false
		checks["database"] = "missing"
	} else if err := h.deps.BackupDB.PingContext(c.Request.Context()); err != nil {
		ready = false
		checks["database"] = err.Error()
	} else {
		checks["database"] = "ok"
	}

	runtimeConfig := h.deps.RuntimeConfig
	if runtimeConfig.Storage.Path == "" && h.deps.StorageUsage != nil {
		runtimeConfig = h.deps.StorageUsage.Config()
	}
	if runtimeConfig.Storage.Path == "" {
		ready = false
		checks["runtime_dirs"] = "storage.path is required"
	} else if err := config.ValidateRuntimeDirs(runtimeConfig); err != nil {
		ready = false
		checks["runtime_dirs"] = err.Error()
	} else {
		checks["runtime_dirs"] = "ok"
	}

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, gin.H{
		"ready":  ready,
		"checks": checks,
	})
}

func (h handlers) setupStatus(c *gin.Context) {
	status, err := h.loadSetupStatus(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h handlers) frontend(c *gin.Context) {
	if c.Request.Method != http.MethodGet || strings.HasPrefix(c.Request.URL.Path, "/api/") {
		c.Status(http.StatusNotFound)
		return
	}
	dist, err := webui.Dist()
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	name := strings.TrimPrefix(path.Clean(c.Request.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	data, err := readFrontendFile(dist, name)
	if err != nil {
		data, err = readFrontendFile(dist, "index.html")
		name = "index.html"
	}
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" || name == "index.html" {
		contentType = "text/html; charset=utf-8"
	}
	if name == "index.html" {
		c.Header("Cache-Control", "no-cache")
	}
	c.Data(http.StatusOK, contentType, data)
}

func readFrontendFile(dist fs.FS, name string) ([]byte, error) {
	info, err := fs.Stat(dist, name)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fs.ErrInvalid
	}
	return fs.ReadFile(dist, name)
}

func (h handlers) setupAdmin(c *gin.Context) {
	count, err := h.deps.Users.Count(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		errorText(c, http.StatusForbidden, "admin already configured")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bindJSON(c, &req) {
		return
	}
	id, err := h.deps.AdminAuth.CreateAdmin(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "username": strings.TrimSpace(req.Username), "role": model.UserRoleAdmin})
}

func (h handlers) setupAPIKey(c *gin.Context) {
	if !h.allowSetupWrite(c) {
		return
	}
	resp, err := h.deps.APIKeyService.EnsureActive(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if err := h.deps.Settings.Set(c.Request.Context(), setupAPIKeyDoneKey, `true`); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

func (h handlers) getAPIKeySettings(c *gin.Context) {
	if !h.hasAdminSession(c) {
		errorText(c, http.StatusUnauthorized, "not authenticated")
		return
	}
	resp, err := h.deps.APIKeyService.Active(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h handlers) regenerateAPIKey(c *gin.Context) {
	if !h.hasAdminSession(c) {
		errorText(c, http.StatusUnauthorized, "not authenticated")
		return
	}
	resp, err := h.deps.APIKeyService.Regenerate(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

func (h handlers) updateAdminSettings(c *gin.Context) {
	cookie, user, ok := h.adminSession(c)
	if !ok {
		errorText(c, http.StatusUnauthorized, "not authenticated")
		return
	}
	var req struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !bindJSON(c, &req) {
		return
	}
	updated, err := h.deps.AdminAuth.UpdateCredentials(c.Request.Context(), user.ID, req.Username, req.CurrentPassword, req.NewPassword)
	if err != nil {
		if errors.Is(err, adminauth.ErrInvalidCredentials) {
			errorText(c, http.StatusUnauthorized, "current password is invalid")
			return
		}
		errorJSON(c, http.StatusBadRequest, err)
		return
	}
	if err := h.deps.AdminAuth.UpdateSession(c.Request.Context(), cookie, updated); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	updated.PasswordHash = ""
	c.JSON(http.StatusOK, updated)
}

func (h handlers) setupListenRules(c *gin.Context) {
	if !h.allowSetupWrite(c) {
		return
	}
	var req model.ListenRules
	if !bindJSON(c, &req) {
		return
	}
	rules, ok := h.validateListenRules(c, req)
	if !ok {
		return
	}
	if err := h.saveListenRules(c.Request.Context(), rules); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	status, err := h.loadSetupStatus(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h handlers) getListenRules(c *gin.Context) {
	raw, ok, err := h.deps.Settings.Get(c.Request.Context(), messagefilter.GlobalListenRulesSettingKey)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		c.JSON(http.StatusOK, emptyListenRules())
		return
	}
	var rules model.ListenRules
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		errorJSON(c, http.StatusInternalServerError, fmt.Errorf("decode listen rules: %w", err))
		return
	}
	c.JSON(http.StatusOK, normalizeListenRules(rules))
}

func (h handlers) updateListenRules(c *gin.Context) {
	var req model.ListenRules
	if !bindJSON(c, &req) {
		return
	}
	rules, ok := h.validateListenRules(c, req)
	if !ok {
		return
	}
	if err := h.saveListenRules(c.Request.Context(), rules); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, rules)
}

func (h handlers) validateListenRules(c *gin.Context, req model.ListenRules) (model.ListenRules, bool) {
	rules := normalizeListenRules(req)
	if len(rules.MessageTypes) == 0 {
		errorText(c, http.StatusBadRequest, "message_types is required")
		return model.ListenRules{}, false
	}
	if len(rules.LinkTypes) == 0 {
		errorText(c, http.StatusBadRequest, "link_types is required")
		return model.ListenRules{}, false
	}
	return rules, true
}

func (h handlers) saveListenRules(ctx context.Context, rules model.ListenRules) error {
	data, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	if err := h.deps.Settings.Set(ctx, messagefilter.GlobalListenRulesSettingKey, string(data)); err != nil {
		return err
	}
	return h.deps.Settings.Set(ctx, setupListenRulesDoneKey, `true`)
}

func normalizeListenRules(req model.ListenRules) model.ListenRules {
	ignoredLinkPatterns := req.IgnoredLinkPatterns
	if ignoredLinkPatterns == nil {
		ignoredLinkPatterns = messagefilter.DefaultIgnoredLinkPatterns()
	}
	return model.ListenRules{
		Includes:            normalizeSetupStrings(req.Includes),
		Excludes:            normalizeSetupStrings(req.Excludes),
		MessageTypes:        normalizeSetupStrings(req.MessageTypes),
		LinkTypes:           normalizeSetupStrings(req.LinkTypes),
		IgnoredLinkPatterns: normalizeSetupStrings(ignoredLinkPatterns),
	}
}

func emptyListenRules() model.ListenRules {
	return model.ListenRules{
		Includes:            []string{},
		Excludes:            []string{},
		MessageTypes:        []string{},
		LinkTypes:           []string{},
		IgnoredLinkPatterns: messagefilter.DefaultIgnoredLinkPatterns(),
	}
}

func (h handlers) saveSetupTelegramAPI(c *gin.Context) {
	if !h.allowSetupWrite(c) {
		return
	}
	settings, ok := readSetupTelegramAPISettingsRequest(c)
	if !ok {
		return
	}
	if settings.AppID == 0 && settings.AppHash == "" {
		c.JSON(http.StatusOK, repository.RedactTelegramAPI(model.TelegramAPISettings{}))
		return
	}
	if err := h.deps.Settings.SaveTelegramAPI(c.Request.Context(), settings); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, repository.RedactTelegramAPI(settings))
}

func (h handlers) setupComplete(c *gin.Context) {
	if !h.allowSetupWrite(c) {
		return
	}
	if err := h.deps.Settings.Set(c.Request.Context(), "setup.complete", `true`); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	status, err := h.loadSetupStatus(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h handlers) allowSetupWrite(c *gin.Context) bool {
	status, err := h.loadSetupStatus(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return false
	}
	if !status.AdminConfigured {
		errorText(c, http.StatusForbidden, "admin is required")
		return false
	}
	if h.hasAdminSession(c) {
		return true
	}
	errorText(c, http.StatusUnauthorized, "not authenticated")
	return false
}

func (h handlers) getTelegramAPISettings(c *gin.Context) {
	settings, err := h.deps.Settings.LoadTelegramAPI(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, repository.RedactTelegramAPI(settings))
}

func (h handlers) updateTelegramAPISettings(c *gin.Context) {
	settings, ok := readTelegramAPISettingsRequest(c, true)
	if !ok {
		return
	}
	if err := h.deps.Settings.SaveTelegramAPI(c.Request.Context(), settings); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, repository.RedactTelegramAPI(settings))
}

func (h handlers) getTelegramBotSettings(c *gin.Context) {
	settings, err := h.deps.Settings.LoadTelegramBot(c.Request.Context(), h.deps.RuntimeConfig.Bot)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, repository.RedactTelegramBot(settings))
}

func (h handlers) updateTelegramBotSettings(c *gin.Context) {
	existing, err := h.deps.Settings.LoadTelegramBot(c.Request.Context(), h.deps.RuntimeConfig.Bot)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	settings, ok := readTelegramBotSettingsRequest(c, existing)
	if !ok {
		return
	}
	if settings.Enabled && strings.TrimSpace(settings.Token) == "" {
		errorText(c, http.StatusBadRequest, "bot token is required when enabled is true")
		return
	}
	if err := h.deps.Settings.SaveTelegramBot(c.Request.Context(), settings); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, repository.RedactTelegramBot(settings))
}

func (h handlers) getRuntimeSettings(c *gin.Context) {
	settings, err := h.deps.Settings.LoadRuntimeSettings(c.Request.Context(), h.deps.RuntimeConfig)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, config.RedactRuntimeSettings(settings))
}

func (h handlers) updateRuntimeSettings(c *gin.Context) {
	existing, err := h.deps.Settings.LoadRuntimeSettings(c.Request.Context(), h.deps.RuntimeConfig)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	var settings config.RuntimeSettings
	if !bindJSON(c, &settings) {
		return
	}
	settings = config.PreserveRuntimeSecrets(settings, existing)
	if _, err := config.ApplyRuntimeSettings(h.deps.RuntimeConfig, settings); err != nil {
		errorText(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.deps.Settings.SaveRuntimeSettings(c.Request.Context(), settings); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if h.deps.MediaLimiter != nil {
		h.deps.MediaLimiter.Update(settings.Telegram.Media.Concurrency)
	}
	c.JSON(http.StatusOK, config.RedactRuntimeSettings(settings))
}

func (h handlers) aiModels(c *gin.Context) {
	var req struct {
		Provider string `json:"provider"`
		BaseURL  string `json:"base_url"`
		APIKey   string `json:"api_key"`
	}
	if !bindJSON(c, &req) {
		return
	}
	settings, err := h.deps.Settings.LoadRuntimeSettings(c.Request.Context(), h.deps.RuntimeConfig)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	providerID := aipkg.NormalizeProvider(req.Provider)
	if req.Provider == "" {
		providerID = aipkg.NormalizeProvider(settings.AI.MediaMetadata.Provider)
	}
	preset, hasPreset := aipkg.LookupProvider(providerID)
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		baseURL = settings.AI.MediaMetadata.BaseURL
	}
	if baseURL == "" && hasPreset {
		baseURL = preset.BaseURL
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = settings.AI.MediaMetadata.APIKey
	}
	if baseURL == "" {
		errorText(c, http.StatusBadRequest, "base_url is required")
		return
	}
	requiresAPIKey := true
	if hasPreset {
		requiresAPIKey = preset.RequiresAPIKey
	}
	if apiKey == "" && requiresAPIKey {
		errorText(c, http.StatusBadRequest, "api_key is required")
		return
	}
	client := aipkg.NewClient(aipkg.ClientOptions{
		BaseURL:            baseURL,
		APIKey:             apiKey,
		AllowMissingAPIKey: !requiresAPIKey,
	})
	models, err := client.ListModels(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": models})
}

func (h handlers) aiProviders(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"items": aipkg.ProviderPresets()})
}

func (h handlers) aiTest(c *gin.Context) {
	var req struct {
		ID           string `json:"id"`
		Provider     string `json:"provider"`
		BaseURL      string `json:"base_url"`
		BaseURLCamel string `json:"baseURL"`
		APIKey       string `json:"api_key"`
		APIKeyCamel  string `json:"apiKey"`
		Model        string `json:"model"`
	}
	if !bindJSON(c, &req) {
		return
	}
	settings, err := h.deps.Settings.LoadRuntimeSettings(c.Request.Context(), h.deps.RuntimeConfig)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	providerID := aipkg.NormalizeProvider(req.Provider)
	preset, hasPreset := aipkg.LookupProvider(providerID)
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(req.BaseURLCamel)
	}
	if baseURL == "" && hasPreset {
		baseURL = preset.BaseURL
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" && hasPreset {
		modelName = preset.DefaultModel
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(req.APIKeyCamel)
	}
	if apiKey == "" {
		apiKey = savedAIMediaProviderAPIKey(settings.AI.MediaMetadata, req.ID, req.Provider, baseURL, modelName)
	}
	if baseURL == "" {
		errorText(c, http.StatusBadRequest, "base_url is required")
		return
	}
	if modelName == "" {
		errorText(c, http.StatusBadRequest, "model is required")
		return
	}
	requiresAPIKey := true
	if hasPreset {
		requiresAPIKey = preset.RequiresAPIKey
	}
	if apiKey == "" && requiresAPIKey {
		errorText(c, http.StatusBadRequest, "api_key is required")
		return
	}
	client := aipkg.NewClient(aipkg.ClientOptions{
		BaseURL:            baseURL,
		APIKey:             apiKey,
		Model:              modelName,
		AllowMissingAPIKey: !requiresAPIKey,
	})
	started := time.Now()
	if _, err := client.Ping(c.Request.Context()); err != nil {
		errorJSON(c, http.StatusBadGateway, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":         true,
		"model":      modelName,
		"latency_ms": time.Since(started).Milliseconds(),
	})
}

func savedAIMediaProviderAPIKey(settings config.AIMediaMetadataSettings, id string, provider string, baseURL string, model string) string {
	id = strings.TrimSpace(id)
	provider = strings.TrimSpace(provider)
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	model = strings.TrimSpace(model)
	for _, item := range settings.EffectiveProviders() {
		if strings.TrimSpace(item.APIKey) == "" {
			continue
		}
		if id != "" && strings.TrimSpace(item.ID) == id {
			return strings.TrimSpace(item.APIKey)
		}
		if provider != "" && strings.TrimSpace(item.Provider) != provider {
			continue
		}
		if baseURL != "" && strings.TrimRight(strings.TrimSpace(item.BaseURL), "/") != baseURL {
			continue
		}
		if model != "" && strings.TrimSpace(item.Model) != model {
			continue
		}
		return strings.TrimSpace(item.APIKey)
	}
	return ""
}

func readTelegramAPISettingsRequest(c *gin.Context, requireHash bool) (model.TelegramAPISettings, bool) {
	var req struct {
		AppID   int    `json:"app_id"`
		AppHash string `json:"app_hash"`
	}
	if !bindJSON(c, &req) {
		return model.TelegramAPISettings{}, false
	}
	if req.AppID <= 0 {
		errorText(c, http.StatusBadRequest, "app_id must be greater than zero")
		return model.TelegramAPISettings{}, false
	}
	req.AppHash = strings.TrimSpace(req.AppHash)
	if requireHash && req.AppHash == "" {
		errorText(c, http.StatusBadRequest, "app_hash is required")
		return model.TelegramAPISettings{}, false
	}
	return model.TelegramAPISettings{AppID: req.AppID, AppHash: req.AppHash}, true
}

func readTelegramBotSettingsRequest(c *gin.Context, existing config.BotConfig) (config.BotConfig, bool) {
	var req struct {
		Enabled      bool            `json:"enabled"`
		Token        *string         `json:"token"`
		PollInterval config.Duration `json:"poll_interval"`
	}
	if !bindJSON(c, &req) {
		return config.BotConfig{}, false
	}
	if req.PollInterval <= 0 {
		errorText(c, http.StatusBadRequest, "poll_interval must be greater than zero")
		return config.BotConfig{}, false
	}
	token := existing.Token
	if req.Token != nil {
		trimmed := strings.TrimSpace(*req.Token)
		if trimmed != "" {
			token = trimmed
		}
	}
	return config.BotConfig{
		Enabled:      req.Enabled,
		Token:        token,
		PollInterval: req.PollInterval,
	}, true
}

func readSetupTelegramAPISettingsRequest(c *gin.Context) (model.TelegramAPISettings, bool) {
	var req struct {
		AppID   int    `json:"app_id"`
		AppHash string `json:"app_hash"`
	}
	if !bindJSON(c, &req) {
		return model.TelegramAPISettings{}, false
	}
	req.AppHash = strings.TrimSpace(req.AppHash)
	if req.AppID == 0 && req.AppHash == "" {
		return model.TelegramAPISettings{}, true
	}
	if req.AppID <= 0 {
		errorText(c, http.StatusBadRequest, "app_id must be greater than zero")
		return model.TelegramAPISettings{}, false
	}
	if req.AppHash == "" {
		errorText(c, http.StatusBadRequest, "app_hash is required")
		return model.TelegramAPISettings{}, false
	}
	return model.TelegramAPISettings{AppID: req.AppID, AppHash: req.AppHash}, true
}

func (h handlers) authLogin(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !bindJSON(c, &req) {
		return
	}
	user, err := h.deps.AdminAuth.Authenticate(c.Request.Context(), req.Username, req.Password)
	if err != nil {
		if errors.Is(err, adminauth.ErrInvalidCredentials) {
			errorText(c, http.StatusUnauthorized, "用户名或密码错误")
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	token, err := h.deps.AdminAuth.CreateSession(c.Request.Context(), user)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(adminSessionCookie, token, int(adminauth.DefaultSessionTTL/time.Second), "/", "", false, true)
	user.PasswordHash = ""
	c.JSON(http.StatusOK, user)
}

func (h handlers) authLogout(c *gin.Context) {
	if cookie, err := c.Cookie(adminSessionCookie); err == nil {
		if err := h.deps.AdminAuth.DeleteSession(c.Request.Context(), cookie); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(adminSessionCookie, "", -1, "/", "", false, true)
	c.JSON(http.StatusOK, gin.H{"logged_out": true})
}

func (h handlers) authMe(c *gin.Context) {
	cookie, err := c.Cookie(adminSessionCookie)
	if err != nil {
		errorText(c, http.StatusUnauthorized, "not authenticated")
		return
	}
	user, ok := h.deps.AdminAuth.UserForSession(c.Request.Context(), cookie)
	if !ok {
		errorText(c, http.StatusUnauthorized, "not authenticated")
		return
	}
	user.PasswordHash = ""
	c.JSON(http.StatusOK, user)
}

func (h handlers) requireAdminSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.hasAdminSession(c) {
			c.Next()
			return
		}
		errorText(c, http.StatusUnauthorized, "not authenticated")
		c.Abort()
	}
}

func (h handlers) requireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := apiKeyFromRequest(c.Request)
		if key == "" {
			errorText(c, http.StatusUnauthorized, "api key is required")
			c.Abort()
			return
		}
		keyID, ok, err := h.deps.APIKeyService.Verify(c.Request.Context(), key)
		if err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			c.Abort()
			return
		}
		if !ok {
			errorText(c, http.StatusUnauthorized, "invalid api key")
			c.Abort()
			return
		}
		c.Set(apiKeyIDKey, keyID)
		c.Next()
	}
}

func (h handlers) hasAdminSession(c *gin.Context) bool {
	_, _, ok := h.adminSession(c)
	return ok
}

func (h handlers) adminSession(c *gin.Context) (string, model.User, bool) {
	cookie, err := c.Cookie(adminSessionCookie)
	if err != nil {
		return "", model.User{}, false
	}
	user, ok := h.deps.AdminAuth.UserForSession(c.Request.Context(), cookie)
	return cookie, user, ok
}

func apiKeyFromRequest(r *http.Request) string {
	return apiKeyFromRequestHeader(r)
}

func apiKeyFromRequestHeader(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("X-API-Key")); key != "" {
		return key
	}
	return strings.TrimSpace(r.Header.Get("Authorization"))
}

func (h handlers) storageUsage(c *gin.Context) {
	usage, err := h.deps.StorageUsage.Usage()
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, usage)
}

func (h handlers) loadSetupStatus(ctx context.Context) (model.SetupStatus, error) {
	var status model.SetupStatus
	adminCount, err := h.deps.Users.Count(ctx)
	if err != nil {
		return model.SetupStatus{}, err
	}
	keyCount, err := h.deps.APIKeys.CountEnabled(ctx)
	if err != nil {
		return model.SetupStatus{}, err
	}
	apiKeyDoneRaw, apiKeyDone, err := h.deps.Settings.Get(ctx, setupAPIKeyDoneKey)
	if err != nil {
		return model.SetupStatus{}, err
	}
	listenRulesDoneRaw, listenRulesDone, err := h.deps.Settings.Get(ctx, setupListenRulesDoneKey)
	if err != nil {
		return model.SetupStatus{}, err
	}
	completeRaw, ok, err := h.deps.Settings.Get(ctx, "setup.complete")
	if err != nil {
		return model.SetupStatus{}, err
	}
	telegramAPI, err := h.deps.Settings.LoadTelegramAPI(ctx)
	if err != nil {
		return model.SetupStatus{}, err
	}
	status.AdminConfigured = adminCount > 0
	status.APIKeyConfigured = keyCount > 0
	status.APIKeyStepComplete = status.APIKeyConfigured || (apiKeyDone && apiKeyDoneRaw == `true`)
	status.TelegramConfigured = telegramAPI.AppID > 0 && telegramAPI.AppHash != ""
	if h.deps.Accounts != nil {
		accounts, err := h.deps.Accounts.FindAll(ctx)
		if err != nil {
			return model.SetupStatus{}, err
		}
		status.TelegramLoginComplete = len(accounts) > 0
	}
	status.ListenRulesConfigured = listenRulesDone && listenRulesDoneRaw == `true`
	status.Complete = ok && completeRaw == `true`
	status.CurrentStep = setupCurrentStep(status)
	return status, nil
}

func setupCurrentStep(status model.SetupStatus) string {
	switch {
	case status.Complete:
		return "complete"
	case !status.AdminConfigured:
		return "admin"
	case !status.APIKeyStepComplete:
		return "api_key"
	case !status.TelegramConfigured:
		return "telegram_api"
	case !status.TelegramLoginComplete:
		return "telegram_login"
	case !status.ListenRulesConfigured:
		return "listen_rules"
	default:
		return "channel_selection"
	}
}

func (h handlers) status(c *gin.Context) {
	counts, err := h.deps.Status.Counts(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"service":        "ok",
		"accounts":       counts.Accounts,
		"channels":       counts.Channels,
		"messages":       counts.Messages,
		"links":          counts.Links,
		"account_states": counts.AccountStates,
	})
}

func (h handlers) tasks(c *gin.Context) {
	if h.deps.TaskRepository == nil {
		errorText(c, http.StatusServiceUnavailable, "tasks are unavailable")
		return
	}
	limit, ok := queryNonNegativeInt(c, "limit")
	if !ok {
		return
	}
	offset, ok := queryNonNegativeInt(c, "offset")
	if !ok {
		return
	}
	filter := taskpkg.ListFilter{
		Status: c.Query("status"),
		Type:   c.Query("type"),
		Query:  c.Query("q"),
		Sort:   c.Query("sort"),
		Order:  c.Query("order"),
		Limit:  limit,
		Offset: offset,
	}
	items, err := h.deps.TaskRepository.List(c.Request.Context(), filter)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	total, err := h.deps.TaskRepository.Count(c.Request.Context(), filter)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": localizeTasks(items), "total": total})
}

func (h handlers) logs(c *gin.Context) {
	viewer, ok := h.logViewer(c)
	if !ok {
		return
	}
	limit, ok := queryNonNegativeInt(c, "limit")
	if !ok {
		return
	}
	offset, ok := queryNonNegativeInt(c, "offset")
	if !ok {
		return
	}
	result, err := viewer.List(logviewer.Query{
		File:   c.Query("file"),
		Level:  c.Query("level"),
		Text:   c.Query("q"),
		Order:  c.Query("order"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		if errors.Is(err, logviewer.ErrInvalidLogFile) {
			errorText(c, http.StatusBadRequest, "invalid log file")
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) downloadLog(c *gin.Context) {
	viewer, ok := h.logViewer(c)
	if !ok {
		return
	}
	name := c.Param("file")
	logPath, err := viewer.Path(name)
	if err != nil {
		errorText(c, http.StatusBadRequest, "invalid log file")
		return
	}
	if _, err := os.Stat(logPath); errors.Is(err, os.ErrNotExist) {
		errorText(c, http.StatusNotFound, "log file not found")
		return
	} else if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.File(logPath)
}

func (h handlers) logViewer(c *gin.Context) (*logviewer.Service, bool) {
	runtimeConfig := h.deps.RuntimeConfig
	if runtimeConfig.Storage.Path == "" && h.deps.StorageUsage != nil {
		runtimeConfig = h.deps.StorageUsage.Config()
	}
	if runtimeConfig.Storage.Path == "" {
		errorText(c, http.StatusServiceUnavailable, "log storage is unavailable")
		return nil, false
	}
	return logviewer.New(filepath.Join(runtimeConfig.Storage.Path, "logs")), true
}

func (h handlers) task(c *gin.Context) {
	if h.deps.TaskRepository == nil {
		errorText(c, http.StatusServiceUnavailable, "tasks are unavailable")
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.deps.TaskRepository.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, localizeTask(item))
}

func (h handlers) deleteTask(c *gin.Context) {
	if h.deps.Tasks == nil {
		errorText(c, http.StatusServiceUnavailable, "tasks are unavailable")
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.deps.Tasks.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, taskpkg.ErrTaskNotDeletable) {
			errorJSON(c, http.StatusConflict, err)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (h handlers) bulkDeleteTasks(c *gin.Context) {
	if h.deps.Tasks == nil {
		errorText(c, http.StatusServiceUnavailable, "tasks are unavailable")
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if len(req.IDs) == 0 {
		errorText(c, http.StatusBadRequest, "ids are required")
		return
	}
	result, err := h.deps.Tasks.DeleteMany(c.Request.Context(), req.IDs)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) retryTask(c *gin.Context) {
	h.updateTask(c, func(ctx context.Context, id int64) error {
		return h.deps.Tasks.Retry(ctx, id)
	})
}

func (h handlers) cancelTask(c *gin.Context) {
	h.updateTask(c, func(ctx context.Context, id int64) error {
		return h.deps.Tasks.Cancel(ctx, id)
	})
}

func (h handlers) pauseTask(c *gin.Context) {
	h.updateTask(c, func(ctx context.Context, id int64) error {
		return h.deps.Tasks.Pause(ctx, id)
	})
}

func (h handlers) resumeTask(c *gin.Context) {
	h.updateTask(c, func(ctx context.Context, id int64) error {
		return h.deps.Tasks.Resume(ctx, id)
	})
}

func (h handlers) updateTask(c *gin.Context, fn func(context.Context, int64) error) {
	if h.deps.Tasks == nil || h.deps.TaskRepository == nil {
		errorText(c, http.StatusServiceUnavailable, "tasks are unavailable")
		return
	}
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := fn(c.Request.Context(), id); err != nil {
		if errors.Is(err, taskpkg.ErrInvalidTransition) {
			errorJSON(c, http.StatusConflict, err)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	item, err := h.deps.TaskRepository.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	localized := localizeTask(item)
	h.publishEvent(taskpkg.Event{Type: taskpkg.EventTaskUpdated, Payload: localized})
	c.JSON(http.StatusOK, localized)
}

func (h handlers) events(c *gin.Context) {
	if h.deps.Events == nil {
		errorText(c, http.StatusServiceUnavailable, "events are unavailable")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	_, _ = c.Writer.WriteString(": connected\n\n")
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	events, unsubscribe := h.deps.Events.Subscribe(c.Request.Context())
	defer unsubscribe()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			event = localizeEvent(event)
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event.Type, data)
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
}

func localizeEvent(event taskpkg.Event) taskpkg.Event {
	if event.Type != taskpkg.EventTaskUpdated {
		return event
	}
	switch payload := event.Payload.(type) {
	case model.Task:
		event.Payload = localizeTask(payload)
	case *model.Task:
		if payload != nil {
			localized := localizeTask(*payload)
			event.Payload = &localized
		}
	}
	return event
}

func (h handlers) publishEvent(event taskpkg.Event) {
	if h.deps.Events != nil {
		h.deps.Events.Publish(event)
	}
}

func (h handlers) sendCode(c *gin.Context) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if req.Phone == "" {
		errorText(c, http.StatusBadRequest, "phone is required")
		return
	}
	phone, err := telegram.NormalizePhone(req.Phone)
	if err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}
	accountID, err := h.deps.Accounts.Save(c.Request.Context(), model.Account{
		Phone:  phone,
		Status: model.AccountStatusLoginRequired,
	})
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	sessionPath := h.sessionPath(accountID)
	sent, err := h.deps.Telegram.SendCode(c.Request.Context(), phone, sessionPath)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	h.deps.CodeStore.Save(phone, sent.PhoneCodeHash)
	c.JSON(http.StatusOK, gin.H{"status": model.AccountStatusLoginRequired, "phone": phone})
}

func (h handlers) signIn(c *gin.Context) {
	var req struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if req.Phone == "" || req.Code == "" {
		errorText(c, http.StatusBadRequest, "phone and code are required")
		return
	}
	phone, err := telegram.NormalizePhone(req.Phone)
	if err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}
	account, err := h.deps.Accounts.FindByPhone(c.Request.Context(), phone)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, sql.ErrNoRows) {
			status = http.StatusNotFound
		}
		errorJSON(c, status, err)
		return
	}
	hash, ok := h.deps.CodeStore.Take(phone)
	if !ok {
		errorText(c, http.StatusBadRequest, "login code hash is missing; call send-code first")
		return
	}
	profile, err := h.deps.Telegram.SignIn(c.Request.Context(), phone, req.Code, hash, h.sessionPath(account.ID))
	if err != nil {
		if errors.Is(err, telegram.ErrPasswordRequired) {
			c.JSON(http.StatusAccepted, gin.H{"status": model.AccountStatusLoginRequired, "password_required": true})
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	h.updateAccountProfile(c, account, profile)
}

func (h handlers) password(c *gin.Context) {
	var req struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if req.Phone == "" || req.Password == "" {
		errorText(c, http.StatusBadRequest, "phone and password are required")
		return
	}
	phone, err := telegram.NormalizePhone(req.Phone)
	if err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}
	account, err := h.deps.Accounts.FindByPhone(c.Request.Context(), phone)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	profile, err := h.deps.Telegram.Password(c.Request.Context(), req.Password, h.sessionPath(account.ID))
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	h.updateAccountProfile(c, account, profile)
}

func (h handlers) syncAccountAvatar(c *gin.Context) {
	if h.deps.AvatarService == nil {
		errorText(c, http.StatusServiceUnavailable, "avatar service is unavailable")
		return
	}
	if h.deps.Telegram == nil {
		errorText(c, http.StatusServiceUnavailable, "telegram client is unavailable")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		errorText(c, http.StatusBadRequest, "id must be a positive integer")
		return
	}
	account, err := h.deps.Accounts.FindByID(c.Request.Context(), id)
	if err != nil {
		errorText(c, http.StatusNotFound, "account not found")
		return
	}

	// Fetch latest profile to get current PhotoID
	session := h.accountSession(account)
	profile, err := h.deps.Telegram.GetUserProfile(c.Request.Context(), session)
	if err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return
	}

	// Update account PhotoID if it changed
	if profile.PhotoID != account.PhotoID {
		account.PhotoID = profile.PhotoID
		if err := h.deps.Accounts.UpdatePhotoID(c.Request.Context(), account.ID, profile.PhotoID); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}

	if account.PhotoID <= 0 {
		errorText(c, http.StatusBadRequest, "account has no avatar to sync")
		return
	}

	job := h.deps.AvatarService.EnqueueAccountAvatar(context.WithoutCancel(c.Request.Context()), account)
	c.JSON(http.StatusAccepted, gin.H{"status": "queued", "job_id": job.ID})
}

func (h handlers) telethonSessionLogin(c *gin.Context) {
	var req struct {
		SessionString string `json:"session_string"`
	}
	if !bindJSON(c, &req) {
		return
	}
	req.SessionString = strings.TrimSpace(req.SessionString)
	if req.SessionString == "" {
		errorText(c, http.StatusBadRequest, "session_string is required")
		return
	}

	sessionPath := ""
	if h.deps.Sessions != nil {
		sessionPath = h.deps.Sessions.PathForTemporary("telethon-" + fmt.Sprintf("%d", time.Now().UnixNano()))
		if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}

	profile, err := h.deps.Telegram.LoginWithTelethonSession(c.Request.Context(), req.SessionString, sessionPath)
	if err != nil {
		if h.deps.Sessions != nil && sessionPath != "" {
			_ = h.deps.Sessions.RemovePath(sessionPath)
		}
		errorJSON(c, http.StatusBadRequest, err)
		return
	}

	phone := profile.Phone
	if phone == "" {
		phone = fmt.Sprintf("tg:%d", profile.TelegramUserID)
	}

	accountID, err := h.deps.Accounts.Save(c.Request.Context(), model.Account{
		Phone:          phone,
		TelegramUserID: profile.TelegramUserID,
		Status:         model.AccountStatusLoginRequired,
	})
	if err != nil {
		if h.deps.Sessions != nil && sessionPath != "" {
			_ = h.deps.Sessions.RemovePath(sessionPath)
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}

	if h.deps.Sessions != nil && sessionPath != "" {
		finalPath, err := h.deps.Sessions.MoveTemporaryToAccount(sessionPath, accountID)
		if err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
		sessionPath = finalPath
	}

	account := model.Account{
		ID:             accountID,
		Phone:          phone,
		TelegramUserID: profile.TelegramUserID,
		FirstName:      profile.FirstName,
		LastName:       profile.LastName,
		Username:       profile.Username,
		Status:         model.AccountStatusOnline,
		SessionPath:    sessionPath,
	}
	now := time.Now().UTC()
	account.LastOnlineAt = &now

	if err := h.deps.Accounts.Update(c.Request.Context(), account); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}

	h.respondWithOnlineAccount(c, account)
}

func (h handlers) startQRLogin(c *gin.Context) {
	sessionPath := ""
	if h.deps.Sessions != nil {
		sessionPath = h.deps.Sessions.PathForTemporary("qr-login-" + newQRLoginID())
		if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	session, err := h.deps.Telegram.StartQRLogin(c.Request.Context(), sessionPath)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	item := h.deps.QRLogins.Add(sessionPath, session)
	token := session.Token()
	c.JSON(http.StatusOK, gin.H{
		"login_id":   item.LoginID,
		"status":     telegram.QRLoginStatusPending,
		"qr_url":     token.URL,
		"expires_at": token.ExpiresAt,
	})
}

func (h handlers) pollQRLogin(c *gin.Context) {
	loginID := c.Param("login_id")
	item, ok := h.deps.QRLogins.Find(loginID)
	if !ok {
		errorText(c, http.StatusNotFound, "qr login session not found")
		return
	}
	result, err := item.Session.Poll(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if result.Status == telegram.QRLoginStatusOnline {
		account, err := h.saveQRProfile(c.Request.Context(), result.Profile, item.SessionPath)
		if err != nil {
			if h.deps.Sessions != nil && item.SessionPath != "" {
				_ = h.deps.Sessions.RemovePath(item.SessionPath)
			}
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
		h.deps.QRLogins.Remove(loginID)
		h.respondWithOnlineAccount(c, account)
		return
	}
	token := result.Token
	if token.URL == "" {
		token = item.Session.Token()
	}
	c.JSON(http.StatusOK, gin.H{
		"login_id":   item.LoginID,
		"status":     telegram.QRLoginStatusPending,
		"qr_url":     token.URL,
		"expires_at": token.ExpiresAt,
	})
}

func (h handlers) cancelQRLogin(c *gin.Context) {
	if err := h.deps.QRLogins.Cancel(c.Request.Context(), c.Param("login_id")); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"canceled": true})
}

func (h handlers) accounts(c *gin.Context) {
	items, err := h.deps.Accounts.FindAll(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": localizeAccounts(items)})
}

func (h handlers) logoutAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	account, err := h.deps.Accounts.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	if h.deps.AccountRuntime != nil {
		if err := h.deps.AccountRuntime.StopAccount(c.Request.Context(), id); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	if err := h.logoutTelegramAccount(c.Request.Context(), account); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if h.deps.Sessions != nil {
		if err := h.deps.Sessions.RemoveForAccount(id); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	if err := h.deps.Accounts.UpdateStatus(c.Request.Context(), id, model.AccountStatusLoginRequired); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	account, err = h.deps.Accounts.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, localizeAccount(account))
}

func (h handlers) deleteAccount(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	account, err := h.deps.Accounts.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	if h.deps.AccountRuntime != nil {
		if err := h.deps.AccountRuntime.StopAccount(c.Request.Context(), id); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	if err := h.logoutTelegramAccount(c.Request.Context(), account); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if h.deps.Sessions != nil {
		if err := h.deps.Sessions.RemoveForAccount(id); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	if err := h.deps.Accounts.Delete(c.Request.Context(), id); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (h handlers) logoutTelegramAccount(ctx context.Context, account model.Account) error {
	if h.deps.Telegram == nil {
		return nil
	}
	sessionPath := account.SessionPath
	if sessionPath == "" {
		sessionPath = h.sessionPath(account.ID)
	}
	if sessionPath == "" {
		return nil
	}
	if _, err := os.Stat(sessionPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat telegram session: %w", err)
	}
	err := h.deps.Telegram.Logout(ctx, telegram.AccountSession{
		AccountID:   account.ID,
		Phone:       account.Phone,
		SessionPath: sessionPath,
	})
	if errors.Is(err, telegram.ErrUnavailable) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("logout telegram account: %w", err)
	}
	return nil
}

func (h handlers) channels(c *gin.Context) {
	accountID, ok := queryPositiveInt64(c, "account_id")
	if !ok {
		return
	}
	var (
		items []model.Channel
		err   error
	)
	if accountID > 0 {
		items, err = h.deps.Channels.FindByAccountID(c.Request.Context(), accountID)
	} else {
		items, err = h.deps.Channels.FindAll(c.Request.Context())
	}
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	items = hideUnavailableChannels(items)
	if accountID == 0 {
		items = deduplicateGlobalChannels(items)
	}
	c.JSON(http.StatusOK, gin.H{"items": localizeChannels(items)})
}

func hideUnavailableChannels(channels []model.Channel) []model.Channel {
	out := make([]model.Channel, 0, len(channels))
	for _, channel := range channels {
		if shouldHideUnavailableChannel(channel) {
			continue
		}
		out = append(out, channel)
	}
	return out
}

func shouldHideUnavailableChannel(channel model.Channel) bool {
	if channel.Type == model.ChannelTypeSavedMessages {
		return false
	}
	return channel.MemberCount == 0
}

type channelDedupKey struct {
	telegramChannelID int64
	channelType       string
	accountID         int64
	rowID             int64
}

func deduplicateGlobalChannels(channels []model.Channel) []model.Channel {
	out := make([]model.Channel, 0, len(channels))
	positions := make(map[channelDedupKey]int, len(channels))
	for _, channel := range channels {
		key := globalChannelDedupKey(channel)
		position, ok := positions[key]
		if !ok {
			positions[key] = len(out)
			out = append(out, channel)
			continue
		}
		if preferGlobalChannelRepresentative(channel, out[position]) {
			out[position] = channel
		}
	}
	return out
}

func globalChannelDedupKey(channel model.Channel) channelDedupKey {
	key := channelDedupKey{
		telegramChannelID: channel.TelegramChannelID,
		channelType:       channel.Type,
	}
	if channel.Type == model.ChannelTypeSavedMessages || channel.TelegramChannelID <= 0 {
		key.accountID = channel.AccountID
		key.rowID = channel.ID
	}
	return key
}

func preferGlobalChannelRepresentative(candidate model.Channel, current model.Channel) bool {
	candidateScore := globalChannelRepresentativeScore(candidate)
	currentScore := globalChannelRepresentativeScore(current)
	if candidateScore != currentScore {
		return candidateScore > currentScore
	}
	if candidate.IndexedMessageCount != current.IndexedMessageCount {
		return candidate.IndexedMessageCount > current.IndexedMessageCount
	}
	if candidate.LastSyncTime != nil && current.LastSyncTime != nil && !candidate.LastSyncTime.Equal(*current.LastSyncTime) {
		return candidate.LastSyncTime.After(*current.LastSyncTime)
	}
	if candidate.LastSyncTime != nil && current.LastSyncTime == nil {
		return true
	}
	if candidate.LastSyncTime == nil && current.LastSyncTime != nil {
		return false
	}
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	return candidate.ID < current.ID
}

func globalChannelRepresentativeScore(channel model.Channel) int {
	score := 0
	if channel.HistorySyncEnabled {
		score += 4
	}
	if channel.ListenEnabled {
		score += 4
	}
	if channel.SyncState == "synced" || channel.SyncState == "syncing" || channel.SyncState == "pending" {
		score += 2
	}
	if channel.ListenState == "enabled" {
		score += 2
	}
	if channel.RemoteSearchAllowed {
		score += 1
	}
	return score
}

func (h handlers) channel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.deps.Channels.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, localizeChannel(item))
}

func (h handlers) updateChannelControl(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req model.ChannelControl
	if !bindJSON(c, &req) {
		return
	}
	control, ok := h.validateChannelControl(c, req)
	if !ok {
		return
	}
	before, err := h.deps.Channels.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	if err := h.deps.Channels.UpdateControl(c.Request.Context(), id, control); err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	item, err := h.deps.Channels.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	if h.shouldEnqueueListenHistorySync(before, item) {
		if _, err := h.enqueueHistorySyncTaskPayload(c.Request.Context(), taskpkg.HistorySyncPayload{
			ChannelID:   item.ID,
			MaxMessages: listenHistorySyncMaxMessages,
		}); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, localizeChannel(item))
}

func (h handlers) updateChannelsControl(c *gin.Context) {
	var req struct {
		ChannelIDs []int64              `json:"channel_ids"`
		Control    model.ChannelControl `json:"control"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if len(req.ChannelIDs) == 0 {
		errorText(c, http.StatusBadRequest, "channel_ids is required")
		return
	}
	for _, id := range req.ChannelIDs {
		if id <= 0 {
			errorText(c, http.StatusBadRequest, "channel_ids must contain positive integers")
			return
		}
	}
	control, ok := h.validateChannelControl(c, req.Control)
	if !ok {
		return
	}
	beforeByID := map[int64]model.Channel{}
	seenBefore := map[int64]struct{}{}
	for _, id := range req.ChannelIDs {
		if _, ok := seenBefore[id]; ok {
			continue
		}
		seenBefore[id] = struct{}{}
		item, err := h.deps.Channels.FindByID(c.Request.Context(), id)
		if err != nil {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		beforeByID[id] = item
	}
	if err := h.deps.Channels.UpdateControls(c.Request.Context(), req.ChannelIDs, control); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	items := make([]model.Channel, 0, len(req.ChannelIDs))
	seen := map[int64]struct{}{}
	for _, id := range req.ChannelIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		item, err := h.deps.Channels.FindByID(c.Request.Context(), id)
		if err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
		items = append(items, item)
	}
	listenSyncChannelIDs := make([]int64, 0, len(items))
	for _, item := range items {
		if h.shouldEnqueueListenHistorySync(beforeByID[item.ID], item) {
			listenSyncChannelIDs = append(listenSyncChannelIDs, item.ID)
		}
	}
	if len(listenSyncChannelIDs) > 0 {
		if _, err := h.enqueueHistorySyncTaskPayload(c.Request.Context(), taskpkg.HistorySyncPayload{
			ChannelIDs:  listenSyncChannelIDs,
			MaxMessages: listenHistorySyncMaxMessages,
		}); err != nil {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": localizeChannels(items)})
}

func (h handlers) clearChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	result, err := h.deps.Channels.ClearIndexedData(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	item, err := h.deps.Channels.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"channel": localizeChannel(item),
		"deleted": result,
	})
}

func (h handlers) shouldEnqueueListenHistorySync(before model.Channel, after model.Channel) bool {
	if h.deps.Tasks == nil {
		return false
	}
	return !channelListenEnabled(before) && channelListenEnabled(after)
}

func channelListenEnabled(channel model.Channel) bool {
	return channel.ListenEnabled || channel.ListenState == "enabled"
}

func (h handlers) validateChannelControl(c *gin.Context, control model.ChannelControl) (model.ChannelControl, bool) {
	profile, err := channelpkg.ParseProfile(control.SyncProfile)
	if err != nil {
		errorText(c, http.StatusBadRequest, err.Error())
		return model.ChannelControl{}, false
	}
	control.SyncProfile = profile
	if profile == channelpkg.SyncProfileDeep || profile == channelpkg.SyncProfileFull {
		if !h.checkStorageQuota(c) {
			return model.ChannelControl{}, false
		}
	}
	return control, true
}

func (h handlers) analyzeChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.deps.Channels.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	var watchRule *model.WatchRule
	if h.deps.WatchRules != nil {
		rule, err := h.deps.WatchRules.FindByChannelID(c.Request.Context(), id)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			errorJSON(c, http.StatusInternalServerError, err)
			return
		}
		if err == nil {
			watchRule = &rule
		}
	}
	c.JSON(http.StatusOK, model.ChannelAnalysis{
		Channel: localizeChannel(item),
		Control: model.ChannelControl{
			HistorySyncEnabled:  item.HistorySyncEnabled,
			SyncProfile:         item.SyncProfile,
			ListenEnabled:       item.ListenEnabled,
			RemoteSearchAllowed: item.RemoteSearchAllowed,
		},
		WatchRule:     watchRule,
		IndexedCounts: model.ChannelIndexedCounts{},
	})
}

func (h handlers) createRemoteSearchTask(c *gin.Context) {
	var req struct {
		ChannelID int64  `json:"channel_id"`
		Query     string `json:"query"`
	}
	if !bindJSON(c, &req) {
		return
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		errorText(c, http.StatusBadRequest, "query is required")
		return
	}
	if req.ChannelID <= 0 {
		errorText(c, http.StatusBadRequest, "channel_id must be a positive integer")
		return
	}
	if h.deps.RemoteSearch == nil {
		errorText(c, http.StatusServiceUnavailable, "remote search is unavailable")
		return
	}
	if h.deps.RemoteSearchExec != nil {
		task, err := h.deps.RemoteSearchExec.Search(c.Request.Context(), req.ChannelID, query, 50)
		if err != nil {
			h.remoteSearchError(c, err)
			return
		}
		c.JSON(http.StatusAccepted, task)
		return
	}
	item, err := h.deps.Channels.FindByID(c.Request.Context(), req.ChannelID)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	if !item.RemoteSearchAllowed {
		errorWithCode(c, http.StatusConflict, "remote_search_not_allowed", "remote search is not allowed for this channel")
		return
	}
	if item.LastMessageID > 0 || item.LastSyncTime != nil {
		errorWithCode(c, http.StatusConflict, "remote_search_requires_unsynced_channel", "remote search requires an unsynced channel")
		return
	}
	task := model.RemoteSearchTask{
		AccountID: item.AccountID,
		ChannelID: item.ID,
		Query:     query,
		Status:    model.RemoteSearchStatusQueued,
		Source:    "remote",
		ExpiresAt: time.Now().UTC().Add(30 * time.Minute),
	}
	id, err := h.deps.RemoteSearch.Create(c.Request.Context(), task)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	task, err = h.deps.RemoteSearch.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusAccepted, task)
}

func (h handlers) getRemoteSearchTask(c *gin.Context) {
	id, ok := pathPositiveInt64(c, "task_id")
	if !ok {
		return
	}
	if h.deps.RemoteSearchExec == nil {
		errorText(c, http.StatusServiceUnavailable, "remote search execution is unavailable")
		return
	}
	result, err := h.deps.RemoteSearchExec.Results(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	result, err = h.attachMediaToRemoteSearchResults(c.Request.Context(), result, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) remoteSearchError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, searchsvc.ErrEmptyQuery):
		errorText(c, http.StatusBadRequest, "query is required")
	case errors.Is(err, searchsvc.ErrRemoteSearchNotAllowed):
		errorWithCode(c, http.StatusConflict, "remote_search_not_allowed", "remote search is not allowed for this channel")
	case errors.Is(err, searchsvc.ErrRemoteSearchRequiresUnsynced):
		errorWithCode(c, http.StatusConflict, "remote_search_requires_unsynced_channel", "remote search requires an unsynced channel")
	default:
		errorJSON(c, http.StatusInternalServerError, err)
	}
}

func (h handlers) checkStorageQuota(c *gin.Context) bool {
	if h.deps.StorageUsage == nil {
		return true
	}
	usage, err := h.deps.StorageUsage.Usage()
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return false
	}
	if usage.MaxDBBytes > 0 && usage.DBBytes >= usage.MaxDBBytes {
		errorWithCode(c, http.StatusConflict, "storage_quota_exceeded", "database storage quota exceeded")
		return false
	}
	return true
}

func (h handlers) syncChannel(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if h.deps.Tasks != nil {
		h.enqueueHistorySyncTask(c, taskpkg.HistorySyncPayload{ChannelID: id})
		return
	}
	if h.deps.SyncQueue != nil {
		jobCtx := context.WithoutCancel(c.Request.Context())
		job := h.deps.SyncQueue.Enqueue(jobCtx, "channel-sync", func(ctx context.Context) error {
			_, err := h.deps.History.SyncChannel(ctx, id)
			return err
		})
		c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "status": job.Status})
		return
	}
	result, err := h.deps.History.SyncChannel(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusAccepted, result)
}

func (h handlers) syncChannels(c *gin.Context) {
	var req struct {
		ChannelIDs  []int64 `json:"channel_ids"`
		MaxMessages *int    `json:"max_messages"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if len(req.ChannelIDs) == 0 {
		errorText(c, http.StatusBadRequest, "channel_ids is required")
		return
	}
	for _, id := range req.ChannelIDs {
		if id <= 0 {
			errorText(c, http.StatusBadRequest, "channel_ids must contain positive integers")
			return
		}
	}
	maxMessages := 0
	if req.MaxMessages != nil {
		if *req.MaxMessages <= 0 {
			errorText(c, http.StatusBadRequest, "max_messages must be a positive integer")
			return
		}
		maxMessages = *req.MaxMessages
	}
	if h.deps.Tasks != nil {
		h.enqueueHistorySyncTask(c, taskpkg.HistorySyncPayload{
			ChannelIDs:  append([]int64(nil), req.ChannelIDs...),
			MaxMessages: maxMessages,
		})
		return
	}
	if h.deps.SyncQueue != nil {
		channelIDs := append([]int64(nil), req.ChannelIDs...)
		limit := maxMessages
		jobCtx := context.WithoutCancel(c.Request.Context())
		job := h.deps.SyncQueue.Enqueue(jobCtx, "channels-sync", func(ctx context.Context) error {
			result := h.deps.History.SyncManyWithMaxMessages(ctx, channelIDs, limit)
			if len(result.Failures) > 0 {
				return fmt.Errorf("sync failures: %v", result.Failures)
			}
			return nil
		})
		c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "status": job.Status})
		return
	}
	result := h.deps.History.SyncManyWithMaxMessages(c.Request.Context(), req.ChannelIDs, maxMessages)
	c.JSON(http.StatusAccepted, localizeSyncManyResult(result))
}

func (h handlers) enqueueHistorySyncTask(c *gin.Context, payload taskpkg.HistorySyncPayload) {
	task, err := h.enqueueHistorySyncTaskPayload(c.Request.Context(), payload)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	localized := localizeTask(task)
	c.JSON(http.StatusAccepted, gin.H{
		"task_id": task.ID,
		"job_id":  strconv.FormatInt(task.ID, 10),
		"status":  task.Status,
		"task":    localized,
	})
}

func (h handlers) enqueueHistorySyncTaskPayload(ctx context.Context, payload taskpkg.HistorySyncPayload) (model.Task, error) {
	task, err := h.deps.Tasks.Enqueue(ctx, model.TaskTypeHistorySync, payload)
	if err != nil {
		return model.Task{}, err
	}
	h.publishEvent(taskpkg.Event{Type: taskpkg.EventTaskUpdated, Payload: localizeTask(task)})
	return task, nil
}

func (h handlers) checkChannelWebAccess(c *gin.Context) {
	var req struct {
		ChannelIDs []int64 `json:"channel_ids"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if len(req.ChannelIDs) == 0 {
		errorText(c, http.StatusBadRequest, "channel_ids is required")
		return
	}
	for _, id := range req.ChannelIDs {
		if id <= 0 {
			errorText(c, http.StatusBadRequest, "channel_ids must contain positive integers")
			return
		}
	}
	if h.deps.ChannelWebAccess == nil {
		errorText(c, http.StatusServiceUnavailable, "channel web access checker is unavailable")
		return
	}
	items, err := h.deps.ChannelWebAccess.CheckMany(c.Request.Context(), req.ChannelIDs)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": localizeWebAccessResults(items)})
}

func (h handlers) syncAccountChannels(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	account, err := h.deps.Accounts.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	if h.deps.SyncQueue != nil {
		jobCtx := context.WithoutCancel(c.Request.Context())
		job := h.deps.SyncQueue.Enqueue(jobCtx, "account-channels-sync", func(ctx context.Context) error {
			_, err := h.deps.ChannelSync.SyncAccount(ctx, account)
			return err
		})
		c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "status": job.Status})
		return
	}
	items, err := h.deps.ChannelSync.SyncAccount(c.Request.Context(), account)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"items": items})
}

type watchRulePayload struct {
	ChannelID    int64           `json:"channel_id"`
	Enabled      *bool           `json:"enabled"`
	Includes     json.RawMessage `json:"includes"`
	Excludes     json.RawMessage `json:"excludes"`
	MessageTypes json.RawMessage `json:"message_types"`
	LinkTypes    json.RawMessage `json:"link_types"`
}

func (h handlers) watchRules(c *gin.Context) {
	items, err := h.deps.WatchRules.FindAll(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h handlers) watchRule(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	item, err := h.deps.WatchRules.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusNotFound, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h handlers) createWatchRule(c *gin.Context) {
	rule, ok := h.readWatchRuleRequest(c, true)
	if !ok {
		return
	}
	id, err := h.deps.WatchRules.Create(c.Request.Context(), rule)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicateWatchRule) {
			errorText(c, http.StatusConflict, "watch rule already exists for channel")
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	item, err := h.deps.WatchRules.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h handlers) updateWatchRule(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	rule, ok := h.readWatchRuleRequest(c, false)
	if !ok {
		return
	}
	rule.ID = id
	if err := h.deps.WatchRules.Update(c.Request.Context(), rule); err != nil {
		if errors.Is(err, repository.ErrDuplicateWatchRule) {
			errorText(c, http.StatusConflict, "watch rule already exists for channel")
			return
		}
		if errors.Is(err, repository.ErrNotFound) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	item, err := h.deps.WatchRules.FindByID(c.Request.Context(), id)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, item)
}

func (h handlers) deleteWatchRule(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.deps.WatchRules.Delete(c.Request.Context(), id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (h handlers) readWatchRuleRequest(c *gin.Context, create bool) (model.WatchRule, bool) {
	var payload watchRulePayload
	if !bindJSON(c, &payload) {
		return model.WatchRule{}, false
	}
	if payload.ChannelID <= 0 {
		errorText(c, http.StatusBadRequest, "channel_id must be a positive integer")
		return model.WatchRule{}, false
	}
	if _, err := h.deps.Channels.FindByID(c.Request.Context(), payload.ChannelID); err != nil {
		errorText(c, http.StatusBadRequest, "channel_id must reference an existing channel")
		return model.WatchRule{}, false
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	} else if !create {
		errorText(c, http.StatusBadRequest, "enabled is required")
		return model.WatchRule{}, false
	}
	includes, ok := decodeStringArray(c, payload.Includes, "includes")
	if !ok {
		return model.WatchRule{}, false
	}
	excludes, ok := decodeStringArray(c, payload.Excludes, "excludes")
	if !ok {
		return model.WatchRule{}, false
	}
	messageTypes, ok := decodeStringArray(c, payload.MessageTypes, "message_types")
	if !ok {
		return model.WatchRule{}, false
	}
	linkTypes, ok := decodeStringArray(c, payload.LinkTypes, "link_types")
	if !ok {
		return model.WatchRule{}, false
	}
	return model.WatchRule{
		ChannelID:    payload.ChannelID,
		Enabled:      enabled,
		Includes:     includes,
		Excludes:     excludes,
		MessageTypes: messageTypes,
		LinkTypes:    linkTypes,
	}, true
}

func decodeStringArray(c *gin.Context, raw json.RawMessage, field string) ([]string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, true
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		errorText(c, http.StatusBadRequest, field+" must be an array of strings")
		return nil, false
	}
	return out, true
}

func normalizeSetupStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (h handlers) search(c *gin.Context) {
	accountID, channelID, limit, offset, ok := readFilters(c)
	if !ok {
		return
	}
	dateFrom, dateTo, ok := parseDateRange(c)
	if !ok {
		return
	}
	beforeDate, beforeID, ok := parseCursor(c)
	if !ok {
		return
	}
	items, err := h.deps.Search.Search(c.Request.Context(), searchsvc.Params{
		Query:      c.Query("q"),
		AccountID:  accountID,
		ChannelID:  channelID,
		LinkType:   c.Query("link_type"),
		Sort:       c.Query("sort"),
		DateFrom:   dateFrom,
		DateTo:     dateTo,
		BeforeDate: beforeDate,
		BeforeID:   beforeID,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, searchsvc.ErrEmptyQuery) {
			status = http.StatusBadRequest
		}
		errorJSON(c, status, err)
		return
	}
	items, err = h.attachMediaToSearchResults(c.Request.Context(), items, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h handlers) searchGlobal(c *gin.Context) {
	query, ok := h.readSearchQuery(c)
	if !ok {
		return
	}
	result, err := h.deps.Search.Global(c.Request.Context(), query)
	if err != nil {
		h.searchError(c, err)
		return
	}
	result.Messages.Items, err = h.attachMediaToSearchResults(c.Request.Context(), result.Messages.Items, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	result.Links.Items, err = h.attachMediaToLinkResults(c.Request.Context(), result.Links.Items, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	result.Files.Items, err = h.attachMediaToFileResults(c.Request.Context(), result.Files.Items, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) searchMessages(c *gin.Context) {
	query, ok := h.readSearchQuery(c)
	if !ok {
		return
	}
	result, err := h.deps.Search.Messages(c.Request.Context(), query)
	if err != nil {
		h.searchError(c, err)
		return
	}
	result.Items, err = h.attachMediaToSearchResults(c.Request.Context(), result.Items, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) searchLinks(c *gin.Context) {
	query, ok := h.readSearchQuery(c)
	if !ok {
		return
	}
	result, err := h.deps.Search.ScopedLinks(c.Request.Context(), query)
	if err != nil {
		h.searchError(c, err)
		return
	}
	result.Items, err = h.attachMediaToLinkResults(c.Request.Context(), result.Items, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) searchFiles(c *gin.Context) {
	query, ok := h.readSearchQuery(c)
	if !ok {
		return
	}
	result, err := h.deps.Search.Files(c.Request.Context(), query)
	if err != nil {
		h.searchError(c, err)
		return
	}
	result.Items, err = h.attachMediaToFileResults(c.Request.Context(), result.Items, false)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) searchChannels(c *gin.Context) {
	query, ok := h.readSearchQuery(c)
	if !ok {
		return
	}
	result, err := h.deps.Search.Channels(c.Request.Context(), query)
	if err != nil {
		h.searchError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) readSearchQuery(c *gin.Context) (searchsvc.SearchQuery, bool) {
	accountID, channelID, limit, offset, ok := readFilters(c)
	if !ok {
		return searchsvc.SearchQuery{}, false
	}
	dateFrom, dateTo, ok := parseDateRange(c)
	if !ok {
		return searchsvc.SearchQuery{}, false
	}
	return searchsvc.SearchQuery{
		Query:       c.Query("q"),
		AccountID:   accountID,
		ChannelID:   channelID,
		MessageType: c.Query("message_type"),
		LinkType:    firstQuery(c, "link_type", "type"),
		FileType:    firstQuery(c, "file_type", "category"),
		Sort:        c.Query("sort"),
		DateFrom:    dateFrom,
		DateTo:      dateTo,
		Limit:       limit,
		Offset:      offset,
	}, true
}

func (h handlers) searchError(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, searchsvc.ErrEmptyQuery) {
		status = http.StatusBadRequest
	}
	errorJSON(c, status, err)
}

func firstQuery(c *gin.Context, keys ...string) string {
	for _, key := range keys {
		if value := c.Query(key); value != "" {
			return value
		}
	}
	return ""
}

func (h handlers) latest(c *gin.Context) {
	accountID, channelID, limit, _, ok := readFilters(c)
	if !ok {
		return
	}
	beforeDate, beforeID, ok := parseCursor(c)
	if !ok {
		return
	}
	items, err := h.deps.Search.Latest(c.Request.Context(), searchsvc.LatestParams{
		AccountID:  accountID,
		ChannelID:  channelID,
		BeforeDate: beforeDate,
		BeforeID:   beforeID,
		Limit:      limit,
	})
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": latestMessageItems(items)})
}

type latestMessageItem struct {
	ID                int64        `json:"id"`
	ChannelID         int64        `json:"channel_id"`
	TelegramChannelID int64        `json:"telegram_channel_id"`
	TelegramMessageID int64        `json:"telegram_message_id"`
	SenderID          int64        `json:"sender_id"`
	Text              string       `json:"text"`
	RawJSON           string       `json:"raw_json"`
	Date              time.Time    `json:"date"`
	EditDate          *time.Time   `json:"edit_date,omitempty"`
	Deleted           bool         `json:"deleted"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
	ChannelTitle      string       `json:"channel_title"`
	ChannelUsername   string       `json:"channel_username"`
	Links             []model.Link `json:"links"`
}

func latestMessageItems(items []model.SearchResult) []latestMessageItem {
	out := make([]latestMessageItem, len(items))
	for i, item := range items {
		links := item.Links
		if links == nil {
			links = []model.Link{}
		}
		out[i] = latestMessageItem{
			ID:                item.ID,
			ChannelID:         item.ChannelID,
			TelegramChannelID: item.TelegramChannelID,
			TelegramMessageID: item.TelegramMessageID,
			SenderID:          item.SenderID,
			Text:              item.Text,
			RawJSON:           item.RawJSON,
			Date:              item.Date,
			EditDate:          item.EditDate,
			Deleted:           item.Deleted,
			CreatedAt:         item.CreatedAt,
			UpdatedAt:         item.UpdatedAt,
			ChannelTitle:      item.ChannelTitle,
			ChannelUsername:   item.ChannelUsername,
			Links:             links,
		}
	}
	return out
}

func (h handlers) links(c *gin.Context) {
	accountID, channelID, limit, offset, ok := readFilters(c)
	if !ok {
		return
	}
	dateFrom, dateTo, ok := parseDateRange(c)
	if !ok {
		return
	}
	items, err := h.deps.Search.Links(c.Request.Context(), searchsvc.LinkParams{
		Type:      c.Query("type"),
		AccountID: accountID,
		ChannelID: channelID,
		Keyword:   c.Query("keyword"),
		Sort:      c.Query("sort"),
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h handlers) linksGrouped(c *gin.Context) {
	if h.deps.Links == nil {
		errorText(c, http.StatusServiceUnavailable, "links are unavailable")
		return
	}
	grouped, err := h.deps.Links.CountByType(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"grouped": grouped})
}

func (h handlers) mergedLinks(c *gin.Context) {
	accountID, channelID, limit, offset, ok := readFilters(c)
	if !ok {
		return
	}
	dateFrom, dateTo, ok := parseDateRange(c)
	if !ok {
		return
	}
	keyword := c.Query("q")
	if keyword == "" {
		keyword = c.Query("keyword")
	}
	result, err := h.deps.Search.MergedLinks(c.Request.Context(), searchsvc.LinkParams{
		Type:      c.Query("type"),
		AccountID: accountID,
		ChannelID: channelID,
		Keyword:   keyword,
		DateFrom:  dateFrom,
		DateTo:    dateTo,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h handlers) resources(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	query, ok := readResourceQuery(c)
	if !ok {
		return
	}
	result, err := h.deps.Resources.List(c.Request.Context(), query)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	result.Items, err = h.attachMediaToResourceItems(c.Request.Context(), result.Items, h.shouldSignMediaURLs(c))
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, struct {
		Items []resource.Item `json:"items"`
		Total int             `json:"total"`
	}{
		Items: result.Items,
		Total: result.Total,
	})
}

func (h handlers) trendingResources(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	query, ok := readResourceQuery(c)
	if !ok {
		return
	}
	dateFrom, dateTo, ok := trendingDateRange(c.Query("range"), time.Now().UTC())
	if !ok {
		errorText(c, http.StatusBadRequest, "range must be today, week, or month")
		return
	}
	query.Sort = "hot"
	query.DateFrom = dateFrom
	query.DateTo = dateTo
	result, err := h.deps.Resources.List(c.Request.Context(), query)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	result.Items, err = h.attachMediaToResourceItems(c.Request.Context(), result.Items, h.shouldSignMediaURLs(c))
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func trendingDateRange(value string, now time.Time) (*time.Time, *time.Time, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	now = now.UTC()
	dateTo := now
	var dateFrom time.Time
	switch value {
	case "", "today":
		dateFrom = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "week":
		dateFrom = now.AddDate(0, 0, -7)
	case "month":
		dateFrom = now.AddDate(0, 0, -30)
	default:
		return nil, nil, false
	}
	return &dateFrom, &dateTo, true
}

func (h handlers) resourcesGrouped(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	grouped, err := h.deps.Resources.GlobalGrouped(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"grouped": grouped})
}

func (h handlers) dashboardResourceStats(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	grouped, err := h.deps.Resources.ResourceTypeStats(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"grouped": grouped})
}

func (h handlers) resource(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	id := c.Param("id")
	result, err := h.deps.Resources.List(c.Request.Context(), resource.Query{Limit: 200})
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	for _, item := range result.Items {
		if item.ID == id {
			items, err := h.attachMediaToResourceItems(c.Request.Context(), []resource.Item{item}, h.shouldSignMediaURLs(c))
			if err != nil {
				errorJSON(c, http.StatusInternalServerError, err)
				return
			}
			c.JSON(http.StatusOK, items[0])
			return
		}
	}
	errorText(c, http.StatusNotFound, "resource not found")
}

func (h handlers) deleteResource(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	if err := h.deps.Resources.Delete(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, resource.ErrInvalidResourceID) {
			errorJSON(c, http.StatusBadRequest, err)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			errorJSON(c, http.StatusNotFound, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (h handlers) bulkDeleteResources(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if !bindJSON(c, &req) {
		return
	}
	if len(req.IDs) == 0 {
		errorText(c, http.StatusBadRequest, "ids are required")
		return
	}
	result, err := h.deps.Resources.DeleteMany(c.Request.Context(), req.IDs)
	if err != nil {
		if errors.Is(err, resource.ErrInvalidResourceID) {
			errorJSON(c, http.StatusBadRequest, err)
			return
		}
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func readResourceQuery(c *gin.Context) (resource.Query, bool) {
	accountID, channelID, limit, offset, ok := readFilters(c)
	if !ok {
		return resource.Query{}, false
	}
	keyword := c.Query("q")
	if keyword == "" {
		keyword = c.Query("keyword")
	}
	return resource.Query{
		Keyword:   keyword,
		Type:      c.Query("type"),
		Category:  c.Query("category"),
		AccountID: accountID,
		ChannelID: channelID,
		Extension: c.Query("extension"),
		Sort:      c.Query("sort"),
		Limit:     limit,
		Offset:    offset,
	}, true
}

func (h handlers) maintenanceSQLite(c *gin.Context) {
	if h.deps.Maintenance == nil {
		errorText(c, http.StatusServiceUnavailable, "maintenance repository is unavailable")
		return
	}
	ops, err := h.deps.Maintenance.OptimizeSQLite(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"operations": ops})
}

func (h handlers) maintenanceBackup(c *gin.Context) {
	if h.deps.BackupDB == nil || h.deps.BackupDir == "" {
		errorText(c, http.StatusServiceUnavailable, "backup is unavailable")
		return
	}
	path, err := backup.SQLite(c.Request.Context(), h.deps.BackupDB, h.deps.BackupDir)
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": path})
}

func (h handlers) rebuildResourceIndex(c *gin.Context) {
	if h.deps.Resources == nil {
		errorText(c, http.StatusServiceUnavailable, "resources are unavailable")
		return
	}
	if err := h.deps.Resources.RebuildIndex(c.Request.Context()); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	stats, err := h.deps.Resources.IndexStats(c.Request.Context())
	if err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"rebuilt": true, "indexed_rows": stats.IndexedRows, "updated_at": stats.UpdatedAt})
}

func (h handlers) updateAccountProfile(c *gin.Context, account model.Account, profile telegram.Profile) {
	account.TelegramUserID = profile.TelegramUserID
	if profile.Phone != "" {
		account.Phone = profile.Phone
	}
	account.FirstName = profile.FirstName
	account.LastName = profile.LastName
	account.Username = profile.Username
	account.PhotoID = profile.PhotoID
	account.Status = model.AccountStatusOnline
	account.SessionPath = h.sessionPath(account.ID)
	now := time.Now().UTC()
	account.LastOnlineAt = &now
	account.LastError = ""
	if err := h.deps.Accounts.Update(c.Request.Context(), account); err != nil {
		errorJSON(c, http.StatusInternalServerError, err)
		return
	}

	// Trigger avatar download in background
	if h.deps.AvatarService != nil && account.PhotoID > 0 {
		h.deps.AvatarService.EnqueueAccountAvatar(context.WithoutCancel(c.Request.Context()), account)
	}

	h.respondWithOnlineAccount(c, account)
}

func (h handlers) saveQRProfile(ctx context.Context, profile telegram.Profile, tempSessionPath string) (model.Account, error) {
	phone := profile.Phone
	if phone == "" {
		phone = fmt.Sprintf("tg:%d", profile.TelegramUserID)
	}
	now := time.Now().UTC()
	account := model.Account{
		Phone:          phone,
		TelegramUserID: profile.TelegramUserID,
		FirstName:      profile.FirstName,
		LastName:       profile.LastName,
		Username:       profile.Username,
		PhotoID:        profile.PhotoID,
		Status:         model.AccountStatusOnline,
		SessionPath:    tempSessionPath,
		LastOnlineAt:   &now,
		LastError:      "",
	}
	accountID, err := h.deps.Accounts.Save(ctx, account)
	if err != nil {
		return model.Account{}, err
	}
	account.ID = accountID
	if h.deps.Sessions != nil && tempSessionPath != "" {
		finalPath, err := h.deps.Sessions.MoveTemporaryToAccount(tempSessionPath, account.ID)
		if err != nil {
			return model.Account{}, err
		}
		account.SessionPath = finalPath
		if err := h.deps.Accounts.Update(ctx, account); err != nil {
			return model.Account{}, err
		}
	}
	stored, err := h.deps.Accounts.FindByID(ctx, account.ID)
	if err != nil {
		return model.Account{}, err
	}
	return stored, nil
}

func (h handlers) respondWithOnlineAccount(c *gin.Context, account model.Account) {
	metadataSync := gin.H{"status": "skipped", "channel_count": 0}
	if h.deps.ChannelSync != nil {
		if h.deps.SyncQueue != nil {
			accountForSync := account
			jobCtx := context.WithoutCancel(c.Request.Context())
			job := h.deps.SyncQueue.Enqueue(jobCtx, "metadata-sync", func(ctx context.Context) error {
				items, err := h.deps.ChannelSync.SyncAccount(ctx, accountForSync)
				if err != nil {
					accountForSync.LastError = localizeDisplayError(err.Error())
					if updateErr := h.deps.Accounts.Update(ctx, accountForSync); updateErr != nil {
						return updateErr
					}
					return err
				}
				if h.deps.ChannelWebAccess != nil {
					channelIDs := make([]int64, 0, len(items))
					for _, item := range items {
						channelIDs = append(channelIDs, item.ID)
					}
					if len(channelIDs) > 0 {
						if _, err := h.deps.ChannelWebAccess.CheckMany(ctx, channelIDs); err != nil {
							return err
						}
					}
				}
				// Trigger channel avatar downloads in background
				if h.deps.AvatarService != nil {
					h.deps.AvatarService.EnqueueChannelAvatars(ctx, accountForSync, items)
				}
				return nil
			})
			metadataSync = gin.H{"status": "queued", "channel_count": 0, "job_id": job.ID}
			c.JSON(http.StatusOK, gin.H{
				"status":        model.AccountStatusOnline,
				"account":       account,
				"metadata_sync": metadataSync,
			})
			return
		}
		items, err := h.deps.ChannelSync.SyncAccount(c.Request.Context(), account)
		if err != nil {
			account.LastError = localizeDisplayError(err.Error())
			if updateErr := h.deps.Accounts.Update(c.Request.Context(), account); updateErr != nil {
				errorJSON(c, http.StatusInternalServerError, updateErr)
				return
			}
			metadataSync = gin.H{"status": "failed", "channel_count": 0, "error": localizeDisplayError(err.Error())}
		} else {
			metadataSync = gin.H{"status": "succeeded", "channel_count": len(items)}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"status":        model.AccountStatusOnline,
		"account":       account,
		"metadata_sync": metadataSync,
	})
}

func (h handlers) sessionPath(accountID int64) string {
	if h.deps.Sessions == nil {
		return ""
	}
	return h.deps.Sessions.PathForAccount(accountID)
}

func bindJSON(c *gin.Context, out any) bool {
	if err := c.ShouldBindJSON(out); err != nil {
		errorJSON(c, http.StatusBadRequest, err)
		return false
	}
	return true
}

func pathID(c *gin.Context) (int64, bool) {
	return pathPositiveInt64(c, "id")
}

func pathPositiveInt64(c *gin.Context, key string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || id <= 0 {
		errorText(c, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func queryPositiveInt64(c *gin.Context, key string) (int64, bool) {
	value := c.Query(key)
	if value == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		errorText(c, http.StatusBadRequest, key+" must be a positive integer")
		return 0, false
	}
	return n, true
}

func queryNonNegativeInt(c *gin.Context, key string) (int, bool) {
	value := c.Query(key)
	if value == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		errorText(c, http.StatusBadRequest, key+" must be a non-negative integer")
		return 0, false
	}
	if int64(int(n)) != n {
		errorText(c, http.StatusBadRequest, key+" is too large")
		return 0, false
	}
	return int(n), true
}

func readFilters(c *gin.Context) (accountID int64, channelID int64, limit int, offset int, ok bool) {
	accountID, ok = queryPositiveInt64(c, "account_id")
	if !ok {
		return 0, 0, 0, 0, false
	}
	channelID, ok = queryPositiveInt64(c, "channel_id")
	if !ok {
		return 0, 0, 0, 0, false
	}
	limit, ok = queryNonNegativeInt(c, "limit")
	if !ok {
		return 0, 0, 0, 0, false
	}
	offset, ok = queryNonNegativeInt(c, "offset")
	if !ok {
		return 0, 0, 0, 0, false
	}
	return accountID, channelID, limit, offset, true
}

func parseDateRange(c *gin.Context) (*time.Time, *time.Time, bool) {
	from, ok := parseDateQuery(c, "date_from", false)
	if !ok {
		return nil, nil, false
	}
	to, ok := parseDateQuery(c, "date_to", true)
	if !ok {
		return nil, nil, false
	}
	return from, to, true
}

func parseDateQuery(c *gin.Context, key string, end bool) (*time.Time, bool) {
	value := c.Query(key)
	if value == "" {
		return nil, true
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		if end {
			t = t.Add(time.Nanosecond)
		}
		return &t, true
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		if end {
			t = t.AddDate(0, 0, 1)
		}
		return &t, true
	}
	errorText(c, http.StatusBadRequest, key+" must be YYYY-MM-DD or RFC3339")
	return nil, false
}

func parseCursor(c *gin.Context) (*time.Time, int64, bool) {
	beforeDateRaw := c.Query("before_date")
	beforeIDRaw := c.Query("before_id")
	if beforeDateRaw == "" && beforeIDRaw == "" {
		return nil, 0, true
	}
	if beforeDateRaw == "" || beforeIDRaw == "" {
		errorText(c, http.StatusBadRequest, "before_date and before_id must be provided together")
		return nil, 0, false
	}
	beforeDate, ok := parseDateQuery(c, "before_date", false)
	if !ok {
		return nil, 0, false
	}
	beforeID, err := strconv.ParseInt(beforeIDRaw, 10, 64)
	if err != nil || beforeID <= 0 {
		errorText(c, http.StatusBadRequest, "before_id must be a positive integer")
		return nil, 0, false
	}
	return beforeDate, beforeID, true
}

func errorJSON(c *gin.Context, status int, err error) {
	errorText(c, status, err.Error())
}

func errorText(c *gin.Context, status int, msg string) {
	code := "internal_error"
	if status >= http.StatusBadRequest && status < http.StatusInternalServerError {
		code = "bad_request"
	}
	errorWithCode(c, status, code, msg)
}

func errorWithCode(c *gin.Context, status int, code string, msg string) {
	if status >= http.StatusInternalServerError {
		apiLogger(c).Error("api request failed",
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.String("error", msg),
		)
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": localizedErrorMessage(status, msg)}})
}

func apiLogger(c *gin.Context) *zap.Logger {
	if value, ok := c.Get(apiLoggerKey); ok {
		if logger, ok := value.(*zap.Logger); ok && logger != nil {
			return logger
		}
	}
	return zap.L()
}
