package gophermart

import (
	"errors"
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	"github.com/caarlos0/env/v11"
)

// ErrHelp сообщает, что запрошена справка и описание флагов уже напечатано.
var ErrHelp = arg.ErrHelp

// Parse читает флаги и окружение, явный флаг важнее переменной окружения.
func Parse(args []string) (Config, error) {
	var parsed flags
	// окружение читает env ниже, иначе пустая переменная затенила бы env-файл
	parser, err := arg.NewParser(
		arg.Config{Program: "gophermart", IgnoreEnv: true, Out: os.Stderr},
		&parsed,
	)
	if err != nil {
		return Config{}, fmt.Errorf("build flag parser: %w", err)
	}
	if err := parser.Parse(args); err != nil {
		if errors.Is(err, arg.ErrHelp) {
			parser.WriteHelp(os.Stderr)
		}
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	cfg := Default()
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	parsed.apply(&cfg)
	cfg.normalize()

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}
