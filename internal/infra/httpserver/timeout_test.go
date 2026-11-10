package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTimeoutCancelsOperationContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var requestContext context.Context
	router.GET("/work", Timeout(time.Nanosecond), func(c *gin.Context) {
		requestContext = c.Request.Context()
		<-requestContext.Done()
		if !errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			t.Error("operation did not observe its timeout")
		}
		c.Status(http.StatusGatewayTimeout)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/work", nil))
	if requestContext == nil || requestContext.Err() == nil {
		t.Fatal("operation context was not canceled")
	}
}
