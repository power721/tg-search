package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"tg-search/internal/linkcheck"
)

type externalLinkCheckRequest struct {
	Items     []linkcheck.Item `json:"items"`
	TimeoutMS int64            `json:"timeout_ms"`
	Timeout   int64            `json:"timeout"`
}

func (h handlers) externalCheckLinks(c *gin.Context) {
	var req externalLinkCheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, externalAPIResponse{Code: http.StatusBadRequest, Message: "invalid request: " + err.Error()})
		return
	}

	timeout := req.TimeoutMS
	if timeout == 0 {
		timeout = req.Timeout * 1000
	}
	service := linkcheck.NewService(linkcheck.Options{})
	response, err := service.Check(c.Request.Context(), linkcheck.Request{
		Items:   req.Items,
		Timeout: time.Duration(timeout) * time.Millisecond,
	})
	if err != nil {
		status := http.StatusBadRequest
		message := err.Error()
		switch {
		case errors.Is(err, linkcheck.ErrItemsRequired):
			message = "items is required"
		}
		c.JSON(status, externalAPIResponse{Code: status, Message: message})
		return
	}

	c.PureJSON(http.StatusOK, externalAPIResponse{Code: 0, Message: "success", Data: response})
}
