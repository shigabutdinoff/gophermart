package compress

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSniffer_AccumulatesAndTakes(t *testing.T) {
	var s sniffer
	assert.Equal(t, 0, s.buffered())

	s.add([]byte("ab"))
	s.add([]byte("cde"))
	assert.Equal(t, 5, s.buffered())

	assert.Equal(t, "abcde", string(s.take()))
	assert.Equal(t, 0, s.buffered())
}

func TestSniffer_TakeEmpty(t *testing.T) {
	var s sniffer
	assert.Nil(t, s.take())
}

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
