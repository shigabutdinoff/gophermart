package orders

import (
	"testing"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/shigabutdinoff/gophermart/internal/handlers/apiconfig"
)

func TestRouteHelpersRegisterOnlyOwnMethod(t *testing.T) {
	t.Run("загрузка", func(t *testing.T) {
		api := humachi.New(chi.NewRouter(), apiconfig.New())

		registerUploadRoute(api, zap.NewNop(), nil, nil)

		path := api.OpenAPI().Paths[ordersPath]
		require.NotNil(t, path)
		assert.NotNil(t, path.Post)
		assert.Nil(t, path.Get)
	})

	t.Run("список", func(t *testing.T) {
		api := humachi.New(chi.NewRouter(), apiconfig.New())

		registerListRoute(api, zap.NewNop(), nil, nil)

		path := api.OpenAPI().Paths[ordersPath]
		require.NotNil(t, path)
		assert.Nil(t, path.Post)
		assert.NotNil(t, path.Get)
	})
}
