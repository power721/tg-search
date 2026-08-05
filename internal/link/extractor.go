package link

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"tg-search/internal/model"
)

type Parser interface {
	Extract(text string) []Candidate
}

type Candidate struct {
	Type       string
	URL        string
	Password   string
	MatchStart int
	MatchEnd   int
}

type mediaMetadata struct {
	Title    string
	Year     string
	Season   string
	Episode  string
	Quality  string
	Size     string
	TMDBID   string
	Category string
	Tags     string
}

type Extractor struct {
	parsers         []Parser
	passwordPattern *regexp.Regexp
}

type regexParser struct {
	typ       string
	pattern   *regexp.Regexp
	urlGroup  int
	passGroup int
}

func NewExtractor() *Extractor {
	return &Extractor{
		parsers: []Parser{
			providerParser("115", `(?i)(https?://(?:115|115cdn|anxia)\.com/s/[\w-]+(?:\?password=([\w-]+))?)`, 1, 2),
			providerParser("xunlei", `(?i)(https?://pan\.xunlei\.com/s/[\w-]+(?:\?pwd=([\w-]+))?)`, 1, 2),
			providerParser("baidu", `(?i)(https?://pan\.baidu\.com/s/[\w-]+(?:\?pwd=([\w-]+))?)`, 1, 2),
			providerParser("baidu", `(?i)(https?://pan\.baidu\.com/(?:share|wap)/init\?[^\s"'<>，。；、)）]+)`, 1, 0),
			providerParser("pikpak", `(?i)(https?://mypikpak\.com/s/[\w-]+(?:\?[^\s"'<>，。；、)）]+)?)`, 1, 0),
			providerParser("tianyi", `(?i)(https?://cloud\.189\.cn/web/share\?code=[\w-]+)`, 1, 0),
			providerParser("tianyi", `(?i)(https?://cloud\.189\.cn/t/[\w-]+)%EF%BC%88%E8%AE%BF%E9%97%AE%E7%A0%81%EF%BC%9A(\w+)%EF%BC%89`, 1, 2),
			providerParser("tianyi", `(?i)(https?://cloud\.189\.cn/t/[\w-]+(?:%[0-9A-Fa-f]{2})*)(?:（访问码：(\w+)）)?`, 1, 2),
			providerParser("tianyi", `(?i)(https?://h5\.cloud\.189\.cn/share\.html#/t/[\w-]+)`, 1, 0),
			providerParser("mobile", `(?i)(https?://(?:www\.)?caiyun\.139\.com/(?:m/i\?[\w-]+(?:&[\w%-]+=[\w%-]+)*|w/i/[\w-]+(?:\?[\w%-]+=[\w%-]+(?:&[\w%-]+=[\w%-]+)*)?))`, 1, 0),
			providerParser("mobile", `(?i)(https?://(?:www\.)?yun\.139\.com/shareweb/#/w/i/[\w-]+(?:\?[\w%-]+=[\w%-]+(?:&[\w%-]+=[\w%-]+)*)?)`, 1, 0),
			providerParser("mobile", `(?i)(https?://caiyun\.feixin\.10086\.cn/[\w-]+(?:\?[\w%-]+=[\w%-]+(?:&[\w%-]+=[\w%-]+)*)?)`, 1, 0),
			providerParser("quark", `(?i)(https?://pan\.quark\.cn/s/[\w-]+)`, 1, 0),
			providerParser("uc", `(?i)(https?://(?:drive|fast)\.uc\.cn/s/[\w-]+(?:\?[\w%-]+=[\w%-]+(?:&[\w%-]+=[\w%-]+)*)?)`, 1, 0),
			providerParser("aliyun", `(?i)(https?://(?:www\.)?(?:alipan|aliyundrive)\.com/s/[\w-]+(?:/folder/[\w-]+)?(?:\?password=([\w-]+))?)`, 1, 2),
			providerParser("123", `(?i)(https?://(?:www\.)?123(?:684|865|685|912|pan|592)\.(?:com|cn)/s/[\w-]+(?:\.html)?)(?:(?:\?(?:pwd=|(?:%E6%8F%90%E5%8F%96%E7%A0%81|提取码)[:：])|提取码[:：])([\w-]+))?`, 1, 2),
			providerParser("123", `(?i)(https?://[A-Za-z0-9-]+\.share\.123pan\.cn/123pan/[\w-]+(?:\?pwd=([\w-]+))?)`, 1, 2),
			providerParser("guangya", `(?i)(https?://(?:www\.)?guangyapan\.com/s/[A-Za-z0-9_-]+)`, 1, 0),
			providerParser("magnet", `(?i)(magnet:\?[^\s"'<>，。；、]+)`, 1, 0),
			providerParser("ed2k", `(?i)(ed2k://\|file\|[^\r\n|]+\|\d+\|[A-Z0-9]+\|/)`, 1, 0),
			providerParser("url", `(?i)(https?://[^\s"'<>，。；、]+)`, 1, 0),
		},
		passwordPattern: regexp.MustCompile(`(?i)(?:密码|提取码|验证码|访问码|分享密码|密钥|pwd|password|code|share_pwd|pass_code)[=:：\s]*([A-Za-z0-9]{1,4})`),
	}
}

func providerParser(typ string, pattern string, urlGroup int, passGroup int) Parser {
	return regexParser{
		typ:       typ,
		pattern:   regexp.MustCompile(pattern),
		urlGroup:  urlGroup,
		passGroup: passGroup,
	}
}

func (p regexParser) Extract(text string) []Candidate {
	matches := p.pattern.FindAllStringSubmatchIndex(text, -1)
	out := make([]Candidate, 0, len(matches))
	for _, match := range matches {
		urlStart, urlEnd, ok := capture(match, p.urlGroup)
		if !ok {
			continue
		}
		candidate := Candidate{
			Type:       p.typ,
			URL:        text[urlStart:urlEnd],
			MatchStart: urlStart,
			MatchEnd:   urlEnd,
		}
		if passStart, passEnd, ok := capture(match, p.passGroup); ok {
			candidate.Password = text[passStart:passEnd]
		}
		out = append(out, candidate)
	}
	return out
}

func capture(match []int, group int) (int, int, bool) {
	if group <= 0 {
		return 0, 0, false
	}
	idx := group * 2
	if idx+1 >= len(match) || match[idx] < 0 || match[idx+1] < 0 {
		return 0, 0, false
	}
	return match[idx], match[idx+1], true
}

func (e *Extractor) Extract(text string) []model.Link {
	if e == nil {
		e = NewExtractor()
	}
	messageMetadata := extractMediaMetadata(text)
	candidates := make([]Candidate, 0)
	for _, parser := range e.parsers {
		candidates = append(candidates, parser.Extract(text)...)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].MatchStart < candidates[j].MatchStart
	})

	seen := map[string]struct{}{}
	var providerURLs []string
	var providerSpans []matchSpan
	var out []model.Link
	for _, candidate := range candidates {
		url := cleanLinkURL(candidate.Type, strings.TrimSpace(candidate.URL))
		if url == "" {
			continue
		}
		if candidate.Type != "url" && overlapsSpan(candidate.MatchStart, candidate.MatchEnd, providerSpans) {
			continue
		}
		if candidate.Type == "url" && overlapsProvider(url, providerURLs) {
			continue
		}
		if _, ok := seen[url]; ok {
			continue
		}
		password := candidate.Password
		if password == "" {
			password = queryPassword(candidate.Type, url)
		}
		if password == "" {
			password = e.nearbyPassword(text, candidate.MatchEnd)
		}
		note := inferNote(text, candidate.MatchStart)
		metadata := messageMetadata
		metadata.overlay(mediaMetadataNearLink(text, candidate.MatchStart))
		metadata.overlay(mediaMetadataFromSameLinePrefix(text, candidate.MatchStart))
		metadata.merge(mediaMetadataFromURL(candidate.Type, url))
		if metadata.Title == "" {
			metadata.Title = note
		}
		if note == "" || isLowConfidenceNote(note) || (metadata.Title != "" && isProseNote(note) && !noteMatchesMediaTitle(note, metadata.Title)) {
			note = metadata.Title
		}
		seen[url] = struct{}{}
		if candidate.Type != "url" {
			providerURLs = append(providerURLs, url)
			providerSpans = append(providerSpans, matchSpan{start: candidate.MatchStart, end: candidate.MatchEnd})
		}
		out = append(out, model.Link{
			Type:          candidate.Type,
			URL:           url,
			Password:      password,
			Note:          note,
			SourceSnippet: sourceSnippet(text, candidate.MatchStart, candidate.MatchEnd),
			Category:      resourceCategory(candidate.Type),
			MediaTitle:    metadata.Title,
			MediaYear:     metadata.Year,
			MediaSeason:   metadata.Season,
			MediaEpisode:  metadata.Episode,
			MediaQuality:  metadata.Quality,
			MediaSize:     metadata.Size,
			MediaTMDBID:   metadata.TMDBID,
			MediaCategory: metadata.Category,
			MediaTags:     metadata.Tags,
		})
	}
	return out
}

func (m *mediaMetadata) merge(other mediaMetadata) {
	if m.Title == "" {
		m.Title = other.Title
	}
	if m.Year == "" {
		m.Year = other.Year
	}
	if m.Season == "" {
		m.Season = other.Season
	}
	if m.Episode == "" {
		m.Episode = other.Episode
	}
	if m.Quality == "" {
		m.Quality = other.Quality
	}
	if m.Size == "" {
		m.Size = other.Size
	}
	if m.TMDBID == "" {
		m.TMDBID = other.TMDBID
	}
	if m.Category == "" {
		m.Category = other.Category
	}
	if m.Tags == "" {
		m.Tags = other.Tags
	}
}

func (m *mediaMetadata) overlay(other mediaMetadata) {
	if other.Title != "" {
		m.Title = other.Title
	}
	if other.Year != "" {
		m.Year = other.Year
	}
	if other.Season != "" {
		m.Season = other.Season
	}
	if other.Episode != "" {
		m.Episode = other.Episode
	}
	if other.Quality != "" {
		m.Quality = other.Quality
	}
	if other.Size != "" {
		m.Size = other.Size
	}
	if other.TMDBID != "" {
		m.TMDBID = other.TMDBID
	}
	if other.Category != "" {
		m.Category = other.Category
	}
	if other.Tags != "" {
		m.Tags = other.Tags
	}
}

type matchSpan struct {
	start int
	end   int
}

func overlapsSpan(start int, end int, spans []matchSpan) bool {
	for _, item := range spans {
		if start < item.end && item.start < end {
			return true
		}
	}
	return false
}

func overlapsProvider(url string, providers []string) bool {
	for _, providerURL := range providers {
		if url == providerURL || strings.HasPrefix(url, providerURL) || strings.HasPrefix(providerURL, url) {
			return true
		}
	}
	return false
}

func resourceCategory(typ string) string {
	switch typ {
	case "magnet":
		return "magnet"
	case "ed2k":
		return "ed2k"
	case "url":
		return "http"
	default:
		return "cloud_drive"
	}
}

func sourceSnippet(text string, start int, end int) string {
	if start < 0 || start > len(text) {
		return ""
	}
	if end < start {
		end = start
	}
	lineStart := strings.LastIndex(text[:start], "\n") + 1
	lineEndRel := strings.Index(text[end:], "\n")
	lineEnd := len(text)
	if lineEndRel >= 0 {
		lineEnd = end + lineEndRel
	}
	snippet := strings.TrimSpace(text[lineStart:lineEnd])
	const maxSnippet = 240
	if utf8.RuneCountInString(snippet) <= maxSnippet {
		return snippet
	}
	runes := []rune(snippet)
	return string(runes[:maxSnippet])
}

func trimTrailingPunctuation(raw string) string {
	return strings.TrimRight(raw, ".,;:!?)]}）】》\"'，#")
}

func cleanLinkURL(typ string, raw string) string {
	raw = trimTrailingPunctuation(strings.TrimSpace(raw))
	raw = trimAtTrailingMarkers(raw, typ)
	switch typ {
	case "baidu":
		if strings.Contains(raw, "pan.baidu.com/share/init?") || strings.Contains(raw, "pan.baidu.com/wap/init?") {
			raw = cleanQueryCodeValue(raw, "pwd")
		}
	case "pikpak":
		raw = keepQueryCodeOnly(raw, "pwd")
	case "123":
		raw = cleanQueryCodeValue(raw, "pwd")
	case "115":
		raw = cleanQueryCodeValue(raw, "password")
	case "xunlei":
		raw = cleanQueryCodeValue(raw, "pwd")
	case "aliyun":
		raw = cleanQueryCodeValue(raw, "password")
	case "uc":
		raw = cleanQueryCodeValue(raw, "password")
	}
	return trimTrailingPunctuation(raw)
}

func trimAtTrailingMarkers(raw string, typ string) string {
	if raw == "" {
		return raw
	}
	markers := []string{"标签", "🏷", "📁", "📎", "🔗", "🔑", "访问码", "提取码", "密码"}
	if typ == "tianyi" {
		markers = []string{"标签", "🏷", "📁", "📎", "🔗", "🔑"}
	}
	min := len(raw)
	for _, marker := range markers {
		if idx := strings.Index(raw, marker); idx > 0 && idx < min {
			if marker == "提取码" && strings.Contains(raw[:idx], "?") {
				continue
			}
			min = idx
		}
	}
	if min < len(raw) {
		raw = raw[:min]
	}
	return strings.TrimSpace(raw)
}

func cleanQueryCodeValue(raw string, key string) string {
	for _, prefix := range []string{"?" + key + "=", "&" + key + "="} {
		idx := strings.Index(strings.ToLower(raw), strings.ToLower(prefix))
		if idx < 0 {
			continue
		}
		valueStart := idx + len(prefix)
		valueEnd := valueStart
		for valueEnd < len(raw) {
			r, size := utf8.DecodeRuneInString(raw[valueEnd:])
			if !isCodeRune(r) {
				break
			}
			valueEnd += size
		}
		return raw[:valueEnd]
	}
	return raw
}

func keepQueryCodeOnly(raw string, key string) string {
	code := queryPassword("", raw)
	queryIdx := strings.Index(raw, "?")
	if queryIdx < 0 {
		return raw
	}
	if code == "" {
		return raw[:queryIdx]
	}
	return raw[:queryIdx] + "?" + key + "=" + code
}

func isCodeRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

func queryPassword(typ string, raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	keys := []string{"pwd", "password", "share_pwd", "pass_code"}
	if typ != "tianyi" {
		keys = append(keys, "code")
	}
	values := parsed.Query()
	for _, key := range keys {
		if value := values.Get(key); value != "" {
			return cleanCodeValue(value)
		}
	}
	return ""
}

func cleanCodeValue(value string) string {
	value = strings.TrimSpace(value)
	end := 0
	for end < len(value) {
		r, size := utf8.DecodeRuneInString(value[end:])
		if !isCodeRune(r) {
			break
		}
		end += size
	}
	if end == 0 {
		return ""
	}
	return value[:end]
}

func (e *Extractor) nearbyPassword(text string, after int) string {
	end := after + 80
	if end > len(text) {
		end = len(text)
	}
	segment := text[after:end]
	if nextURL := nextURLIndex(segment); nextURL >= 0 {
		segment = segment[:nextURL]
	}
	m := e.passwordPattern.FindStringSubmatch(segment)
	if len(m) != 2 {
		return ""
	}
	return m[1]
}

func nextURLIndex(segment string) int {
	lower := strings.ToLower(segment)
	indexes := []int{}
	for _, marker := range []string{"http://", "https://", "magnet:?", "ed2k://"} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			indexes = append(indexes, idx)
		}
	}
	if len(indexes) == 0 {
		return -1
	}
	sort.Ints(indexes)
	return indexes[0]
}

func inferNote(text string, linkStart int) string {
	if linkStart < 0 || linkStart > len(text) {
		return ""
	}
	lineStart := strings.LastIndex(text[:linkStart], "\n") + 1
	currentPrefix := text[lineStart:linkStart]
	if note := cleanNoteCandidate(currentPrefix); note != "" {
		return note
	}

	prevEnd := lineStart
	for prevEnd > 0 {
		prevStart := strings.LastIndex(text[:prevEnd-1], "\n") + 1
		line := text[prevStart : prevEnd-1]
		prevEnd = prevStart
		if strings.TrimSpace(line) == "" {
			continue
		}
		if note := cleanNoteCandidate(line); note != "" {
			return note
		}
	}
	return ""
}

func cleanNoteCandidate(raw string) string {
	candidate := strings.TrimSpace(raw)
	candidate = strings.TrimRight(candidate, ":：-—| \t")
	candidate = strings.TrimSpace(candidate)
	candidate = stripLeadingSymbols(candidate)
	for _, prefix := range []string{"资源名称", "名称", "标题", "片名", "电影", "电视剧", "剧集", "动漫", "动画", "综艺"} {
		switch {
		case strings.HasPrefix(candidate, prefix+"："):
			candidate = strings.TrimSpace(candidate[len(prefix)+len("："):])
		case strings.HasPrefix(candidate, prefix+":"):
			candidate = strings.TrimSpace(candidate[len(prefix)+len(":"):])
			break
		}
	}
	candidate = strings.TrimRight(candidate, ":：-—| \t")
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || isLinkLabel(candidate) || isMetadataLine(candidate) || isStandaloneDateLine(candidate) {
		return ""
	}
	return candidate
}

func stripLeadingSymbols(value string) string {
	for value != "" {
		r, size := utf8.DecodeRuneInString(value)
		if r == ' ' || r == '\t' || r == '-' || r == '*' || r == '>' || r == ':' || r == '：' || r == '|' {
			value = value[size:]
			continue
		}
		if isSymbolOrPunctuation(r) || unicode.IsMark(r) {
			value = value[size:]
			continue
		}
		break
	}
	return strings.TrimSpace(value)
}

func isSymbolOrPunctuation(r rune) bool {
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

func isLinkLabel(value string) bool {
	normalized := strings.ToLower(strings.NewReplacer(" ", "", "\t", "", "：", "", ":", "", "-", "", "_", "", "网盘", "", "云盘", "").Replace(value))
	labels := map[string]struct{}{
		"":       {},
		"链接":     {},
		"直达链接":   {},
		"地址":     {},
		"资源":     {},
		"资源地址":   {},
		"下载":     {},
		"网盘地址":   {},
		"夸克":     {},
		"quark":  {},
		"百度":     {},
		"baidu":  {},
		"度盘":     {},
		"阿里":     {},
		"aliyun": {},
		"alipan": {},
		"uc":     {},
		"迅雷":     {},
		"xunlei": {},
		"115":    {},
		"123":    {},
		"123盘":   {},
		"123盘地址": {},
		"天翼":     {},
		"mobile": {},
		"pikpak": {},
		"磁力":     {},
		"ed2k":   {},
	}
	_, ok := labels[normalized]
	return ok
}

func isMetadataLine(value string) bool {
	head := value
	for _, separator := range []string{"：", ":"} {
		if idx := strings.Index(head, separator); idx >= 0 {
			head = head[:idx]
			break
		}
	}
	normalized := strings.ToLower(strings.NewReplacer(" ", "", "\t", "", "_", "", "-", "").Replace(strings.TrimSpace(head)))
	metadataLabels := map[string]struct{}{
		"tmdbid": {},
		"id":     {},
		"评分":     {},
		"类型":     {},
		"分类":     {},
		"质量":     {},
		"画质":     {},
		"视频":     {},
		"文件":     {},
		"大小":     {},
		"描述":     {},
		"剧情":     {},
		"状态":     {},
		"季数":     {},
		"地区":     {},
		"平台":     {},
		"主演":     {},
		"简介":     {},
		"介绍":     {},
		"字幕":     {},
		"分享":     {},
		"来自":     {},
		"频道":     {},
		"群组":     {},
		"投稿":     {},
		"提取码":    {},
		"访问码":    {},
		"标签":     {},
		"搜索结果":   {},
		"网盘":     {},
		"云盘":     {},
		"网盘名称":   {},
	}
	_, ok := metadataLabels[normalized]
	return ok
}

func isLowConfidenceNote(note string) bool {
	if note == "" {
		return true
	}
	if isMetadataLine(note) || isLinkLabel(note) {
		return true
	}
	if isStandaloneDateLine(note) {
		return true
	}
	if isCatalogItemLine(note) {
		return true
	}
	normalized := strings.ToLower(strings.TrimSpace(note))
	if strings.Contains(normalized, "://") || strings.Contains(normalized, "magnet:?") {
		return true
	}
	if strings.HasPrefix(normalized, "链接") || strings.HasPrefix(normalized, "直达链接") {
		return true
	}
	if strings.HasSuffix(normalized, "(") || strings.HasSuffix(normalized, "（") {
		return true
	}
	// Synopsis/plot text leaked from a 简介 block, or a raw bracketed title
	// line grabbed verbatim — neither is a usable link note; fall back to the
	// cleaned media title instead.
	if strings.ContainsAny(normalized, "。！？!?") {
		return true
	}
	return strings.HasPrefix(normalized, "【") || strings.HasPrefix(normalized, "[")
}

func noteMatchesMediaTitle(note string, mediaTitle string) bool {
	if note == "" || mediaTitle == "" {
		return false
	}
	noteTitle, _ := titleFromExplicitLine(note)
	if noteTitle == "" {
		noteTitle, _ = titleFromPlainLine(note)
	}
	if noteTitle == "" {
		return false
	}
	normalizedNote := normalizeMediaTitle(noteTitle)
	normalizedTitle := normalizeMediaTitle(mediaTitle)
	if normalizedNote == normalizedTitle {
		return true
	}
	return strings.Contains(normalizedNote, normalizedTitle)
}

func isProseNote(note string) bool {
	trimmed := strings.TrimSpace(note)
	if utf8.RuneCountInString(trimmed) > 40 && strings.ContainsAny(trimmed, "。！？；!?;") {
		return true
	}
	return utf8.RuneCountInString(trimmed) > 60 && strings.Contains(trimmed, "，")
}

func extractMediaMetadata(text string) mediaMetadata {
	var metadata mediaMetadata
	lines := strings.Split(text, "\n")
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		clean := cleanMediaLine(line)
		if clean == "" {
			continue
		}
		if isStandaloneDateLine(clean) {
			continue
		}
		hasResourceURL := isResourceURLLine(clean)
		if metadata.TMDBID == "" {
			metadata.TMDBID = extractFirstMatch(clean, `(?i)(?:TMDB(?:\s*ID)?|tmdb)[：:\s-]*(\d+)`)
			if metadata.TMDBID == "" {
				metadata.TMDBID = extractFirstMatch(clean, `(?i)\{tmdb-(\d+)\}`)
			}
		}
		if metadata.Size == "" {
			metadata.Size = extractFirstMatch(clean, `(?i)(?:大小|文件大小|体积|总大小)[：:\s]*([0-9]+(?:\.[0-9]+)?\s*(?:KB|MB|GB|TB|G|T))\b`)
		}
		lineQuality := extractLabeledValue(clean, []string{"质量", "视频质量", "画质", "视频"})
		if lineQuality != "" {
			metadata.Quality = appendMetadataValue(metadata.Quality, lineQuality)
		}
		if category := extractLabeledValue(clean, []string{"分类", "类型", "题材"}); category != "" {
			metadata.Category = category
		}
		if metadata.Tags == "" {
			metadata.Tags = extractTags(clean)
		}
		if metadata.Size == "" {
			metadata.Size = sizeFromTags(metadata.Tags)
		}
		if hasResourceURL {
			continue
		}
		if isCatalogItemLine(clean) {
			continue
		}
		if metadata.Category == "" {
			metadata.Category = categoryFromLine(clean)
		}
		if metadata.Title == "" {
			title, category := titleFromMarkerLine(line, clean)
			if title == "" {
				title, category = titleFromExplicitLine(clean)
			}
			if title == "" {
				title, category = titleFromPlainLine(clean)
			}
			if title != "" {
				metadata.Title = title
				if metadata.Category == "" {
					metadata.Category = category
				}
			}
		}
		metadata.merge(sequenceMetadata(clean))
		if metadata.Year == "" {
			metadata.Year = extractYear(clean)
		}
		if lineQuality == "" {
			metadata.Quality = appendMetadataValue(metadata.Quality, qualityFromLine(clean))
		}
	}
	if metadata.Title != "" {
		if metadata.Year == "" {
			metadata.Year = extractYear(metadata.Title)
		}
		metadata.merge(sequenceMetadata(metadata.Title))
		metadata.Title = normalizeMediaTitle(metadata.Title)
	}
	if metadata.Size == "" {
		metadata.Size = sizeFromTags(metadata.Tags)
	}
	metadata.Quality = appendMetadataValue(metadata.Quality, qualityFromLine(metadata.Tags))
	return metadata
}

func mediaMetadataNearLink(text string, linkStart int) mediaMetadata {
	if linkStart < 0 || linkStart > len(text) {
		return mediaMetadata{}
	}
	lineStart := strings.LastIndex(text[:linkStart], "\n") + 1
	prevEnd := lineStart
	for inspected := 0; prevEnd > 0 && inspected < 4; inspected++ {
		prevStart := strings.LastIndex(text[:prevEnd-1], "\n") + 1
		line := strings.TrimSpace(text[prevStart : prevEnd-1])
		prevEnd = prevStart
		if line == "" {
			continue
		}
		clean := cleanMediaLine(line)
		if clean == "" || isResourceURLLine(clean) || isLinkLabel(clean) || isCatalogItemLine(clean) {
			continue
		}
		if isMetadataLine(clean) {
			break
		}
		if metadata := mediaMetadataFromNearbyTitleLine(clean); metadata.Title != "" {
			return metadata
		}
	}
	return mediaMetadata{}
}

func mediaMetadataFromSameLinePrefix(text string, linkStart int) mediaMetadata {
	if linkStart < 0 || linkStart > len(text) {
		return mediaMetadata{}
	}
	lineStart := strings.LastIndex(text[:linkStart], "\n") + 1
	prefix := strings.TrimSpace(text[lineStart:linkStart])
	prefix = strings.TrimRight(prefix, " \t(（")
	if prefix == "" {
		return mediaMetadata{}
	}
	if idx := firstLabelSeparator(prefix); idx >= 0 {
		head := strings.TrimSpace(prefix[:idx])
		tail := strings.TrimSpace(prefix[idx+separatorLen(prefix[idx:]):])
		switch {
		case isLinkLabel(head):
			prefix = tail
		case tail == "":
			// "<label>：" immediately followed by the URL is a provider or channel
			// label (e.g. 光鸭, 夸克), not a media title. It is already captured as
			// the link note; do not let it overwrite the message header title.
			return mediaMetadata{}
		}
	}
	prefix = cleanNoteCandidate(prefix)
	prefix = strings.TrimRight(prefix, " \t(（")
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || isLowConfidenceNote(prefix) {
		return mediaMetadata{}
	}
	return mediaMetadataFromTitleLine(prefix)
}

func firstLabelSeparator(value string) int {
	first := -1
	for _, separator := range []string{"：", ":"} {
		if idx := strings.Index(value, separator); idx >= 0 && (first < 0 || idx < first) {
			first = idx
		}
	}
	return first
}

func separatorLen(value string) int {
	if strings.HasPrefix(value, "：") {
		return len("：")
	}
	if strings.HasPrefix(value, ":") {
		return len(":")
	}
	return 0
}

func mediaMetadataFromTitleLine(line string) mediaMetadata {
	var metadata mediaMetadata
	title, category := titleFromExplicitLine(line)
	if title == "" {
		title, category = titleFromPlainLine(line)
	}
	if title == "" {
		return metadata
	}
	metadata.Title = title
	metadata.Year = extractYear(line)
	metadata.TMDBID = extractFirstMatch(line, `(?i)\{tmdb-(\d+)\}`)
	metadata.Category = firstNonEmptyString(category, categoryFromLine(line))
	metadata.Quality = qualityFromLine(line)
	metadata.merge(sequenceMetadata(line))
	return metadata
}

func mediaMetadataFromNearbyTitleLine(line string) mediaMetadata {
	var metadata mediaMetadata
	title, category := titleFromExplicitLine(line)
	if title == "" && regexp.MustCompile(`(?i)\{tmdb-\d+\}`).MatchString(line) {
		title, category = titleFromPlainLine(line)
	}
	if title == "" {
		return metadata
	}
	metadata.Title = title
	metadata.Year = extractYear(line)
	metadata.TMDBID = extractFirstMatch(line, `(?i)\{tmdb-(\d+)\}`)
	metadata.Category = firstNonEmptyString(category, categoryFromLine(line))
	metadata.Quality = qualityFromLine(line)
	metadata.merge(sequenceMetadata(line))
	return metadata
}

func cleanMediaLine(line string) string {
	line = strings.TrimSpace(line)
	for line != "" {
		r, size := utf8.DecodeRuneInString(line)
		if r == '#' {
			break
		}
		if r == ' ' || r == '\t' || r == '-' || r == '*' || r == '>' || r == '|' || r == ':' || r == '：' || unicode.IsSymbol(r) || unicode.IsMark(r) {
			line = strings.TrimSpace(line[size:])
			continue
		}
		break
	}
	return strings.TrimSpace(line)
}

func isResourceURLLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "magnet:?") || strings.Contains(lower, "ed2k://")
}

func isCatalogItemLine(line string) bool {
	return regexp.MustCompile(`^\d+\s*[.、．]\s*.+`).MatchString(strings.TrimSpace(line))
}

// titleMarkerEmojis flag a line as the resource title (🎬, 📺, 🗄 header, …).
// Unlike 💾 (网盘) or 📝 (简介), these mark the media name itself.
var titleMarkerEmojis = map[rune]struct{}{
	'🎬': {}, // clapper board
	'🎥': {}, // movie camera
	'📺': {}, // television
	'🗄': {}, // file cabinet (channel header)
	'🎞': {}, // film frames
	'📀': {}, // DVD
	'📼': {}, // videocassette
	'🎮': {}, // video game
}

// titleFromMarkerLine resolves a title when markerLine begins with a title
// marker emoji. The emoji is detected on the raw line (cleanMediaLine strips
// it); the title is parsed from cleanLine so existing 《…》, "类型：名" and
// short-line behavior is unchanged. The length cap is bypassed (a marker is
// an explicit title signal, so a long release filename is accepted), but all
// other content guards still apply — a decorative marker like "📺 影片更新通知~"
// is rejected and the real title is found on a later line.
func titleFromMarkerLine(markerLine, cleanLine string) (string, string) {
	trimmed := strings.TrimSpace(markerLine)
	if trimmed == "" {
		return "", ""
	}
	r, _ := utf8.DecodeRuneInString(trimmed)
	if _, ok := titleMarkerEmojis[r]; !ok {
		return "", ""
	}
	if title, category := titleFromExplicitLine(cleanLine); title != "" {
		return title, category
	}
	if title, category := titleFromPlainLineMaxLength(cleanLine, -1); title != "" {
		// Reduce bracketed release tags to just the media name. The name can
		// either precede the brackets ("神之水滴[全8集]…") or be wrapped inside
		// the leading bracket ("·✅【我的妈耶 (2026)】…"), so titleFromBracketedLine
		// handles both.
		if name := titleFromBracketedLine(cleanLine); name != "" {
			return name, category
		}
		return title, category
	}
	return "", ""
}

// titleFromBracketedLine reduces a marker title line to its media name by
// dropping bracketed tag groups. If readable text precedes the first bracket
// it is the name ("神之水滴[全8集][简繁英字幕]…" → "神之水滴"); otherwise the name
// is taken from inside the leading bracket ("·✅【我的妈耶 (2026)】【4K高码率】…"
// → "我的妈耶"). Returns "" when no bracket is present.
func titleFromBracketedLine(line string) string {
	idx := strings.IndexAny(line, "[【")
	if idx < 0 {
		return ""
	}
	before := strings.TrimSpace(line[:idx])
	if hasMediaText(before) {
		return normalizeMediaTitle(before)
	}
	inner := firstBracketInner(line[idx:])
	if inner == "" {
		return ""
	}
	return normalizeMediaTitle(inner)
}

// hasMediaText reports whether s contains a letter or digit (i.e. real title
// content rather than only symbols/punctuation like "·✅").
func hasMediaText(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// firstBracketInner returns the content inside the leading [] or 【】 group.
func firstBracketInner(s string) string {
	switch {
	case strings.HasPrefix(s, "【"):
		if end := strings.Index(s, "】"); end > len("【") {
			return strings.TrimSpace(s[len("【"):end])
		}
	case strings.HasPrefix(s, "["):
		if end := strings.Index(s, "]"); end > 1 {
			return strings.TrimSpace(s[1:end])
		}
	}
	return ""
}

func titleFromExplicitLine(line string) (string, string) {
	category := ""
	if match := regexp.MustCompile(`^《([^》]+)》\s*(.+)$`).FindStringSubmatch(line); len(match) == 3 {
		head := strings.TrimSpace(match[1])
		if isMediaCategoryLabel(head) {
			category = head
			return normalizeMediaTitle(match[2]), category
		}
		return normalizeMediaTitle(head), category
	}
	if match := regexp.MustCompile(`^\[([^\]]+)\]\s*(.+)$`).FindStringSubmatch(line); len(match) == 3 {
		category = strings.TrimSpace(match[1])
		return normalizeMediaTitle(match[2]), category
	}
	if match := regexp.MustCompile(`^(最新擦边短剧|资源名称|电视剧名|名称|标题|片名|电影|电视剧|剧集|动漫|动画|综艺|短剧|已更新)\s*[：:]\s*(.+)$`).FindStringSubmatch(line); len(match) == 3 {
		if match[1] != "最新擦边短剧" && match[1] != "资源名称" && match[1] != "电视剧名" && match[1] != "名称" && match[1] != "标题" && match[1] != "片名" && match[1] != "已更新" {
			category = match[1]
		}
		return normalizeMediaTitle(match[2]), category
	}
	if match := regexp.MustCompile(`^(电影|电视剧|剧集|动漫|动画|综艺|短剧)\s+(.+)$`).FindStringSubmatch(line); len(match) == 3 {
		return normalizeMediaTitle(match[2]), match[1]
	}
	if match := regexp.MustCompile(`^(短剧)[-—]\s*(.+)$`).FindStringSubmatch(line); len(match) == 3 {
		return normalizeMediaTitle(match[2]), match[1]
	}
	return "", ""
}

func isMediaCategoryLabel(value string) bool {
	switch strings.TrimSpace(value) {
	case "电影", "电视剧", "剧集", "国产剧", "国剧", "港剧", "台剧", "日剧", "韩剧", "美剧", "英剧", "动漫", "动画", "综艺", "短剧", "纪录片":
		return true
	default:
		return false
	}
}

func titleFromPlainLine(line string) (string, string) {
	return titleFromPlainLineMaxLength(line, 80)
}

// titleFromPlainLineMaxLength is titleFromPlainLine with an optional length
// cap. A maxLen <= 0 disables the cap so an explicit title marker emoji can
// still accept a long release filename while keeping every other guard.
func titleFromPlainLineMaxLength(line string, maxLen int) (string, string) {
	lower := strings.ToLower(line)
	if strings.Contains(line, "://") || strings.Contains(lower, "magnet:?") || isLinkLabel(line) || isMetadataLine(line) {
		return "", ""
	}
	if isAnnouncementLine(line) {
		return "", ""
	}
	if isStandaloneDateLine(line) {
		return "", ""
	}
	if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "@") {
		return "", ""
	}
	if strings.Contains(line, "更新通知") {
		return "", ""
	}
	if regexp.MustCompile(`(?i)^(?:\d{3,4}p|4K|8K|WEB|WEB[- ]?DL|NF|Netflix|DV|HDR|SDR)\b`).MatchString(line) {
		return "", ""
	}
	if maxLen > 0 && utf8.RuneCountInString(line) > maxLen {
		return "", ""
	}
	normalized := normalizeMediaTitle(line)
	if normalized == "" || isLinkLabel(normalized) || isMetadataLine(normalized) {
		return "", ""
	}
	if !looksLikeMediaTitle(line) {
		return "", ""
	}
	return normalized, ""
}

func isAnnouncementLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if strings.Contains(trimmed, "@") {
		return true
	}
	if strings.Contains(trimmed, "反馈命令") || strings.Contains(trimmed, "使用 /") {
		return true
	}
	if strings.Contains(trimmed, "频道") && (strings.Contains(trimmed, "地址") || strings.Contains(strings.ToLower(trimmed), "channel")) {
		return true
	}
	return false
}

func isStandaloneDateLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return regexp.MustCompile(`^(?:(?:19|20)\d{2}\s*年\s*)?\d{1,2}\s*月\s*\d{1,2}\s*(?:日|号)?$`).MatchString(trimmed)
}

func categoryFromLine(line string) string {
	for _, category := range []string{"短剧", "综艺", "电视剧", "剧集", "电影", "动漫", "动画"} {
		if strings.Contains(line, category) {
			return category
		}
	}
	return ""
}

func looksLikeMediaTitle(line string) bool {
	if regexp.MustCompile(`(?:19|20)\d{2}|S\d{1,2}E?\d*|第[一二三四五六七八九十\d]+季|第\s*\d+\s*集|更新\s*\d+|\d+\s*集`).MatchString(line) {
		return true
	}
	for _, r := range line {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func normalizeMediaTitle(raw string) string {
	title := strings.TrimSpace(raw)
	title = strings.ReplaceAll(title, "｜", "|")
	if idx := strings.Index(title, "|"); idx >= 0 {
		title = strings.TrimSpace(title[:idx])
	}
	if idx := regexp.MustCompile(`（\s*\d+\s*集\s*）|\(\s*\d+\s*集\s*\)`).FindStringIndex(title); idx != nil {
		title = title[:idx[0]]
	}
	title = regexp.MustCompile(`（完整版）|\(完整版\)|\[完整版\]|【完整版】`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`\s+\d+\s*集完\s*$`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`(?i)\{tmdb-\d+\}`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`(?i)\bTMDB(?:\s*ID)?[：:\s-]*\d+`).ReplaceAllString(title, "")
	if idx := regexp.MustCompile(`\s+[-—]\s+S\d{1,2}E\d{1,4}\b`).FindStringIndex(title); idx != nil {
		title = title[:idx[0]]
	}
	if match := regexp.MustCompile(`^(.*?)(?:\s*[（(](?:19|20)\d{2}[）)]).*$`).FindStringSubmatch(title); len(match) == 2 {
		title = match[1]
	}
	if idx := regexp.MustCompile(`\s+(?:19|20)\d{2}\b`).FindStringIndex(title); idx != nil {
		title = title[:idx[0]]
	}
	if idx := regexp.MustCompile(`(?i)\s+(?:WEB[- ]?(?:DL|4K)?|4K|8K|2160p|1080p|720p|BDISO|BluRay|REMUX|UHD|HDR10?\+?|DV|SDR|DDP|DTS|HEVC|H\.?26[45]|高码率|杜比全景声|内封|完结|更新至?\s*\d+|更至?\s*\d+|第\s*\d+\s*集|第\s*\d+\s*期)\b`).FindStringIndex(title); idx != nil {
		title = title[:idx[0]]
	}
	title = regexp.MustCompile(`\s*擦边短剧\s*$`).ReplaceAllString(title, "")
	title = regexp.MustCompile(`\s+`).ReplaceAllString(title, " ")
	return strings.Trim(title, " \t:：-—,，")
}

func sequenceMetadata(line string) mediaMetadata {
	var metadata mediaMetadata
	if match := regexp.MustCompile(`(?i)\bS(\d{1,2})(?:E(\d{1,4}))?\b`).FindStringSubmatch(line); len(match) >= 2 {
		metadata.Season = "S" + zeroPad(match[1], 2)
		if len(match) >= 3 && match[2] != "" {
			metadata.Episode = "E" + zeroPad(match[2], 2)
		}
	}
	if metadata.Season == "" {
		if season := extractFirstMatch(line, `第([一二三四五六七八九十\d]+)季`); season != "" {
			metadata.Season = "第" + season + "季"
		}
	}
	if metadata.Episode == "" {
		if episode := extractFirstMatch(line, `补\s*E(\d+)`); episode != "" {
			metadata.Episode = "补E" + zeroPad(episode, 2)
		} else if episode := extractFirstMatch(line, `(\d{4})\s*期`); episode != "" {
			metadata.Episode = episode + "期"
		} else if episode := extractFirstMatch(line, `(?:更新至|更至|更新|更)\s*第?\s*(\d+)\s*集`); episode != "" {
			metadata.Episode = "更新" + episode + "集"
		} else if episode := extractFirstMatch(line, `第\s*(\d+)\s*集`); episode != "" {
			metadata.Episode = "E" + zeroPad(episode, 2)
		} else if episode := extractFirstMatch(line, `(?:全|共)?\s*(\d+)\s*集`); episode != "" {
			metadata.Episode = episode + "集"
		}
	}
	return metadata
}

func zeroPad(value string, width int) string {
	for len(value) < width {
		value = "0" + value
	}
	return value
}

func extractLabeledValue(line string, labels []string) string {
	for _, label := range labels {
		pattern := `^` + regexp.QuoteMeta(label) + `[：:]\s*(.+)$`
		if match := regexp.MustCompile(pattern).FindStringSubmatch(line); len(match) == 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func extractTags(line string) string {
	value := extractLabeledValue(line, []string{"标签", "文件类型"})
	if value == "" {
		if match := regexp.MustCompile(`^(?:标签|文件类型)\s+(.+)$`).FindStringSubmatch(line); len(match) == 2 {
			value = strings.TrimSpace(match[1])
		}
	}
	if value == "" && strings.HasPrefix(strings.TrimSpace(line), "#") {
		value = strings.TrimSpace(line)
	}
	if value == "" {
		return ""
	}
	if idx := strings.Index(value, "http://"); idx >= 0 {
		value = value[:idx]
	}
	if idx := strings.Index(value, "https://"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.ReplaceAll(value, "#", " ")
	value = regexp.MustCompile(`\s+`).ReplaceAllString(value, " ")
	return strings.TrimSpace(value)
}

func sizeFromTags(tags string) string {
	if tags == "" {
		return ""
	}
	return extractFirstMatch(tags, `(?i)\b([0-9]+(?:\.[0-9]+)?\s*(?:KB|MB|GB|TB|G|T))\b`)
}

func qualityFromLine(line string) string {
	tokens := regexp.MustCompile(`(?i)(?:\b(?:Netflix|NF|WEB[- ]?DL|WEB[- ]?4K|WEB|4K|8K|2160p|1080p|720p|BDISO|BluRay|REMUX|UHD|HDR(?:10\+?)?|DV|SDR|DDP(?:5\.?1|2\.0|\.2\.0)?|DTS-HD(?:\s+MA)?|HEVC|H\.?26[45]|AAC|50fps|60fps)\b|高码率|杜比全景声)`).FindAllString(line, -1)
	if len(tokens) == 0 {
		return ""
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		key := strings.ToLower(token)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, token)
	}
	return strings.Join(out, " ")
}

func appendMetadataValue(current string, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return current
	}
	if current == "" {
		return next
	}
	existing := map[string]struct{}{}
	for _, token := range strings.Fields(current) {
		existing[strings.ToLower(token)] = struct{}{}
	}
	out := current
	for _, token := range strings.Fields(next) {
		key := strings.ToLower(token)
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		out += " " + token
	}
	return out
}

func mediaMetadataFromURL(typ string, raw string) mediaMetadata {
	switch typ {
	case "ed2k":
		return mediaMetadataFromED2K(raw)
	case "magnet":
		return mediaMetadataFromMagnet(raw)
	default:
		return mediaMetadata{}
	}
}

func mediaMetadataFromED2K(raw string) mediaMetadata {
	parts := strings.Split(raw, "|")
	if len(parts) < 5 {
		return mediaMetadata{}
	}
	name, err := url.QueryUnescape(parts[2])
	if err != nil {
		name = parts[2]
	}
	title := stripFileExtension(name)
	metadata := mediaMetadata{
		Title:   normalizeMediaTitle(title),
		Quality: qualityFromLine(title),
	}
	metadata.merge(sequenceMetadata(title))
	if metadata.Year == "" {
		metadata.Year = extractYear(title)
	}
	if metadata.Size == "" && parts[3] != "" {
		metadata.Size = formatBytesString(parts[3])
	}
	return metadata
}

func mediaMetadataFromMagnet(raw string) mediaMetadata {
	parsed, err := url.Parse(raw)
	if err != nil {
		return mediaMetadata{}
	}
	title := parsed.Query().Get("dn")
	if title == "" {
		return mediaMetadata{}
	}
	metadata := mediaMetadata{
		Title:   normalizeMediaTitle(stripFileExtension(title)),
		Quality: qualityFromLine(title),
	}
	metadata.merge(sequenceMetadata(title))
	if metadata.Year == "" {
		metadata.Year = extractYear(title)
	}
	return metadata
}

func stripFileExtension(name string) string {
	idx := strings.LastIndex(name, ".")
	if idx <= 0 || idx == len(name)-1 {
		return name
	}
	ext := strings.ToLower(name[idx+1:])
	if regexp.MustCompile(`^[a-z0-9]{2,5}$`).MatchString(ext) {
		return name[:idx]
	}
	return name
}

func formatBytesString(raw string) string {
	var bytes uint64
	for _, r := range raw {
		if r < '0' || r > '9' {
			return ""
		}
		bytes = bytes*10 + uint64(r-'0')
	}
	if bytes == 0 {
		return ""
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(bytes)
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if unit == 0 {
		return raw + " B"
	}
	if value >= 10 {
		return fmt.Sprintf("%.0f %s", value, units[unit])
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", value), "0"), ".") + " " + units[unit]
}

func extractFirstMatch(value string, pattern string) string {
	match := regexp.MustCompile(pattern).FindStringSubmatch(value)
	if len(match) == 0 {
		return ""
	}
	if len(match) == 1 {
		return strings.TrimSpace(match[0])
	}
	return strings.TrimSpace(match[1])
}

func extractYear(value string) string {
	if year := extractFirstMatch(value, `(?i)年\s*代\s*((?:19|20)\d{2})`); year != "" {
		return year
	}
	return extractFirstMatch(value, `(?:19|20)\d{2}`)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
