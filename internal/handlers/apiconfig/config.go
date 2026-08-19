package apiconfig

import "github.com/danielgtaylor/huma/v2"

// New описывает API без служебных маршрутов фреймворка.
// Набор публичных путей задан ТЗ, схемы и документация в него не входят.
func New() huma.Config {
	config := huma.DefaultConfig("Gophermart", "1.0.0")
	config.OpenAPIPath = ""
	config.DocsPath = ""
	config.SchemasPath = ""
	// хук добавляет ссылку на описание схемы в тело и заголовок Link
	config.CreateHooks = nil
	config.Transformers = nil

	return config
}
