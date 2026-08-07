package logging

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/httplog/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/exp/zapslog"
)

// schema убирает из записи полный URL, в нём приезжает строка запроса.
var schema = func() *httplog.Schema {
	fields := *httplog.SchemaECS
	fields.RequestURL = ""

	return &fields
}()

// queryFreeHandler убирает строку запроса из текста записи.
// httplog собирает его из r.URL, и отключить это нечем.
type queryFreeHandler struct {
	slog.Handler
}

func (h queryFreeHandler) Handle(ctx context.Context, record slog.Record) error {
	if start := strings.IndexByte(record.Message, '?'); start >= 0 {
		end := strings.Index(record.Message[start:], " =>")
		if end < 0 {
			end = len(record.Message) - start
		}
		record.Message = record.Message[:start] + record.Message[start+end:]
	}

	return h.Handler.Handle(ctx, record)
}

func (h queryFreeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return queryFreeHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h queryFreeHandler) WithGroup(name string) slog.Handler {
	return queryFreeHandler{Handler: h.Handler.WithGroup(name)}
}

// WithLogging логирует сведения о запросе без тел, заголовков и строки запроса.
func WithLogging(logger *zap.Logger) func(http.Handler) http.Handler {
	handler := queryFreeHandler{Handler: zapslog.NewHandler(logger.Core())}

	return httplog.RequestLogger(
		slog.New(handler),
		&httplog.Options{
			Level: slog.LevelInfo,
			// ответ на панику и запись о ней даёт один слой
			RecoverPanics:     true,
			Schema:            schema,
			LogRequestHeaders: []string{},
		},
	)
}
