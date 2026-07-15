package compress

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnsureContentType_KeepsExisting(t *testing.T) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	ensureContentType(h, []byte("<html>"))
	assert.Equal(t, "application/json", h.Get("Content-Type"))
}

func TestEnsureContentType_DetectsWhenEmpty(t *testing.T) {
	h := http.Header{}
	ensureContentType(h, []byte("<html><body>"+strings.Repeat("x", 16)+"</body></html>"))
	assert.Contains(t, h.Get("Content-Type"), "text/html")
}

func TestCompressibleStatus(t *testing.T) {
	assert.True(t, compressibleStatus(http.StatusOK))
	assert.False(t, compressibleStatus(http.StatusNoContent))
	assert.False(t, compressibleStatus(http.StatusMovedPermanently))
}
