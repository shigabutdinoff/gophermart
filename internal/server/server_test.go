package server

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNew(t *testing.T) {
	s := New(zap.NewNop())

	assert.Equal(t, DefaultRunAddress, s.RunAddress)
	assert.NotNil(t, s.router)
}

func TestServer_ServesAndLogsStart(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	s := New(zap.New(core))
	s.RunAddress = "127.0.0.1:0"

	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	s.router = r
	require.NoError(t, s.listen())

	done := make(chan error, 1)
	go func() { done <- s.Run() }()

	resp, err := http.Get("http://" + s.Addr() + "/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	started := logs.FilterMessage("Сервер запущен").All()
	require.Len(t, started, 1)
	assert.Equal(t, s.Addr(), started[0].ContextMap()["address"])

	require.NoError(t, s.ln.Close())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run не вернулся после закрытия слушателя")
	}
}

func TestServer_ServeErrorReturnedNotLogged(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	s := New(zap.New(core))
	s.RunAddress = "127.0.0.1:0"
	s.router = chi.NewRouter()
	require.NoError(t, s.listen())

	done := make(chan error, 1)
	go func() { done <- s.Run() }()

	require.Eventually(t, func() bool {
		return s.ln.Close() == nil
	}, 2*time.Second, 10*time.Millisecond)

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run не вернулся после падения Serve")
	}

	assert.Equal(t, 0, logs.FilterMessage("Сервер завершился с ошибкой").Len())
}

func TestServer_Run_AddressBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	s := New(zap.NewNop())
	s.RunAddress = ln.Addr().String()
	require.Error(t, s.Run())
}

func TestServer_Run_InvalidAddress(t *testing.T) {
	s := New(zap.NewNop())
	s.RunAddress = "bad::addr"
	require.Error(t, s.Run())
}
