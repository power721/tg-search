package linkcheck

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type HTTPChecker struct {
	client *http.Client
}

// defaultHTTPClient is a process-wide tuned client shared by all HTTPCheckers.
// High MaxIdleConnsPerHost plus HTTP/2 multiplexing lets hundreds of concurrent
// requests to the same pan API share very few TCP/TLS connections, which is the
// key to checking 300+ links within ~1s.
var (
	defaultHTTPClientOnce sync.Once
	defaultHTTPClient     *http.Client
)

func defaultClient() *http.Client {
	defaultHTTPClientOnce.Do(func() {
		dialer := &net.Dialer{
			Timeout:   3 * time.Second,
			KeepAlive: 30 * time.Second,
		}
		transport := &http.Transport{
			MaxIdleConns:          1000,
			MaxIdleConnsPerHost:   512,
			MaxConnsPerHost:       0, // unlimited: rely on HTTP/2 multiplexing + request ctx
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   3 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
		}
		// No client-level Timeout: each request is bounded by its context.
		defaultHTTPClient = &http.Client{Transport: transport}
	})
	return defaultHTTPClient
}

func NewHTTPChecker(client *http.Client) *HTTPChecker {
	if client == nil {
		client = defaultClient()
	}
	return &HTTPChecker{client: client}
}

func (c *HTTPChecker) Check(ctx context.Context, item Item) Result {
	switch item.DiskType {
	case "aliyun":
		return c.checkAliyun(ctx, item)
	case "quark":
		return c.checkQuark(ctx, item)
	case "baidu":
		return c.checkBaidu(ctx, item)
	case "tianyi":
		return c.checkTianyi(ctx, item)
	case "123":
		return c.check123(ctx, item)
	case "uc":
		return c.checkPageKeywords(ctx, item, []string{"失效", "不存在", "违规", "删除", "已过期", "被取消"}, []string{"提取码", "访问码", "请输入密码"}, []string{"文件", "分享", "drive.uc.cn"})
	case "xunlei":
		return c.checkXunlei(ctx, item)
	case "115":
		return c.check115(ctx, item)
	case "mobile":
		return c.checkMobile(ctx, item)
	default:
		return resultFor(item, StateUnsupported, "当前平台暂不支持检测")
	}
}

func (c *HTTPChecker) checkAliyun(ctx context.Context, item Item) Result {
	shareID := lastPathPart(item.URL)
	if shareID == "" {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	body, status, err := c.jsonRequest(ctx, http.MethodPost, "https://api.aliyundrive.com/adrive/v3/share_link/get_share_by_anonymous?share_id="+url.QueryEscape(shareID), map[string]string{"share_id": shareID}, map[string]string{
		"content-type": "application/json",
		"origin":       "https://www.alipan.com",
		"referer":      "https://www.alipan.com/",
		"x-canary":     "client=web,app=share,version=v2.3.1",
	})
	if err != nil {
		return requestFailure(ctx, item)
	}
	var parsed struct {
		ShareName   string `json:"share_name"`
		ShareTitle  string `json:"share_title"`
		Code        string `json:"code"`
		Message     string `json:"message"`
		FileCount   *int   `json:"file_count"`
		ShareStatus string `json:"share_status"`
	}
	_ = json.Unmarshal(body, &parsed)
	code := strings.ToLower(strings.TrimSpace(parsed.Code))
	if code != "" {
		if containsAny(code, "notfound", "cancelled", "canceled", "forbidden", "expired", "sharelink") {
			return resultFor(item, StateBad, firstNonEmpty(parsed.Message, parsed.Code, "链接失效"))
		}
		return resultFor(item, StateUncertain, firstNonEmpty(parsed.Message, parsed.Code, "无法确认链接状态"))
	}
	if parsed.FileCount != nil && *parsed.FileCount == 0 && parsed.ShareName == "" {
		return resultFor(item, StateBad, "分享内容为空")
	}
	statusText := strings.ToLower(strings.TrimSpace(parsed.ShareStatus))
	if containsAny(statusText, "forbidden", "cancel", "expired", "illegal", "invalid", "disabled") {
		return resultFor(item, StateBad, firstNonEmpty(parsed.Message, "链接失效"))
	}
	if status == http.StatusOK && (parsed.ShareName != "" || parsed.ShareTitle != "" || (parsed.FileCount != nil && *parsed.FileCount > 0)) {
		return resultFor(item, StateOK, "链接有效")
	}
	return resultFor(item, StateUncertain, firstNonEmpty(parsed.Message, fmt.Sprintf("HTTP状态码: %d", status)))
}

func (c *HTTPChecker) checkQuark(ctx context.Context, item Item) Result {
	shareID := regexpFirst(item.URL, `/s/([A-Za-z0-9]+)`)
	if shareID == "" {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	password := item.Password
	if password == "" {
		password = queryValue(item.URL, "pwd")
	}
	body, _, err := c.jsonRequest(ctx, http.MethodPost, "https://drive-h.quark.cn/1/clouddrive/share/sharepage/token", map[string]any{
		"pwd_id":                            shareID,
		"passcode":                          password,
		"support_visit_limit_private_share": true,
	}, map[string]string{
		"content-type": "application/json",
		"origin":       "https://pan.quark.cn",
		"referer":      "https://pan.quark.cn/",
	})
	if err != nil {
		return requestFailure(ctx, item)
	}
	var token struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Stoken string `json:"stoken"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &token)
	switch token.Code {
	case 0:
	case 41008:
		return resultFor(item, StateLocked, "需要提取码")
	case 41004, 41010, 41011:
		return resultFor(item, StateBad, "链接失效")
	default:
		return stateFromMessage(item, token.Message)
	}
	if token.Data.Stoken == "" {
		return resultFor(item, StateUncertain, "访问令牌缺失")
	}
	detailURL := fmt.Sprintf("https://drive-pc.quark.cn/1/clouddrive/share/sharepage/detail?pwd_id=%s&stoken=%s&ver=2&pr=ucpro", url.QueryEscape(shareID), url.QueryEscape(token.Data.Stoken))
	detailBody, _, err := c.request(ctx, http.MethodGet, detailURL, nil, map[string]string{
		"accept":  "application/json, text/plain, */*",
		"origin":  "https://pan.quark.cn",
		"referer": "https://pan.quark.cn/",
	})
	if err != nil {
		return requestFailure(ctx, item)
	}
	var detail struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			List     []any `json:"list"`
			IsExpire bool  `json:"is_expire"`
			Share    struct {
				Status           int  `json:"status"`
				PartialViolation bool `json:"partial_violation"`
			} `json:"share"`
		} `json:"data"`
	}
	_ = json.Unmarshal(detailBody, &detail)
	if detail.Code != 0 {
		return stateFromMessage(item, detail.Message)
	}
	if len(detail.Data.List) == 0 || detail.Data.IsExpire || detail.Data.Share.Status > 1 && detail.Data.Share.Status != 3 {
		return resultFor(item, StateBad, "链接失效")
	}
	if detail.Data.Share.PartialViolation {
		return resultFor(item, StateOK, "链接有效但部分文件违规")
	}
	return resultFor(item, StateOK, "链接有效")
}

func (c *HTTPChecker) checkBaidu(ctx context.Context, item Item) Result {
	parsed, err := url.Parse(item.URL)
	if err != nil {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	shortURL := strings.TrimPrefix(strings.TrimPrefix(parsed.Path, "/s/"), "1")
	if strings.HasPrefix(parsed.Path, "/share/init") {
		shortURL = strings.TrimPrefix(parsed.Query().Get("surl"), "1")
	}
	if shortURL == "" {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	password := firstNonEmpty(item.Password, parsed.Query().Get("pwd"))
	headers := map[string]string{"referer": item.URL}
	if password != "" {
		verifyURL := fmt.Sprintf("https://pan.baidu.com/share/verify?surl=%s&pwd=%s", url.QueryEscape(shortURL), url.QueryEscape(password))
		body, _, err := c.formRequest(ctx, http.MethodPost, verifyURL, url.Values{"pwd": {password}, "vcode": {""}, "vcode_str": {""}}, headers)
		if err != nil {
			return requestFailure(ctx, item)
		}
		var verify struct {
			Errno  int    `json:"errno"`
			Errmsg string `json:"errmsg"`
			Randsk string `json:"randsk"`
		}
		_ = json.Unmarshal(body, &verify)
		if verify.Errno == -9 || verify.Errno == -12 {
			return resultFor(item, StateLocked, "提取码错误或缺失")
		}
		if verify.Errno != 0 {
			return resultFor(item, StateUncertain, firstNonEmpty(verify.Errmsg, "无法确认链接状态"))
		}
		headers["cookie"] = "BDCLND=" + verify.Randsk
	}
	listURL := fmt.Sprintf("https://pan.baidu.com/share/list?web=1&page=1&num=20&order=time&desc=1&showempty=0&shorturl=%s&root=1&clienttype=0", url.QueryEscape(shortURL))
	body, _, err := c.request(ctx, http.MethodGet, listURL, nil, headers)
	if err != nil {
		return requestFailure(ctx, item)
	}
	var list struct {
		Errno  int    `json:"errno"`
		Errmsg string `json:"errmsg"`
		List   []any  `json:"list"`
	}
	_ = json.Unmarshal(body, &list)
	switch list.Errno {
	case 0:
		if len(list.List) > 0 {
			return resultFor(item, StateOK, "链接有效")
		}
		return resultFor(item, StateBad, "链接失效")
	case -9, -12:
		return resultFor(item, StateLocked, "需要提取码")
	case -7, 105, 115, 117, 145:
		return resultFor(item, StateBad, "链接失效")
	default:
		return resultFor(item, StateUncertain, firstNonEmpty(list.Errmsg, "无法确认链接状态"))
	}
}

func (c *HTTPChecker) checkTianyi(ctx context.Context, item Item) Result {
	shareCode := queryValue(item.URL, "code")
	if shareCode == "" {
		shareCode = regexpFirst(item.URL, `/t/([A-Za-z0-9]+)`)
	}
	if shareCode == "" {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	if item.Password != "" {
		shareCode = fmt.Sprintf("%s（访问码：%s）", shareCode, item.Password)
	}
	apiURL := "https://cloud.189.cn/api/open/share/getShareInfoByCodeV2.action?shareCode=" + url.QueryEscape(shareCode)
	body, _, err := c.request(ctx, http.MethodGet, apiURL, nil, map[string]string{"referer": item.URL, "sign-type": "1"})
	if err != nil {
		return requestFailure(ctx, item)
	}
	var share struct {
		XMLName        xml.Name `xml:"shareVO"`
		NeedAccessCode int      `xml:"needAccessCode"`
		ShareID        int64    `xml:"shareId"`
		FileName       string   `xml:"fileName"`
	}
	if err := xml.Unmarshal(body, &share); err == nil && share.XMLName.Local == "shareVO" {
		if share.ShareID > 0 || share.FileName != "" || share.NeedAccessCode == 1 {
			return resultFor(item, StateOK, "链接有效")
		}
	}
	return stateFromMessage(item, string(body))
}

func (c *HTTPChecker) check123(ctx context.Context, item Item) Result {
	shareKey := lastPathPart(item.URL)
	if shareKey == "" {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	// 123pan now serves the share/info API on the link's own host (the per-user
	// <id>.share.123pan.cn subdomain or www.123912.com); www.123pan.com is a dead
	// SPA catch-all that 404s every /api path. Derive the API base from the URL.
	apiBase := "https://www.123912.com"
	if parsed, err := url.Parse(item.URL); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		apiBase = parsed.Scheme + "://" + parsed.Host
	}
	body, status, err := c.request(ctx, http.MethodGet, apiBase+"/api/share/info?shareKey="+url.QueryEscape(shareKey), nil, nil)
	if err != nil {
		return requestFailure(ctx, item)
	}
	if status == http.StatusForbidden {
		return resultFor(item, StateOK, "链接有效")
	}
	var response struct {
		Code int `json:"code"`
		Data struct {
			HasPwd bool `json:"HasPwd"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return resultFor(item, StateUncertain, "响应解析失败")
	}
	if response.Code == 0 {
		return resultFor(item, StateOK, "链接有效")
	}
	if response.Data.HasPwd {
		return resultFor(item, StateLocked, "需要提取码")
	}
	return resultFor(item, StateBad, firstNonEmpty(response.Message, "链接失效"))
}

func (c *HTTPChecker) checkXunlei(ctx context.Context, item Item) Result {
	shareID := regexpFirst(item.URL, `pan\.xunlei\.com/s/([^?/#]+)`)
	if shareID == "" {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	password := firstNonEmpty(item.Password, queryValue(item.URL, "pwd"))
	apiURL := fmt.Sprintf("%s/drive/v1/share?share_id=%s&pass_code=%s&limit=100&pass_code_token=&page_token=&thumbnail_size=SIZE_SMALL", xunleiShareHost, url.QueryEscape(shareID), url.QueryEscape(password))

	// The share API rejects anonymous lookups with captcha_invalid, so a valid
	// captcha_token (minted via /v1/shield/captcha/init) is mandatory.
	token, err := c.xunleiCaptchaToken(ctx)
	if err != nil {
		if errors.Is(err, errXunleiCaptchaRequired) {
			return resultFor(item, StateUncertain, "需人工验证")
		}
		return resultFor(item, StateUncertain, "验证码获取失败")
	}

	headers := map[string]string{
		"origin":          "https://pan.xunlei.com",
		"referer":         "https://pan.xunlei.com/",
		"x-client-id":     xunleiWebClientID,
		"x-device-id":     xunleiWebDeviceID,
		"x-captcha-token": token,
	}
	body, status, err := c.request(ctx, http.MethodGet, apiURL, nil, headers)
	if err != nil {
		return requestFailure(ctx, item)
	}
	// A token can be invalidated mid-flight; refresh once and retry.
	if isXunleiCaptchaInvalid(body, status) {
		if fresh, ok := c.refreshXunleiToken(ctx); ok {
			headers["x-captcha-token"] = fresh
			body, status, err = c.request(ctx, http.MethodGet, apiURL, nil, headers)
			if err != nil {
				return requestFailure(ctx, item)
			}
		}
		if isXunleiCaptchaInvalid(body, status) {
			return resultFor(item, StateUncertain, "验证码校验失败")
		}
	}
	return classifyXunlei(item, body, status)
}

// classifyXunlei maps the share API response onto a check Result. The server
// returns a share_status field for every reachable share, so that is the source
// of truth; only responses without one fall back to the error/HTTP status.
func classifyXunlei(item Item, body []byte, status int) Result {
	var response struct {
		Error           string `json:"error"`
		ErrorMsg        string `json:"error_description"`
		ShareID         string `json:"share_id"`
		FileCount       int    `json:"file_count"`
		ShareName       string `json:"share_name"`
		ShareStatus     string `json:"share_status"`
		ShareStatusText string `json:"share_status_text"`
	}
	_ = json.Unmarshal(body, &response)
	switch response.ShareStatus {
	case "OK":
		return resultFor(item, StateOK, "链接有效")
	case "PASS_CODE_EMPTY", "PASS_CODE_ERROR":
		return resultFor(item, StateLocked, "需要提取码")
	}
	if response.ShareStatus != "" {
		// Any other status (CANCEL/EXPIRED/BANNED/...) means the share is dead.
		return resultFor(item, StateBad, firstNonEmpty(response.ShareStatusText, "链接失效"))
	}
	msg := firstNonEmpty(response.ErrorMsg, response.Error)
	if status >= 400 && status < 500 {
		return resultFor(item, StateBad, firstNonEmpty(msg, "链接失效"))
	}
	return resultFor(item, StateUncertain, firstNonEmpty(msg, "无法确认链接状态"))
}

// isXunleiCaptchaInvalid reports whether the share API rejected the request
// because the captcha token is missing/expired (error_code 9, captcha_invalid).
func isXunleiCaptchaInvalid(body []byte, status int) bool {
	var response struct {
		Error     string `json:"error"`
		ErrorCode int    `json:"error_code"`
	}
	_ = json.Unmarshal(body, &response)
	return response.Error == "captcha_invalid" || response.ErrorCode == 9
}

var (
	errXunleiCaptchaRequired = errors.New("xunlei interactive captcha required")

	// xunleiToken caches the captcha token process-wide (one init serves the
	// whole batch and any concurrent requests within its lifetime).
	xunleiTokenMu  sync.Mutex // guards xunleiToken / xunleiTokenExp
	xunleiFetchMu  sync.Mutex // serializes token fetches to avoid a stampede
	xunleiToken    string
	xunleiTokenExp time.Time
)

// xunleiCaptchaToken returns a cached token, minting a new one when absent or
// expired. Concurrent callers share a single fetch.
func (c *HTTPChecker) xunleiCaptchaToken(ctx context.Context) (string, error) {
	if tok := readXunleiToken(); tok != "" {
		return tok, nil
	}
	xunleiFetchMu.Lock()
	defer xunleiFetchMu.Unlock()
	// Another goroutine may have refreshed the token while we waited.
	if tok := readXunleiToken(); tok != "" {
		return tok, nil
	}
	tok, ttl, err := c.fetchXunleiToken(ctx)
	if err != nil {
		return "", err
	}
	storeXunleiToken(tok, ttl)
	return tok, nil
}

// refreshXunleiToken mints a fresh token unconditionally (used after a token is
// rejected) and returns it.
func (c *HTTPChecker) refreshXunleiToken(ctx context.Context) (string, bool) {
	xunleiFetchMu.Lock()
	defer xunleiFetchMu.Unlock()
	tok, ttl, err := c.fetchXunleiToken(ctx)
	if err != nil {
		return "", false
	}
	storeXunleiToken(tok, ttl)
	return tok, true
}

func readXunleiToken() string {
	xunleiTokenMu.Lock()
	defer xunleiTokenMu.Unlock()
	if xunleiToken != "" && time.Now().Before(xunleiTokenExp) {
		return xunleiToken
	}
	return ""
}

func storeXunleiToken(token string, ttl time.Duration) {
	if ttl > 30*time.Second {
		ttl -= 30 * time.Second // refresh before the real expiry
	}
	xunleiTokenMu.Lock()
	xunleiToken = token
	xunleiTokenExp = time.Now().Add(ttl)
	xunleiTokenMu.Unlock()
}

// fetchXunleiToken mints an anonymous captcha token for the share lookup.
func (c *HTTPChecker) fetchXunleiToken(ctx context.Context) (string, time.Duration, error) {
	timestamp := fmt.Sprint(time.Now().UnixMilli())
	payload := map[string]any{
		"action":        "get:/drive/v1/share",
		"captcha_token": "",
		"client_id":     xunleiInitClientID,
		"device_id":     xunleiInitDeviceID,
		"meta": map[string]any{
			"timestamp":      timestamp,
			"captcha_sign":   xunleiCaptchaSign(timestamp),
			"client_version": xunleiInitClientVersion,
			"package_name":   xunleiInitPackageName,
			"username":       "",
			"phone_number":   "",
			"email":          "",
			"user_id":        "0",
		},
		"redirect_uri": "xlaccsdk01://xunlei.com/callback?state=harbor",
	}
	body, status, err := c.jsonRequest(ctx, http.MethodPost, xunleiCaptchaInitURL, payload, map[string]string{
		"accept":           "application/json;charset=UTF-8",
		"content-type":     "application/json",
		"x-client-id":      xunleiInitClientID,
		"x-device-id":      xunleiInitDeviceID,
		"x-client-version": xunleiInitClientVersion,
	})
	if err != nil {
		return "", 0, err
	}
	if status != http.StatusOK {
		return "", 0, fmt.Errorf("captcha init failed: status %d", status)
	}
	var resp struct {
		CaptchaToken string `json:"captcha_token"`
		URL          string `json:"url"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", 0, err
	}
	if resp.URL != "" {
		// Xunlei demands an interactive (slider) captcha that cannot be solved server-side.
		return "", 0, errXunleiCaptchaRequired
	}
	if resp.CaptchaToken == "" {
		return "", 0, fmt.Errorf("empty captcha token")
	}
	ttl := 300 * time.Second
	if resp.ExpiresIn > 0 {
		ttl = time.Duration(resp.ExpiresIn) * time.Second
	}
	return resp.CaptchaToken, ttl, nil
}

// xunleiCaptchaSign reproduces the xunlei shield signature: the client identity
// plus timestamp is hashed through a fixed chain of salt strings.
func xunleiCaptchaSign(timestamp string) string {
	str := xunleiInitClientID + xunleiInitClientVersion + xunleiInitPackageName + xunleiInitDeviceID + timestamp
	for _, algorithm := range xunleiCaptchaAlgorithms {
		sum := md5.Sum([]byte(str + algorithm))
		str = hex.EncodeToString(sum[:])
	}
	return "1." + str
}

const (
	// Web client identity sent on the share lookup (matches the browser app).
	xunleiWebClientID = "ZUBzD9J_XPXfn7f7"
	xunleiWebDeviceID = "5505bd0cab8c9469b98e5891d9fb3e0d"

	// Android-app identity used to mint the captcha token; captcha_sign is
	// computed against these values.
	xunleiInitClientID      = "ZUBzD9J_XPXfn7f7"
	xunleiInitDeviceID      = "5505bd0cab8c9469b98e5891d9fb3e0d"
	xunleiInitClientVersion = "1.10.0.2633"
	xunleiInitPackageName   = "com.xunlei.browser"
)

// xunleiCaptchaAlgorithms is the fixed salt chain used by the official client.
var xunleiCaptchaAlgorithms = []string{
	"uWRwO7gPfdPB/0NfPtfQO+71",
	"F93x+qPluYy6jdgNpq+lwdH1ap6WOM+nfz8/V",
	"0HbpxvpXFsBK5CoTKam",
	"dQhzbhzFRcawnsZqRETT9AuPAJ+wTQso82mRv",
	"SAH98AmLZLRa6DB2u68sGhyiDh15guJpXhBzI",
	"unqfo7Z64Rie9RNHMOB",
	"7yxUdFADp3DOBvXdz0DPuKNVT35wqa5z0DEyEvf",
	"RBG",
	"ThTWPG5eC0UBqlbQ+04nZAptqGCdpv9o55A",
}

// xunleiShareHost and xunleiCaptchaInitURL are the live endpoints; tests override
// them to point at a local httptest server.
var (
	xunleiShareHost      = "https://api-pan.xunlei.com"
	xunleiCaptchaInitURL = "https://xluser-ssl.xunlei.com/v1/shield/captcha/init"
)

func (c *HTTPChecker) check115(ctx context.Context, item Item) Result {
	shareCode := lastPathPart(item.URL)
	password := firstNonEmpty(item.Password, queryValue(item.URL, "password"))
	if shareCode == "" {
		return resultFor(item, StateUncertain, "无法解析分享地址")
	}
	if password == "" {
		return resultFor(item, StateLocked, "115 需要提取码")
	}
	apiURL := fmt.Sprintf("https://115cdn.com/webapi/share/snap?share_code=%s&offset=0&limit=20&receive_code=%s&cid=", url.QueryEscape(shareCode), url.QueryEscape(password))
	body, _, err := c.request(ctx, http.MethodGet, apiURL, nil, map[string]string{"referer": fmt.Sprintf("https://115cdn.com/s/%s?password=%s&", shareCode, password), "x-requested-with": "XMLHttpRequest"})
	if err != nil {
		return requestFailure(ctx, item)
	}
	var response struct {
		State bool   `json:"state"`
		Error string `json:"error"`
		Errno int    `json:"errno"`
		Data  struct {
			List      []any `json:"list"`
			Count     int   `json:"count"`
			ShareInfo struct {
				SnapID       string `json:"snap_id"`
				ShareTitle   string `json:"share_title"`
				ForbidReason string `json:"forbid_reason"`
			} `json:"shareinfo"`
		} `json:"data"`
	}
	_ = json.Unmarshal(body, &response)
	if response.State && response.Errno == 0 && (len(response.Data.List) > 0 || response.Data.Count > 0 || response.Data.ShareInfo.SnapID != "" || response.Data.ShareInfo.ShareTitle != "") {
		return resultFor(item, StateOK, "链接有效")
	}
	return stateFromMessage(item, firstNonEmpty(response.Error, response.Data.ShareInfo.ForbidReason, string(body)))
}

func (c *HTTPChecker) checkMobile(ctx context.Context, item Item) Result {
	return c.checkPageKeywords(ctx, item, []string{"失效", "不存在", "过期", "取消"}, []string{"提取码", "密码", "访问码"}, []string{"文件", "分享", "yun.139.com", "caiyun.139.com"})
}

func (c *HTTPChecker) checkPageKeywords(ctx context.Context, item Item, badKeywords, lockedKeywords, okKeywords []string) Result {
	body, status, err := c.request(ctx, http.MethodGet, item.URL, nil, nil)
	if err != nil {
		return requestFailure(ctx, item)
	}
	if status == http.StatusNotFound {
		return resultFor(item, StateBad, "链接失效")
	}
	text := strings.ToLower(string(body))
	switch {
	case containsAny(text, badKeywords...):
		return resultFor(item, StateBad, "链接失效")
	case containsAny(text, lockedKeywords...):
		return resultFor(item, StateLocked, "需要提取码")
	case containsAny(text, okKeywords...):
		return resultFor(item, StateOK, "链接有效")
	default:
		return resultFor(item, StateUncertain, "无法确认链接状态")
	}
}

func (c *HTTPChecker) jsonRequest(ctx context.Context, method, targetURL string, payload any, headers map[string]string) ([]byte, int, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	return c.request(ctx, method, targetURL, bytes.NewReader(raw), headers)
}

func (c *HTTPChecker) formRequest(ctx context.Context, method, targetURL string, form url.Values, headers map[string]string) ([]byte, int, error) {
	if headers == nil {
		headers = map[string]string{}
	}
	headers["content-type"] = "application/x-www-form-urlencoded"
	return c.request(ctx, method, targetURL, strings.NewReader(form.Encode()), headers)
}

func (c *HTTPChecker) request(ctx context.Context, method, targetURL string, body io.Reader, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0 Safari/537.36")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return raw, resp.StatusCode, err
}

func requestFailure(ctx context.Context, item Item) Result {
	if err := ctx.Err(); err != nil {
		return resultFor(item, StateUncertain, "检测超时")
	}
	return resultFor(item, StateUncertain, "请求失败")
}

func resultFor(item Item, state string, summary string) Result {
	return Result{DiskType: item.DiskType, URL: item.URL, Password: item.Password, State: state, Summary: summary}
}

func stateFromMessage(item Item, message string) Result {
	lower := strings.ToLower(message)
	switch {
	case containsAny(lower, "提取码", "密码", "访问码", "passcode", "pass_code", "receive_code"):
		return resultFor(item, StateLocked, firstNonEmpty(message, "需要提取码"))
	case containsAny(lower, "不存在", "失效", "违规", "过期", "取消", "not found", "deleted", "forbidden", "shareinfonotfound", "sharenotfound", "filenotfound", "shareexpirederror"):
		return resultFor(item, StateBad, firstNonEmpty(message, "链接失效"))
	default:
		return resultFor(item, StateUncertain, firstNonEmpty(message, "无法确认链接状态"))
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func containsAny(content string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(content, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func lastPathPart(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSuffix(parts[len(parts)-1], ".html")
}

func queryValue(rawURL, key string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return parsed.Query().Get(key)
}

func regexpFirst(content, pattern string) string {
	matches := regexp.MustCompile(pattern).FindStringSubmatch(content)
	if len(matches) < 2 {
		return ""
	}
	return matches[1]
}
