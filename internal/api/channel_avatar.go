package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	avatarpkg "tg-search/internal/avatar"
)

func (h handlers) serveChannelAvatar(c *gin.Context) {
	if h.deps.Telegram == nil {
		errorText(c, http.StatusServiceUnavailable, "telegram client is unavailable")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		errorText(c, http.StatusBadRequest, "id must be a positive integer")
		return
	}
	channel, err := h.deps.Channels.FindByID(c.Request.Context(), id)
	if err != nil {
		errorText(c, http.StatusNotFound, "channel not found")
		return
	}
	if channel.PhotoID <= 0 {
		errorText(c, http.StatusNotFound, "channel has no avatar")
		return
	}

	// Set ETag based on photo ID for efficient browser caching
	etag := fmt.Sprintf(`"ch-%d-%d"`, channel.ID, channel.PhotoID)
	c.Header("ETag", etag)

	// Check If-None-Match header for 304 Not Modified response
	if match := c.GetHeader("If-None-Match"); match == etag {
		c.Status(http.StatusNotModified)
		return
	}

	// Check local file first
	if h.deps.RuntimeConfig.Storage.Path != "" {
		localPath := avatarpkg.AvatarAbsolutePath(h.deps.RuntimeConfig.Storage.Path, "channel", channel.ID, channel.PhotoID)
		if avatarpkg.FileExists(localPath) {
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
			c.File(localPath)
			return
		}
	}

	// File not found locally, return 404
	errorText(c, http.StatusNotFound, "avatar not downloaded yet")
}
