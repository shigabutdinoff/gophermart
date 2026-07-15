package bodylimit

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func echoHandler(called *bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		body, ok := ReadAll(w, r)
		if !ok {
			return
		}
		_, _ = w.Write(body)
	})
}

// unknownLength прячет размер тела от httptest.NewRequest.
type unknownLength struct{ io.Reader }

func TestLimit_EarlyRejectByContentLength(t *testing.T) {
	called := false
	h := Limit(8)(echoHandler(&called))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(make([]byte, 9))))
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.False(t, called)
}

func TestLimit_RejectsOversizedBodyOnRead(t *testing.T) {
	called := false
	h := Limit(8)(echoHandler(&called))
	req := httptest.NewRequest(http.MethodPost, "/", unknownLength{strings.NewReader("123456789")})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.True(t, called)
}

func TestLimit_AllowsBodyWithinLimit(t *testing.T) {
	called := false
	h := Limit(8)(echoHandler(&called))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString("12345678")))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called)
	assert.Equal(t, "12345678", rec.Body.String())
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestReadAll_ReadErrorAnswers400(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Body = io.NopCloser(failingBody{})
	rec := httptest.NewRecorder()

	body, ok := ReadAll(rec, req)

	assert.False(t, ok)
	assert.Nil(t, body)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
