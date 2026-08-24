package server

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func TestRouter_RejectsGzipBombByUncompressedSize(t *testing.T) {
	server, srv := newTestServer(t, zap.NewNop(), config.Default())

	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	_, err := zw.Write(make([]byte, server.requestBodyLimit+1))
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
	echo := newEchoServer(t)

	var member bytes.Buffer
	zw := gzip.NewWriter(&member)
	require.NoError(t, zw.Close())
	body := bytes.Repeat(member.Bytes(), int(echo.requestBodyLimit)/member.Len()+1)

	req, err := http.NewRequest(http.MethodPost, echo.http.URL+"/echo", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")

	resp, err := echo.http.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assert.True(t, echo.called)
}

func TestRouter_RejectsOversizedBodyWhileHandlerReads(t *testing.T) {
	server, srv := newTestServer(t, zap.NewNop(), config.Default())

	body := bytes.NewReader(make([]byte, server.requestBodyLimit+1))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/user/register", body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
}

func TestRouter_RegisterBodyStatuses(t *testing.T) {
	tests := []struct {
		name   string
		body   func(limit int64) string
		status int
	}{
		{
			name: "тело длиннее лимита",
			body: func(limit int64) string {
				return `{"login":"user","password":"` + strings.Repeat("p", int(limit)) + `"}`
			},
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name: "хвост за концом JSON",
			body: func(limit int64) string {
				return `{"login":"user","password":"password"}` + strings.Repeat(" ", int(limit))
			},
			status: http.StatusRequestEntityTooLarge,
		},
		{
			name:   "оборванный JSON",
			body:   func(int64) string { return `{"login":"user","password":` },
			status: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, srv := newTestServer(t, zap.NewNop(), config.Default())

			req, err := http.NewRequest(
				http.MethodPost,
				srv.URL+"/api/user/register",
				strings.NewReader(tt.body(s.requestBodyLimit)),
			)
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")

			resp, err := srv.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tt.status, resp.StatusCode)
		})
	}
}
