package apiconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewHidesFrameworkRoutes(t *testing.T) {
	config := New()

	assert.Empty(t, config.OpenAPIPath)
	assert.Empty(t, config.DocsPath)
	assert.Empty(t, config.SchemasPath)
	assert.Empty(t, config.CreateHooks)
	assert.Empty(t, config.Transformers)
}
