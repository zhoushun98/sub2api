//go:build unit

package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOtokUSDTQuoteRejectsAbnormalAmounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewOtokUSDTHandler(nil)
	for _, amount := range []string{"NaN", "nan", "Inf", "-Inf", "+Inf", "-1", "1e9", "abc"} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/quote?amount="+amount, nil)
		h.GetQuote(c)
		require.Equal(t, http.StatusBadRequest, rec.Code, amount)
	}
}
