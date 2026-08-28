package balance

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"go.uber.org/zap"

	domain "github.com/shigabutdinoff/gophermart/internal/balance"
	"github.com/shigabutdinoff/gophermart/internal/handlers/middleware/authorization"
	"github.com/shigabutdinoff/gophermart/internal/handlers/route"
	"github.com/shigabutdinoff/gophermart/internal/money"
)

const balancePath = "/api/user/balance"

// ReadFunc отдаёт состояние счёта пользователя.
type ReadFunc func(ctx context.Context, userID int64) (domain.Balance, error)

// Deps задаёт зависимость маршрута чтения баланса.
type Deps struct {
	Read ReadFunc
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

// RegisterRoutes публикует чтение баланса как huma-операцию.
func RegisterRoutes(api huma.API, logger *zap.Logger, deps Deps, options Options) {
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

		account, err := deps.Read(ctx, userID)
		if err != nil {
			return nil, route.InternalError(logger, "Не удалось получить баланс", err)
		}

		return &balanceOutput{Body: balanceView{
			Current:   account.Current,
			Withdrawn: account.Withdrawn,
		}}, nil
	})
}
