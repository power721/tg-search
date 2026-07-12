package api

import (
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"tg-search/internal/apikey"
	"tg-search/internal/model"
)

func TestMediaFileIDsFallsBackToVideoForImage(t *testing.T) {
	imageID, videoID := mediaFileIDs("", []model.File{{
		TelegramFileID: 42,
		FileName:       "movie.mp4",
		MimeType:       "video/mp4",
	}})
	if imageID != 42 || videoID != 42 {
		t.Fatalf("media IDs = (%d, %d), want (42, 42)", imageID, videoID)
	}
}

func TestMediaURLSignerSignsMultiplePathsWithOneKey(t *testing.T) {
	expiresAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	signer := newMediaURLSigner("0123456789abcdef0123456789abcdef", expiresAt)

	first, err := signer.mediaURL("i", 101)
	if err != nil {
		t.Fatalf("sign first URL: %v", err)
	}
	second, err := signer.mediaURL("i", 202)
	if err != nil {
		t.Fatalf("sign second URL: %v", err)
	}

	wantExp := strconv.FormatInt(expiresAt.Unix(), 10)
	for _, rawURL := range []string{first, second} {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			t.Fatalf("parse %q: %v", rawURL, err)
		}
		if parsed.Query().Get("exp") != wantExp {
			t.Fatalf("URL %q exp = %q, want %q", rawURL, parsed.Query().Get("exp"), wantExp)
		}
		wantSig, err := apikey.MediaSignature(
			"0123456789abcdef0123456789abcdef",
			http.MethodGet,
			parsed.EscapedPath(),
			wantExp,
		)
		if err != nil {
			t.Fatalf("expected signature: %v", err)
		}
		if parsed.Query().Get("sig") != wantSig {
			t.Fatalf("URL %q signature = %q, want %q", rawURL, parsed.Query().Get("sig"), wantSig)
		}
	}
}
