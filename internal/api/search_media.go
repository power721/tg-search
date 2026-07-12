package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"tg-search/internal/apikey"
	"tg-search/internal/model"
	"tg-search/internal/resource"
)

const searchMediaURLTTL = 24 * time.Hour

type mediaURLSigner struct {
	key string
	exp string
}

func newMediaURLSigner(key string, expiresAt time.Time) mediaURLSigner {
	exp := ""
	if key != "" {
		exp = strconv.FormatInt(expiresAt.UTC().Unix(), 10)
	}
	return mediaURLSigner{key: key, exp: exp}
}

func (h handlers) requestMediaURLSigner(ctx context.Context, signed bool) (mediaURLSigner, error) {
	if !signed || h.deps.APIKeyService == nil {
		return mediaURLSigner{}, nil
	}
	active, err := h.deps.APIKeyService.EnsureActive(ctx)
	if err != nil {
		return mediaURLSigner{}, err
	}
	return newMediaURLSigner(active.Key, time.Now().UTC().Add(searchMediaURLTTL)), nil
}

func (s mediaURLSigner) mediaURL(kind string, telegramFileID int64) (string, error) {
	path := "/" + kind + "/" + url.PathEscape(strconv.FormatInt(telegramFileID, 10))
	if s.key == "" {
		return path, nil
	}
	sig, err := apikey.MediaSignature(s.key, http.MethodGet, path, s.exp)
	if err != nil {
		return "", err
	}
	values := url.Values{}
	values.Set("exp", s.exp)
	values.Set("sig", sig)
	return path + "?" + values.Encode(), nil
}

func (s mediaURLSigner) mediaURLs(imageFileID int64, videoFileID int64) (*model.MediaURLs, error) {
	if imageFileID <= 0 && videoFileID <= 0 {
		return nil, nil
	}
	var media model.MediaURLs
	if imageFileID > 0 {
		imageURL, err := s.mediaURL("i", imageFileID)
		if err != nil {
			return nil, err
		}
		media.ImageURL = imageURL
	}
	if videoFileID > 0 {
		videoURL, err := s.mediaURL("v", videoFileID)
		if err != nil {
			return nil, err
		}
		media.VideoURL = videoURL
	}
	return &media, nil
}

func (h handlers) shouldSignMediaURLs(c *gin.Context) bool {
	if h.hasAdminSession(c) {
		return false
	}
	return apiKeyFromRequest(c.Request) != ""
}

func (h handlers) attachMediaToSearchResults(ctx context.Context, items []model.SearchResult, signed bool) ([]model.SearchResult, error) {
	for i := range items {
		media, err := h.searchResultMedia(ctx, items[i].ID, items[i].MessageType, items[i].Files, signed)
		if err != nil {
			return nil, err
		}
		items[i].Media = media
	}
	return items, nil
}

func (h handlers) attachMediaToFileResults(ctx context.Context, items []model.FileResult, signed bool) ([]model.FileResult, error) {
	for i := range items {
		media, err := h.fileResultMedia(ctx, items[i].File, signed)
		if err != nil {
			return nil, err
		}
		items[i].Media = media
	}
	return items, nil
}

func (h handlers) attachMediaToLinkResults(ctx context.Context, items []model.LinkResult, signed bool) ([]model.LinkResult, error) {
	for i := range items {
		media, err := h.searchResultMedia(ctx, items[i].MessageID, items[i].MessageType, nil, signed)
		if err != nil {
			return nil, err
		}
		items[i].Media = media
	}
	return items, nil
}

func (h handlers) attachMediaToRemoteSearchResults(ctx context.Context, result model.RemoteSearchResults, signed bool) (model.RemoteSearchResults, error) {
	for i := range result.Items {
		media, err := h.searchResultMedia(ctx, 0, result.Items[i].MessageType, result.Items[i].Files, signed)
		if err != nil {
			return model.RemoteSearchResults{}, err
		}
		result.Items[i].Media = media
	}
	return result, nil
}

func (h handlers) attachMediaToResourceItems(ctx context.Context, items []resource.Item, signed bool) ([]resource.Item, error) {
	if len(items) == 0 || h.deps.Files == nil {
		return items, nil
	}

	// Collect all message references that need file lookups
	type msgRef struct {
		ChannelID int64
		MessageID int64
	}
	refs := make([]struct{ ChannelID, MessageID int64 }, 0, len(items))
	refMap := make(map[string]int) // key -> item index

	for i := range items {
		// Skip items that already have file info inline
		if items[i].Kind == "file" && items[i].TelegramFileID > 0 {
			continue
		}
		// Need to fetch files from database
		if items[i].ChannelID > 0 && items[i].TelegramMessageID > 0 {
			key := fmt.Sprintf("%d:%d", items[i].ChannelID, items[i].TelegramMessageID)
			if _, exists := refMap[key]; !exists {
				refs = append(refs, struct{ ChannelID, MessageID int64 }{
					ChannelID: items[i].ChannelID,
					MessageID: items[i].TelegramMessageID,
				})
				refMap[key] = i
			}
		}
	}

	// Batch-fetch all files in one query
	var filesMap map[string][]model.File
	if len(refs) > 0 {
		var err error
		filesMap, err = h.deps.Files.FindByMessageRefs(ctx, refs)
		if err != nil {
			return nil, err
		}
	}

	// Attach media to each item
	for i := range items {
		var files []model.File
		if items[i].Kind == "file" && items[i].TelegramFileID > 0 {
			// Inline file info
			files = []model.File{{
				TelegramFileID: items[i].TelegramFileID,
				FileName:       items[i].FileName,
				Extension:      items[i].Extension,
				MimeType:       items[i].MimeType,
				SizeBytes:      items[i].SizeBytes,
				Category:       items[i].Category,
			}}
		} else if filesMap != nil {
			// Files fetched from database
			key := fmt.Sprintf("%d:%d", items[i].ChannelID, items[i].TelegramMessageID)
			files = filesMap[key]
		}

		media, err := h.searchResultMedia(ctx, 0, items[i].MessageType, files, signed)
		if err != nil {
			return nil, err
		}
		if media != nil {
			items[i].SetMediaURLs(media.ImageURL, media.VideoURL)
		}
	}
	return items, nil
}

func (h handlers) searchResultMedia(ctx context.Context, messageID int64, messageType string, files []model.File, signed bool) (*model.MediaURLs, error) {
	if len(files) == 0 && messageID > 0 && h.deps.Files != nil {
		found, err := h.deps.Files.FindByMessageID(ctx, messageID)
		if err != nil {
			return nil, err
		}
		files = found
	}
	imageFileID, videoFileID := mediaFileIDs(messageType, files)
	return h.mediaURLs(ctx, imageFileID, videoFileID, signed)
}

func mediaFileIDs(messageType string, files []model.File) (int64, int64) {
	var imageFileID int64
	var videoFileID int64
	for _, file := range files {
		if file.TelegramFileID <= 0 {
			continue
		}
		if videoFileID == 0 && isVideoMedia(file.MimeType, file.Extension, file.FileName) {
			videoFileID = file.TelegramFileID
		}
		if imageFileID == 0 && isImageMedia(file.MimeType, file.Extension, file.FileName) {
			imageFileID = file.TelegramFileID
		}
	}
	if videoFileID > 0 && imageFileID == 0 {
		imageFileID = videoFileID
	}
	if messageType == "photo" && imageFileID == 0 {
		imageFileID = firstTelegramFileID(files)
	}
	return imageFileID, videoFileID
}

func (h handlers) fileResultMedia(ctx context.Context, file model.File, signed bool) (*model.MediaURLs, error) {
	if file.TelegramFileID <= 0 {
		return nil, nil
	}
	hasVideo := isVideoMedia(file.MimeType, file.Extension, file.FileName)
	hasImage := hasVideo || isImageMedia(file.MimeType, file.Extension, file.FileName)
	var imageFileID int64
	var videoFileID int64
	if hasImage {
		imageFileID = file.TelegramFileID
	}
	if hasVideo {
		videoFileID = file.TelegramFileID
	}
	return h.mediaURLs(ctx, imageFileID, videoFileID, signed)
}

func (h handlers) mediaURLs(ctx context.Context, imageFileID int64, videoFileID int64, signed bool) (*model.MediaURLs, error) {
	signer, err := h.requestMediaURLSigner(ctx, signed)
	if err != nil {
		return nil, err
	}
	return signer.mediaURLs(imageFileID, videoFileID)
}

func (h handlers) mediaURL(ctx context.Context, kind string, telegramFileID int64, signed bool) (string, error) {
	signer, err := h.requestMediaURLSigner(ctx, signed)
	if err != nil {
		return "", err
	}
	return signer.mediaURL(kind, telegramFileID)
}

func firstTelegramFileID(files []model.File) int64 {
	for _, file := range files {
		if file.TelegramFileID > 0 {
			return file.TelegramFileID
		}
	}
	return 0
}

func isVideoMedia(mimeType string, extension string, fileName string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if strings.HasPrefix(mimeType, "video/") {
		return true
	}
	switch mediaExtension(extension, fileName) {
	case ".mp4", ".m4v", ".mkv", ".mov", ".avi", ".webm", ".flv", ".wmv", ".ts":
		return true
	default:
		return false
	}
}

func isImageMedia(mimeType string, extension string, fileName string) bool {
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	switch mediaExtension(extension, fileName) {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp":
		return true
	default:
		return false
	}
}

func mediaExtension(extension string, fileName string) string {
	extension = strings.ToLower(strings.TrimSpace(extension))
	if extension != "" {
		if !strings.HasPrefix(extension, ".") {
			extension = "." + extension
		}
		return extension
	}
	if idx := strings.LastIndex(fileName, "."); idx >= 0 {
		return strings.ToLower(fileName[idx:])
	}
	return ""
}
