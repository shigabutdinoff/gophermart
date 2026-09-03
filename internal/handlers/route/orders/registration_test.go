package orders

import (
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
)

func TestRouteHelpersRegisterOnlyOwnMethod(t *testing.T) {
	t.Run("загрузка", func(t *testing.T) {
		api := apiconfig.NewAPI(chi.NewRouter())

		registerUploadRoute(api, zap.NewNop(), nil, Options{})

		path := api.OpenAPI().Paths[ordersPath]
		require.NotNil(t, path)
		assert.NotNil(t, path.Post)
		assert.Nil(t, path.Get)
	})

	t.Run("список", func(t *testing.T) {
		api := apiconfig.NewAPI(chi.NewRouter())

		registerListRoute(api, zap.NewNop(), nil, Options{})

		path := api.OpenAPI().Paths[ordersPath]
		require.NotNil(t, path)
		assert.Nil(t, path.Post)
		assert.NotNil(t, path.Get)
	})
}
