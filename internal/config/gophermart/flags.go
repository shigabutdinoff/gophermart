package gophermart

// flags описывает только флаги, окружение читает Parse отдельным слоем.
// Указатели отличают явно заданный флаг от опущенного, включая пустое значение.
type flags struct {
	RunAddress     *string `arg:"-a,--run-address" help:"адрес и порт запуска сервиса"`
	DatabaseURI    *string `arg:"-d,--database-uri" help:"строка подключения к PostgreSQL"`
	AccrualAddress *string `arg:"-r,--accrual-system-address" help:"адрес системы расчёта начислений"`
}

// apply отдаёт приоритет явно заданным флагам над значениями окружения.
func (f flags) apply(cfg *Config) {
	if f.RunAddress != nil {
		cfg.RunAddress = *f.RunAddress
	}
	if f.DatabaseURI != nil {
		cfg.DatabaseURI = *f.DatabaseURI
	}
	if f.AccrualAddress != nil {
		cfg.AccrualAddress = *f.AccrualAddress
	}
}
