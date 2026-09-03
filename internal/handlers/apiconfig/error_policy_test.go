package apiconfig

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

type embeddedHumaContext interface {
	huma.Context
}

type operationProbeContext struct {
	embeddedHumaContext
	operation      *huma.Operation
	operationCalls atomic.Int64
}

func (ctx *operationProbeContext) Operation() *huma.Operation {
	ctx.operationCalls.Add(1)

	return ctx.operation
}

func countingErrorFactory(calls *atomic.Int64) errorFactory {
	return func(_ huma.Context, status int, msg string, errs ...error) huma.StatusError {
		calls.Add(1)

		return huma.NewError(status, msg, errs...)
	}
}

func TestNewDoesNotInstallErrorPolicy(t *testing.T) {
	originalFactory := huma.NewErrorWithContext
	originalInstaller := installErrorPolicyOnce
	t.Cleanup(func() {
		huma.NewErrorWithContext = originalFactory
		installErrorPolicyOnce = originalInstaller
	})

	var factoryCalls atomic.Int64
	huma.NewErrorWithContext = countingErrorFactory(&factoryCalls)
	installErrorPolicyOnce = newErrorPolicyInstaller(&huma.NewErrorWithContext)
	ctx := &operationProbeContext{operation: &huma.Operation{}}

	_ = New()
	result := huma.NewErrorWithContext(
		ctx,
		http.StatusUnprocessableEntity,
		humaValidationErrorMessage,
		errors.New("invalid field"),
	)

	assert.Equal(t, http.StatusUnprocessableEntity, result.GetStatus())
	assert.Equal(t, int64(1), factoryCalls.Load())
	assert.Zero(t, ctx.operationCalls.Load())
}

func TestErrorPolicyInstallerWrapsFactoryOnce(t *testing.T) {
	var factoryCalls atomic.Int64
	target := countingErrorFactory(&factoryCalls)
	install := newErrorPolicyInstaller(&target)
	var wait sync.WaitGroup
	for range 32 {
		wait.Go(install)
	}
	wait.Wait()

	install()
	ctx := &operationProbeContext{operation: &huma.Operation{}}
	result := target(
		ctx,
		http.StatusUnprocessableEntity,
		humaValidationErrorMessage,
		errors.New("invalid field"),
	)

	assert.Equal(t, http.StatusUnprocessableEntity, result.GetStatus())
	assert.Equal(t, int64(1), factoryCalls.Load())
	assert.Equal(t, int64(1), ctx.operationCalls.Load())
}

func TestErrorPolicyKeepsNonValidationStatus(t *testing.T) {
	var factoryCalls atomic.Int64
	target := countingErrorFactory(&factoryCalls)
	newErrorPolicyInstaller(&target)()
	ctx := &operationProbeContext{operation: &huma.Operation{
		Metadata: ValidationErrorsAsBadRequest(),
	}}

	result := target(
		ctx,
		http.StatusConflict,
		humaValidationErrorMessage,
		errors.New("invalid field"),
	)

	assert.Equal(t, http.StatusConflict, result.GetStatus())
	assert.Equal(t, int64(1), factoryCalls.Load())
	assert.Zero(t, ctx.operationCalls.Load())
}

func TestErrorPolicyUsesOperationMetadataRegardlessOfRegistrationOrder(t *testing.T) {
	InstallErrorPolicy()

	type validationInput struct {
		Body struct {
			Value string `json:"value" minLength:"5"`
		}
	}
	operations := []struct {
		name       string
		path       string
		metadata   map[string]any
		wantStatus int
	}{
		{
			name:       "marked",
			path:       "/marked",
			metadata:   ValidationErrorsAsBadRequest(),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "unmarked",
			path:       "/unmarked",
			wantStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, order := range []struct {
		name    string
		indexes []int
	}{
		{name: "marked first", indexes: []int{0, 1}},
		{name: "unmarked first", indexes: []int{1, 0}},
	} {
		t.Run(order.name, func(t *testing.T) {
			router := chi.NewRouter()
			api := humachi.New(router, New())
			for _, index := range order.indexes {
				operation := operations[index]
				huma.Register(api, huma.Operation{
					OperationID: "validate-" + operation.name,
					Method:      http.MethodPost,
					Path:        operation.path,
					Metadata:    operation.metadata,
				}, func(context.Context, *validationInput) (*struct{}, error) {
					return nil, nil
				})
			}

			for _, operation := range operations {
				t.Run(operation.name, func(t *testing.T) {
					response := httptest.NewRecorder()
					request := httptest.NewRequest(
						http.MethodPost,
						operation.path,
						strings.NewReader(`{"value":"no"}`),
					)
					request.Header.Set("Content-Type", "application/json")
					router.ServeHTTP(response, request)

					assert.Equal(t, operation.wantStatus, response.Code)
				})
			}
		})
	}
}

func TestErrorPolicyKeepsNonValidationContextErrorsUnprocessable(t *testing.T) {
	InstallErrorPolicy()

	tests := []struct {
		name    string
		message string
		details []error
	}{
		{
			name:    "other message",
			message: "middleware rejected request",
			details: []error{errors.New("invalid field")},
		},
		{name: "reserved message without details", message: humaValidationErrorMessage},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			type validInput struct {
				Body struct {
					Value string `json:"value" minLength:"5"`
				}
			}

			router := chi.NewRouter()
			api := humachi.New(router, New())
			huma.Register(api, huma.Operation{
				OperationID: "context-error-" + strings.ReplaceAll(test.name, " ", "-"),
				Method:      http.MethodPost,
				Path:        "/context-error",
				Metadata:    ValidationErrorsAsBadRequest(),
				Middlewares: huma.Middlewares{
					func(ctx huma.Context, _ func(huma.Context)) {
						_ = huma.WriteErr(
							api,
							ctx,
							http.StatusUnprocessableEntity,
							test.message,
							test.details...,
						)
					},
				},
			}, func(context.Context, *validInput) (*struct{}, error) {
				return nil, nil
			})

			response := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/context-error",
				strings.NewReader(`{"value":"valid"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(response, request)

			assert.Equal(t, http.StatusUnprocessableEntity, response.Code)
			assert.Contains(t, response.Body.String(), test.message)
		})
	}
}
