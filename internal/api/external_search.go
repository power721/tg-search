package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tg-search/internal/model"
	"tg-search/internal/resource"
)

const externalSearchDefaultLimit = 50
const externalSearchMaxLimit = 3000
const externalSearchAccessKey = "external_search.access"

type externalSearchRequest struct {
	Keyword              string   `json:"kw"`
	Query                string   `json:"q"`
	ResultType           string   `json:"res"`
	CloudTypes           []string `json:"cloud_types"`
	IncludeImage         bool     `json:"include_image"`
	IncludeMediaMetadata bool     `json:"include_media_metadata"`
	MediaMetadata        bool     `json:"media_metadata"`
	Limit                int      `json:"limit"`
	Offset               int      `json:"offset"`
}

type externalAPIResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type externalSearchResponse struct {
	Total        int                             `json:"total"`
	Results      []externalSearchResult          `json:"results,omitempty"`
	MergedByType map[string][]externalMergedLink `json:"merged_by_type,omitempty"`
}

type externalSearchResult struct {
	UniqueID string         `json:"unique_id"`
	Datetime time.Time      `json:"datetime"`
	Title    string         `json:"title"`
	Content  string         `json:"content,omitempty"`
	Links    []externalLink `json:"links"`
	Images   []string       `json:"images,omitempty"`
	Media    *externalMedia `json:"media,omitempty"`
}

type externalLink struct {
	Type      string         `json:"type"`
	URL       string         `json:"url"`
	Size      int64          `json:"size,omitempty"`
	Password  string         `json:"password,omitempty"`
	Datetime  time.Time      `json:"datetime,omitempty"`
	WorkTitle string         `json:"work_title,omitempty"`
	Media     *externalMedia `json:"media,omitempty"`
}

type externalMergedLink struct {
	URL      string         `json:"url"`
	Password string         `json:"password,omitempty"`
	Note     string         `json:"note,omitempty"`
	Size     int64          `json:"size,omitempty"`
	Datetime time.Time      `json:"datetime"`
	Images   []string       `json:"images,omitempty"`
	Media    *externalMedia `json:"media,omitempty"`
}

type externalMedia struct {
	Title    string `json:"title,omitempty"`
	Year     string `json:"year,omitempty"`
	Season   string `json:"season,omitempty"`
	Episode  string `json:"episode,omitempty"`
	Quality  string `json:"quality,omitempty"`
	Size     string `json:"size,omitempty"`
	TMDBID   string `json:"tmdb_id,omitempty"`
	Category string `json:"category,omitempty"`
	Tags     string `json:"tags,omitempty"`
}

type externalResourceFilter struct {
	category string
	typ      string
}

type externalResourceItemsResult struct {
	Items []resource.Item
	Total int
}

type externalSearchAccess struct {
	Keyword              string
	ResultType           string
	CloudTypes           []string
	IncludeImage         bool
	IncludeMediaMetadata bool
	Limit                int
	Offset               int
	Total                int
	Returned             int
	Error                string
}

func (h handlers) externalSearchAccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		status := c.Writer.Status()
		fields := []zap.Field{
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.Int("status", status),
			zap.Duration("duration", time.Since(start)),
			zap.String("client_ip", c.ClientIP()),
			zap.String("remote_addr", c.Request.RemoteAddr),
			zap.String("user_agent", c.Request.UserAgent()),
		}
		if keyID, ok := c.Get(apiKeyIDKey); ok {
			if id, ok := keyID.(int64); ok {
				fields = append(fields, zap.Int64("api_key_id", id))
			}
		}
		if value, ok := c.Get(externalSearchAccessKey); ok {
			if access, ok := value.(externalSearchAccess); ok {
				fields = append(fields,
					zap.String("keyword", access.Keyword),
					zap.String("result_type", access.ResultType),
					zap.Bool("include_image", access.IncludeImage),
					zap.Bool("include_media_metadata", access.IncludeMediaMetadata),
					zap.Int("limit", access.Limit),
					zap.Int("offset", access.Offset),
					zap.Int("total", access.Total),
					zap.Int("returned", access.Returned),
				)
				if len(access.CloudTypes) > 0 {
					fields = append(fields, zap.Strings("cloud_types", access.CloudTypes))
				}
				if access.Error != "" {
					fields = append(fields, zap.String("error", access.Error))
				}
			}
		}
		apiLogger(c).Info("public search access", fields...)
	}
}

func (h handlers) externalSearch(c *gin.Context) {
	if h.deps.Resources == nil {
		setExternalSearchAccess(c, externalSearchAccess{Error: "resources are unavailable"})
		c.JSON(http.StatusServiceUnavailable, externalAPIResponse{Code: http.StatusServiceUnavailable, Message: "resources are unavailable"})
		return
	}
	req, ok := readExternalSearchRequest(c)
	if !ok {
		return
	}
	keyword := strings.TrimSpace(firstNonEmptyString(req.Keyword, req.Query))
	resultType := normalizeExternalResultType(req.ResultType)
	access := externalSearchAccess{
		Keyword:              keyword,
		ResultType:           resultType,
		CloudTypes:           normalizeExternalCloudTypes(req.CloudTypes),
		IncludeImage:         req.IncludeImage,
		IncludeMediaMetadata: req.includeMediaMetadata(),
		Limit:                normalizeExternalLimit(req.Limit),
		Offset:               normalizeExternalOffset(req.Offset),
	}
	if resultType == "" {
		access.ResultType = strings.TrimSpace(req.ResultType)
		access.Error = "invalid res"
		setExternalSearchAccess(c, access)
		c.JSON(http.StatusBadRequest, externalAPIResponse{Code: http.StatusBadRequest, Message: "invalid res"})
		return
	}
	limit := access.Limit
	offset := access.Offset
	items, total, err := h.externalResourceItems(c, keyword, req.CloudTypes, limit, offset, req.IncludeImage)
	if err != nil {
		access.Error = err.Error()
		setExternalSearchAccess(c, access)
		c.JSON(http.StatusInternalServerError, externalAPIResponse{Code: http.StatusInternalServerError, Message: "search failed: " + err.Error()})
		return
	}
	response := buildExternalSearchResponse(items, total, resultType, req.includeMediaMetadata(), req.IncludeImage)
	access.Total = total
	access.Returned = len(items)
	setExternalSearchAccess(c, access)
	c.PureJSON(http.StatusOK, externalAPIResponse{Code: 0, Message: "success", Data: response})
}

func setExternalSearchAccess(c *gin.Context, access externalSearchAccess) {
	c.Set(externalSearchAccessKey, access)
}

func (r externalSearchRequest) includeMediaMetadata() bool {
	return r.IncludeMediaMetadata || r.MediaMetadata
}

func readExternalSearchRequest(c *gin.Context) (externalSearchRequest, bool) {
	if c.Request.Method == http.MethodGet {
		limit, ok := optionalQueryNonNegativeInt(c, "limit")
		if !ok {
			return externalSearchRequest{}, false
		}
		offset, ok := optionalQueryNonNegativeInt(c, "offset")
		if !ok {
			return externalSearchRequest{}, false
		}
		includeMediaMetadata, ok := optionalQueryBool(c, "include_media_metadata", "media_metadata")
		if !ok {
			return externalSearchRequest{}, false
		}
		includeImage, ok := optionalQueryBool(c, "include_image")
		if !ok {
			return externalSearchRequest{}, false
		}
		return externalSearchRequest{
			Keyword:              firstQuery(c, "kw", "q", "keyword"),
			ResultType:           c.Query("res"),
			CloudTypes:           splitCSV(c.Query("cloud_types")),
			IncludeImage:         includeImage,
			IncludeMediaMetadata: includeMediaMetadata,
			Limit:                limit,
			Offset:               offset,
		}, true
	}
	var req externalSearchRequest
	if !bindJSON(c, &req) {
		return externalSearchRequest{}, false
	}
	return req, true
}

func optionalQueryBool(c *gin.Context, keys ...string) (bool, bool) {
	for _, key := range keys {
		raw := strings.TrimSpace(c.Query(key))
		if raw == "" {
			continue
		}
		value, err := parseExternalBool(raw)
		if err != nil {
			errorText(c, http.StatusBadRequest, key+" must be a boolean")
			return false, false
		}
		return value, true
	}
	return false, true
}

func parseExternalBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	default:
		return false, strconv.ErrSyntax
	}
}

func optionalQueryNonNegativeInt(c *gin.Context, key string) (int, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		errorText(c, http.StatusBadRequest, key+" must be a non-negative integer")
		return 0, false
	}
	return value, true
}

func normalizeExternalResultType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "merge", "merged_by_type":
		return "merged_by_type"
	case "results":
		return "results"
	case "all":
		return "all"
	default:
		return ""
	}
}

func normalizeExternalLimit(limit int) int {
	if limit <= 0 {
		return externalSearchDefaultLimit
	}
	if limit > externalSearchMaxLimit {
		return externalSearchMaxLimit
	}
	return limit
}

func normalizeExternalOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

func (h handlers) externalResourceItems(c *gin.Context, keyword string, cloudTypes []string, limit int, offset int, includeImage bool) ([]resource.Item, int, error) {
	filters := externalResourceFilters(cloudTypes)
	if result, ok, err := h.externalResourceItemsIndexed(c, keyword, filters, limit, offset, includeImage); ok || err != nil {
		if err != nil {
			return nil, 0, err
		}
		return result.Items, result.Total, nil
	}
	fetchLimit := limit + offset
	seen := map[string]struct{}{}
	items := make([]resource.Item, 0, fetchLimit)
	total := 0
	for _, filter := range filters {
		result, err := h.deps.Resources.List(c.Request.Context(), resource.Query{
			Keyword:  keyword,
			Type:     filter.typ,
			Category: filter.category,
			Limit:    fetchLimit,
			MaxLimit: fetchLimit,
			Sort:     "date_desc",
		})
		if err != nil {
			return nil, 0, err
		}
		total += result.Total
		for i := range result.Items {
			result.Items[i].ChannelUsername = ""
		}
		result.Items, err = h.attachMediaToExternalResourceItems(c.Request.Context(), result.Items, true, includeImage)
		if err != nil {
			return nil, 0, err
		}
		for _, item := range result.Items {
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			items = append(items, item)
		}
	}
	resource.SortItemsByQuality(items, keyword)
	if offset > len(items) {
		return []resource.Item{}, total, nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end], total, nil
}

func (h handlers) externalResourceItemsIndexed(c *gin.Context, keyword string, filters []externalResourceFilter, limit int, offset int, includeImage bool) (externalResourceItemsResult, bool, error) {
	if len(filters) == 0 {
		return externalResourceItemsResult{Items: []resource.Item{}, Total: 0}, true, nil
	}
	indexFilters := make([]model.ResourceIndexFilter, 0, len(filters))
	for _, filter := range filters {
		indexFilters = append(indexFilters, model.ResourceIndexFilter{Category: filter.category, Type: filter.typ})
	}
	sortOrder := "date_desc"
	if strings.TrimSpace(keyword) != "" {
		sortOrder = "hot"
	}
	result, ok, err := h.deps.Resources.ListIndexed(c.Request.Context(), model.ResourceIndexQuery{
		Keyword:  keyword,
		Filters:  indexFilters,
		Limit:    limit,
		Offset:   offset,
		MaxLimit: externalSearchMaxLimit,
		Sort:     sortOrder,
	})
	if err != nil || !ok {
		return externalResourceItemsResult{}, ok, err
	}
	for i := range result.Items {
		result.Items[i].ChannelUsername = ""
	}
	result.Items, err = h.attachMediaToExternalResourceItems(c.Request.Context(), result.Items, true, includeImage)
	if err != nil {
		return externalResourceItemsResult{}, true, err
	}
	return externalResourceItemsResult{Items: result.Items, Total: result.Total}, true, nil
}

func externalResourceFilters(cloudTypes []string) []externalResourceFilter {
	values := normalizeExternalCloudTypes(cloudTypes)
	if len(values) == 0 {
		return []externalResourceFilter{
			{category: "cloud_drive"},
			{category: "magnet"},
			{category: "ed2k"},
			{category: "files", typ: "video"},
		}
	}
	var filters []externalResourceFilter
	seen := map[string]struct{}{}
	hasAllCloudDrives := hasExternalCloudDriveGroup(values)
	for _, value := range values {
		switch {
		case value == "cloud_drive" || value == "pan" || value == "drive":
			addExternalResourceFilter(&filters, seen, externalResourceFilter{category: "cloud_drive"})
		case value == "magnet":
			addExternalResourceFilter(&filters, seen, externalResourceFilter{category: "magnet"})
		case value == "ed2k":
			addExternalResourceFilter(&filters, seen, externalResourceFilter{category: "ed2k"})
		case value == "video":
			addExternalResourceFilter(&filters, seen, externalResourceFilter{category: "files", typ: "video"})
		case isCloudDriveProvider(value):
			if !hasAllCloudDrives {
				addExternalResourceFilter(&filters, seen, externalResourceFilter{category: "cloud_drive", typ: value})
			}
		}
	}
	return filters
}

func hasExternalCloudDriveGroup(values []string) bool {
	for _, value := range values {
		if value == "cloud_drive" || value == "pan" || value == "drive" {
			return true
		}
	}
	return false
}

func normalizeExternalCloudTypes(cloudTypes []string) []string {
	var out []string
	for _, raw := range cloudTypes {
		for _, part := range strings.Split(raw, ",") {
			value := strings.ToLower(strings.TrimSpace(part))
			if value != "" {
				out = append(out, normalizeExternalCloudType(value))
			}
		}
	}
	return out
}

func normalizeExternalCloudType(value string) string {
	switch value {
	case "百度", "百度云", "百度云盘", "百度网盘":
		return "baidu"
	case "阿里", "阿里云", "阿里云盘", "阿里盘", "alipan", "aliyundrive":
		return "aliyun"
	case "夸克", "夸克云盘", "夸克网盘":
		return "quark"
	case "光鸭", "光鸭盘", "光鸭资源":
		return "guangya"
	case "天翼", "天翼云", "天翼云盘":
		return "tianyi"
	case "115网盘", "115云盘":
		return "115"
	case "迅雷", "迅雷云盘", "迅雷网盘":
		return "xunlei"
	case "移动", "移动云盘", "中国移动", "和彩云", "和彩云网盘":
		return "mobile"
	case "uc", "uc云盘", "uc网盘":
		return "uc"
	case "pikpak", "pikpak网盘":
		return "pikpak"
	case "123", "123pan", "123云盘", "123网盘", "123盘", "pan123":
		return "123"
	case "磁力":
		return "magnet"
	case "电驴", "电驴链接":
		return "ed2k"
	default:
		return value
	}
}

func addExternalResourceFilter(filters *[]externalResourceFilter, seen map[string]struct{}, filter externalResourceFilter) {
	key := filter.category + ":" + filter.typ
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	*filters = append(*filters, filter)
}

func isCloudDriveProvider(value string) bool {
	switch value {
	case "quark", "baidu", "aliyun", "uc", "xunlei", "tianyi", "115", "mobile", "pikpak", "123", "guangya", "weiyun", "lanzou", "jianguoyun":
		return true
	default:
		return false
	}
}

func (h handlers) attachMediaToExternalResourceItems(ctx context.Context, items []resource.Item, signed bool, includeImage bool) ([]resource.Item, error) {
	for i := range items {
		if items[i].Kind != "file" && !includeImage {
			continue
		}
		media, err := h.resourceItemMedia(ctx, items[i], signed)
		if err != nil {
			return nil, err
		}
		if media == nil {
			continue
		}
		imageURL := ""
		if includeImage {
			imageURL = media.ImageURL
		}
		items[i].SetMediaURLs(imageURL, media.VideoURL)
	}
	return items, nil
}

func buildExternalSearchResponse(items []resource.Item, total int, resultType string, includeMediaMetadata bool, includeImage bool) externalSearchResponse {
	results := make([]externalSearchResult, 0, len(items))
	merged := map[string][]externalMergedLink{}
	for _, item := range items {
		result := externalResultFromResource(item, includeMediaMetadata, includeImage)
		if len(result.Links) == 0 {
			continue
		}
		results = append(results, result)
		for _, link := range result.Links {
			merged[link.Type] = append(merged[link.Type], externalMergedLink{
				URL:      link.URL,
				Password: link.Password,
				Note:     firstNonEmptyString(link.WorkTitle, result.Title),
				Size:     link.Size,
				Datetime: link.Datetime,
				Images:   result.Images,
				Media:    link.Media,
			})
		}
	}
	response := externalSearchResponse{Total: total}
	if resultType == "results" || resultType == "all" {
		response.Results = results
	}
	if resultType == "merged_by_type" || resultType == "all" {
		response.MergedByType = merged
	}
	return response
}

func externalResultFromResource(item resource.Item, includeMediaMetadata bool, includeImage bool) externalSearchResult {
	title := externalResourceTitle(item, includeMediaMetadata)
	link := externalLink{
		Type:      externalResourceType(item),
		URL:       externalResourceURL(item),
		Size:      item.SizeBytes,
		Password:  item.Password,
		Datetime:  item.Datetime,
		WorkTitle: title,
	}
	media := externalMediaFromResource(item)
	if includeMediaMetadata {
		link.Media = media
	}
	result := externalSearchResult{
		UniqueID: item.ID,
		Datetime: item.Datetime,
		Title:    title,
		Links:    []externalLink{},
	}
	if includeMediaMetadata {
		result.Media = media
	}
	if imageURL := externalResourceImageURL(item, includeImage); imageURL != "" {
		result.Images = []string{imageURL}
	}
	if link.URL != "" {
		result.Links = append(result.Links, link)
	}
	return result
}

func externalResourceTitle(item resource.Item, includeMediaMetadata bool) string {
	if includeMediaMetadata {
		return firstNonEmptyString(item.Title, item.MediaTitle, item.Note, item.FileName, item.URL)
	}
	return firstNonEmptyString(item.Note, item.FileName, item.URL)
}

func externalMediaFromResource(item resource.Item) *externalMedia {
	media := externalMedia{
		Title:    item.MediaTitle,
		Year:     item.MediaYear,
		Season:   item.MediaSeason,
		Episode:  item.MediaEpisode,
		Quality:  item.MediaQuality,
		Size:     item.MediaSize,
		TMDBID:   item.MediaTMDBID,
		Category: item.MediaCategory,
		Tags:     item.MediaTags,
	}
	if media == (externalMedia{}) {
		return nil
	}
	return &media
}

func externalResourceType(item resource.Item) string {
	if item.Kind == "file" {
		return "video"
	}
	if item.Category == "magnet" || item.Category == "ed2k" {
		return item.Category
	}
	if item.Type != "" && item.Type != "url" {
		return item.Type
	}
	return item.Category
}

func externalResourceURL(item resource.Item) string {
	if item.Kind == "file" {
		if item.Media != nil && item.Media.VideoURL != "" {
			return item.Media.VideoURL
		}
		return ""
	}
	return item.URL
}

func externalResourceImageURL(item resource.Item, includeImage bool) string {
	if !includeImage {
		return ""
	}
	if item.Media == nil {
		return ""
	}
	return item.Media.ImageURL
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
