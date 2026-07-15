package server

import (
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	config "github.com/shigabutdinoff/gophermart/internal/config/gophermart"
)

func startServer(t *testing.T, ctx context.Context, s *Server) <-chan error {
	t.Helper()

	require.NoError(t, s.listen())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	return done
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Run не завершился вовремя")
		return nil
	}
}

func TestNew(t *testing.T) {
	s := New(zap.NewNop(), config.Default())

	assert.Equal(t, config.DefaultRunAddress, s.RunAddress)
	assert.Equal(t, DefaultShutdownTimeout, s.shutdownTimeout)
	assert.NotNil(t, s.router)
}

func TestServer_ServesAndLogsStart(t *testing.T) {
	core, logs := observer.New(zap.InfoLevel)
	s := New(zap.New(core), config.Default())
	s.RunAddress = "127.0.0.1:0"

	r := chi.NewRouter()
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	s.router = r

	ctx, cancel := context.WithCancel(context.Background())
	done := startServer(t, ctx, s)

	resp, err := http.Get("http://" + s.Addr() + "/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	cancel()
	assert.NoError(t, waitDone(t, done))

	started := logs.FilterMessage("Сервер запущен").All()
	require.Len(t, started, 1)
	assert.Equal(t, s.Addr(), started[0].ContextMap()["address"])
}

func TestServer_GracefulShutdownWaitsForActiveRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	r := chi.NewRouter()
	r.Get("/hold", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("done"))
	})
	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	core, logs := observer.New(zap.InfoLevel)
	s := New(zap.New(core), config.Default())
	s.RunAddress = "127.0.0.1:0"
	s.router = r

	ctx, cancel := context.WithCancel(context.Background())
	done := startServer(t, ctx, s)

	type result struct {
		body string
		err  error
	}
	resCh := make(chan result, 1)
	go func() {
		resp, err := http.Get("http://" + s.Addr() + "/hold")
		if err != nil {
			resCh <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		resCh <- result{body: string(b), err: err}
	}()

	<-started
	cancel()

	probe := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	require.Eventually(t, func() bool {
		resp, err := probe.Get("http://" + s.Addr() + "/")
		if err == nil {
			resp.Body.Close()
			return false
		}
		return true
	}, 2*time.Second, 10*time.Millisecond)

	close(release)

	res := <-resCh
	require.NoError(t, res.err)
	assert.Equal(t, "done", res.body)

	assert.NoError(t, waitDone(t, done))

	assert.Equal(t, 1, logs.FilterMessage("Начата остановка сервера").Len())
	assert.Equal(t, 1, logs.FilterMessage("Остановка завершена").Len())
}

func TestServer_ForcesShutdownAfterTimeout(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	r := chi.NewRouter()
	r.Get("/", func(http.ResponseWriter, *http.Request) {
		close(started)
		<-release
	})

	core, logs := observer.New(zap.InfoLevel)
	s := New(zap.New(core), config.Default())
	s.RunAddress = "127.0.0.1:0"
	s.shutdownTimeout = 100 * time.Millisecond
	s.router = r

	ctx, cancel := context.WithCancel(context.Background())
	done := startServer(t, ctx, s)

	go func() {
		resp, err := http.Get("http://" + s.Addr() + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()

	<-started
	cancel()

	require.Error(t, waitDone(t, done))
	close(release)

	assert.Equal(t, 1, logs.FilterMessage("Превышен таймаут остановки, принудительное закрытие").Len())
}

func TestServer_ServeErrorReturnedNotLogged(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	s := New(zap.New(core), config.Default())
	s.RunAddress = "127.0.0.1:0"
	s.router = chi.NewRouter()

	done := startServer(t, context.Background(), s)

	require.Eventually(t, func() bool {
		return s.ln.Close() == nil
	}, 2*time.Second, 10*time.Millisecond)

	require.Error(t, waitDone(t, done))

	assert.Equal(t, 0, logs.FilterMessage("Сервер завершился с ошибкой").Len())
}

func TestServer_ServeFailureClosesActiveConnections(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	r := chi.NewRouter()
	r.Get("/hold", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	})

	s := New(zap.NewNop(), config.Default())
	s.RunAddress = "127.0.0.1:0"
	s.router = r

	done := startServer(t, context.Background(), s)

	resCh := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + s.Addr() + "/hold")
		if err == nil {
			_, err = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		resCh <- err
	}()

	<-started
	require.NoError(t, s.ln.Close())

	require.Error(t, waitDone(t, done))

	select {
	case err := <-resCh:
		assert.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("активное соединение пережило возврат Run")
	}
}

func TestServer_Run_AddressBusy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()

	s := New(zap.NewNop(), config.Default())
	s.RunAddress = ln.Addr().String()
	require.Error(t, s.Run(context.Background()))
}

func TestServer_Run_InvalidAddress(t *testing.T) {
	s := New(zap.NewNop(), config.Default())
	s.RunAddress = "bad::addr"
	require.Error(t, s.Run(context.Background()))
}

func TestRouter_PanicRecovered(t *testing.T) {
	s := New(zap.NewNop(), config.Default())
	s.router.Get("/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })
	srv := httptest.NewServer(s.router)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/panic")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	resp2, err := srv.Client().Get(srv.URL + "/api/unknown")
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestRouter_PanicMidResponseAbortsConnection(t *testing.T) {
	s := New(zap.NewNop(), config.Default())
	s.router.Get("/broken", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("partial"))
		panic("boom")
	})
	srv := httptest.NewServer(s.router)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/broken")
	if err == nil {
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
	}
	assert.Error(t, err)
}

func TestRouter_PanicAfterBufferedStatusAnswers500(t *testing.T) {
	s := New(zap.NewNop(), config.Default())
	s.router.Get("/panic", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("boom")
	})
	srv := httptest.NewServer(s.router)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/panic", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestRouter_LogsEveryRequest(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	s := New(zap.New(core), config.Default())
	s.router.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(s.router)
	defer srv.Close()

	reqs := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/ping", http.StatusOK},
		{http.MethodPost, "/ping", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/unknown", http.StatusNotFound},
	}
	for _, tc := range reqs {
		req, err := http.NewRequest(tc.method, srv.URL+tc.path, http.NoBody)
		require.NoError(t, err)
		resp, err := srv.Client().Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, tc.want, resp.StatusCode)
	}

	entries := observed.FilterMessage("Сведения о запросе").All()
	require.Len(t, entries, len(reqs))
	for i, tc := range reqs {
		fields := entries[i].ContextMap()
		assert.Equal(t, tc.method, fields["method"])
		assert.Equal(t, tc.path, fields["uri"])
		assert.EqualValues(t, tc.want, fields["status"])
	}
}

func TestRouter_CompressesResponseForGzipClient(t *testing.T) {
	s := New(zap.NewNop(), config.Default())
	s.router.Get("/payload", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok","padding":"` + strings.Repeat("0123456789", 60) + `"}`))
	})
	srv := httptest.NewServer(s.router)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/payload", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", "gzip")

	resp, err := rawClient().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	zr, err := gzip.NewReader(resp.Body)
	require.NoError(t, err)
	body, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"status":"ok"`)
}

// rawClient даёт клиент без прозрачной декомпрессии и своего Accept-Encoding.
func rawClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DisableCompression: true}}
}
