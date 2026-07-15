package healthcheck

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPing_OK(t *testing.T) {
	rec := httptest.NewRecorder()

	Ping(rec, httptest.NewRequest(http.MethodGet, "/ping", nil))

	res := rec.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "application/json", res.Header.Get("Content-Type"))

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status":"ok"}`, string(body))
}
