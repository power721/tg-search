package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"tg-search/internal/retry"
	"tg-search/internal/scheduler"
)

func TestRetryJobEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)

	queue := scheduler.NewRetryQueue(scheduler.RetryQueueOptions{
		Policy: retry.DefaultPolicy(),
		Logger: zap.NewNop(),
	})

	// Enqueue a test job
	job := queue.Enqueue(context.Background(), "test-job", func(ctx context.Context) error {
		return nil
	})

	deps := Dependencies{
		SyncQueue: queue,
	}
	h := handlers{deps: deps}

	t.Run("returns job when it exists", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/"+job.ID, nil)
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = gin.Params{{Key: "id", Value: job.ID}}

		h.retryJob(c)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
		}

		var response scheduler.RetryJob
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("failed to parse response: %v", err)
		}

		if response.ID != job.ID {
			t.Errorf("job.ID = %s, want %s", response.ID, job.ID)
		}
		if response.Name != "test-job" {
			t.Errorf("job.Name = %s, want test-job", response.Name)
		}
	})

	t.Run("returns 404 when job not found", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/999", nil)
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = gin.Params{{Key: "id", Value: "999"}}

		h.retryJob(c)

		if w.Code != http.StatusNotFound {
			t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("returns 400 when id is empty", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/", nil)
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = gin.Params{{Key: "id", Value: ""}}

		h.retryJob(c)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("returns 503 when SyncQueue is nil", func(t *testing.T) {
		h := handlers{deps: Dependencies{}}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/1", nil)
		c, _ := gin.CreateTestContext(w)
		c.Request = req
		c.Params = gin.Params{{Key: "id", Value: "1"}}

		h.retryJob(c)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})
}
