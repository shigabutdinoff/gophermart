package gophermart

import "flag"

// bindFlags регистрирует флаги на fs и связывает их с полями cfg.
func bindFlags(fs *flag.FlagSet, cfg *Config) {
	fs.StringVar(&cfg.RunAddress, "run-address", cfg.RunAddress, "адрес и порт запуска сервиса")
	fs.StringVar(&cfg.RunAddress, "a", cfg.RunAddress, "адрес и порт запуска сервиса (короткая форма)")
	fs.StringVar(&cfg.DatabaseURI, "database-uri", cfg.DatabaseURI, "строка подключения к PostgreSQL")
	fs.StringVar(&cfg.DatabaseURI, "d", cfg.DatabaseURI, "строка подключения к PostgreSQL (короткая форма)")
	fs.StringVar(&cfg.AccrualAddress, "accrual-system-address", cfg.AccrualAddress, "адрес системы расчёта начислений")
	fs.StringVar(&cfg.AccrualAddress, "r", cfg.AccrualAddress, "адрес системы расчёта начислений (короткая форма)")
	fs.Int64Var(&cfg.RequestBodyLimit, "request-body-limit", cfg.RequestBodyLimit, "максимальный размер распакованного тела запроса в байтах")
	fs.Int64Var(&cfg.RequestBodyLimit, "l", cfg.RequestBodyLimit, "максимальный размер распакованного тела запроса в байтах (короткая форма)")
}

// overrideWithFlags отдаёт приоритет явно заданным флагам над окружением.
func overrideWithFlags(fs *flag.FlagSet, cfg *Config, flags Config) {
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "run-address", "a":
			cfg.RunAddress = flags.RunAddress
		case "database-uri", "d":
			cfg.DatabaseURI = flags.DatabaseURI
		case "accrual-system-address", "r":
			cfg.AccrualAddress = flags.AccrualAddress
		case "request-body-limit", "l":
			cfg.RequestBodyLimit = flags.RequestBodyLimit
		}
	})
}
