package balance

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/zap"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route"
	"github.com/shigabutdinoff/gophermart/internal/money"
	"github.com/shigabutdinoff/gophermart/internal/ordernumber"
)

const balancePath = "/api/user/balance"

const withdrawPath = "/api/user/balance/withdraw"

const withdrawalsPath = "/api/user/withdrawals"

const (
	messageInvalidOrder      = "Неверный номер заказа"
	messageNonPositiveSum    = "Сумма списания должна быть положительной"
	messageInsufficientFunds = "Недостаточно средств"
)

// ReadFunc отдаёт состояние счёта пользователя.
type ReadFunc func(ctx context.Context, userID int64) (domain.Balance, error)

// WithdrawFunc списывает сумму по номеру гипотетического заказа.
type WithdrawFunc func(
	ctx context.Context,
	number string,
	sum money.Points,
	userID int64,
) error

// ListFunc отдаёт историю списаний пользователя.
type ListFunc func(ctx context.Context, userID int64) ([]domain.Withdrawal, error)

// Deps задаёт зависимости маршрутов счёта.
type Deps struct {
	Read     ReadFunc
	Withdraw WithdrawFunc
	List     ListFunc
}

// Options задаёт параметры публикации маршрута баланса.
type Options = route.Options

type balanceView struct {
	Current   money.Points `json:"current"`
	Withdrawn money.Points `json:"withdrawn"`
}

type balanceOutput struct {
	Body balanceView
}

type withdrawPoints money.Points

func (withdrawPoints) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeNumber}
}

func (p *withdrawPoints) UnmarshalJSON(data []byte) error {
	return (*money.Points)(p).UnmarshalJSON(data)
}

type withdrawBody struct {
	Order string         `json:"order" required:"false"`
	Sum   withdrawPoints `json:"sum"`
}

type withdrawInput struct {
	Body withdrawBody
}

type withdrawOutput struct{}

type withdrawalView struct {
	Order       string       `json:"order"`
	Sum         money.Points `json:"sum"`
	ProcessedAt time.Time    `json:"processed_at"`
}

// RegisterRoutes публикует операции счёта.
func RegisterRoutes(api huma.API, logger *zap.Logger, deps Deps, options Options) {
	registerBalanceRoute(api, logger, deps.Read, options)
	registerWithdrawRoute(api, logger, deps.Withdraw, options)
	registerWithdrawalsRoute(api, logger, deps.List, options)
}

func registerBalanceRoute(
	api huma.API,
	logger *zap.Logger,
	read ReadFunc,
	options Options,
) {
	huma.Register(api, huma.Operation{
		OperationID:   "get-balance",
		Method:        http.MethodGet,
		Path:          balancePath,
		Summary:       "Получение текущего баланса пользователя",
		DefaultStatus: http.StatusOK,
		Middlewares:   options.Middlewares,
		Errors: []int{
			http.StatusUnauthorized,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, _ *struct{}) (*balanceOutput, error) {
		userID, err := authorization.RequireUserID(ctx)
		if err != nil {
			return nil, err
		}

		account, err := read(ctx, userID)
		if err != nil {
			return nil, route.InternalError(logger, "Не удалось получить баланс", err)
		}

		return &balanceOutput{Body: balanceView{
			Current:   account.Current,
			Withdrawn: account.Withdrawn,
		}}, nil
	})
}

func registerWithdrawRoute(
	api huma.API,
	logger *zap.Logger,
	withdraw WithdrawFunc,
	options Options,
) {
	huma.Register(api, huma.Operation{
		OperationID:   "withdraw-balance",
		Method:        http.MethodPost,
		Path:          withdrawPath,
		Summary:       "Списание баллов в счёт оплаты заказа",
		Metadata:      apiconfig.ValidationErrorsAsBadRequest(),
		DefaultStatus: http.StatusOK,
		Middlewares:   options.Middlewares,
		Errors: []int{
			http.StatusBadRequest,
			http.StatusUnauthorized,
			http.StatusPaymentRequired,
			http.StatusUnprocessableEntity,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, in *withdrawInput) (*withdrawOutput, error) {
		userID, err := authorization.RequireUserID(ctx)
		if err != nil {
			return nil, err
		}

		err = withdraw(ctx, in.Body.Order, money.Points(in.Body.Sum), userID)
		switch {
		case err == nil:
			return &withdrawOutput{}, nil
		case errors.Is(err, ordernumber.ErrEmpty), errors.Is(err, ordernumber.ErrInvalid):
			return nil, huma.Error422UnprocessableEntity(messageInvalidOrder)
		case errors.Is(err, domain.ErrNonPositiveSum):
			return nil, huma.Error400BadRequest(messageNonPositiveSum)
		case errors.Is(err, domain.ErrInsufficientFunds):
			return nil, huma.Error402PaymentRequired(messageInsufficientFunds)
		default:
			return nil, route.InternalError(logger, "Не удалось списать баллы", err)
		}
	})
}

func registerWithdrawalsRoute(
	api huma.API,
	logger *zap.Logger,
	list ListFunc,
	options Options,
) {
	huma.Register(api, huma.Operation{
		OperationID:   "list-withdrawals",
		Method:        http.MethodGet,
		Path:          withdrawalsPath,
		Summary:       "Получение истории списаний пользователя",
		DefaultStatus: http.StatusOK,
		Middlewares:   options.Middlewares,
		Errors: []int{
			http.StatusUnauthorized,
			http.StatusInternalServerError,
		},
	}, func(ctx context.Context, _ *struct{}) (*route.JSONList, error) {
		userID, err := authorization.RequireUserID(ctx)
		if err != nil {
			return nil, err
		}

		withdrawals, err := list(ctx, userID)
		if err != nil {
			return nil, route.InternalError(logger, "Не удалось получить историю списаний", err)
		}

		response, err := route.JSONOrNoContent(withdrawalViews(withdrawals))
		if err != nil {
			return nil, route.InternalError(logger, "Не удалось собрать историю списаний", err)
		}

		return response, nil
	})
}

func withdrawalViews(withdrawals []domain.Withdrawal) []withdrawalView {
	views := make([]withdrawalView, len(withdrawals))
	for i, withdrawal := range withdrawals {
		views[i] = withdrawalView{
			Order:       withdrawal.Order,
			Sum:         withdrawal.Sum,
			ProcessedAt: withdrawal.ProcessedAt,
		}
	}

	return views
}
