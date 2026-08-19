package server

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

const gzipBodyLimitInteraction = 1 << 20

func TestRouter_RejectsGzipBombByUncompressedSize(t *testing.T) {
	t.Skip("requires feature/body-limit")
	_, srv := newTestServer(t, zap.NewNop(), config.Default())

	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	_, err := zw.Write(make([]byte, gzipBodyLimitInteraction+1))
	require.NoError(t, err)
	require.NoError(t, zw.Close())

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/user/register", &bomb)
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

// Поток пустых gzip-членов не даёт распакованных байт, лимит считает сжатые.
func TestRouter_RejectsOversizedCompressedBody(t *testing.T) {
	t.Skip("requires feature/body-limit")
	echo := newEchoServer(t)

	var member bytes.Buffer
	zw := gzip.NewWriter(&member)
	require.NoError(t, zw.Close())
	body := bytes.Repeat(member.Bytes(), gzipBodyLimitInteraction/member.Len()+1)

	req, err := http.NewRequest(http.MethodPost, echo.http.URL+"/echo", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := echo.http.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.True(t, echo.called)
}
