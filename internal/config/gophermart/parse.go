package gophermart

import (
	"errors"
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
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
	values, err := environment()
	if err != nil {
		return Config{}, err
	}
	if err := env.ParseWithOptions(&cfg, env.Options{Environment: values}); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	parsed.apply(&cfg)
	cfg.normalize()

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// environment кладёт непустые переменные процесса поверх значений env-файла.
func environment() (map[string]string, error) {
	values, err := godotenv.Read()
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("load env file: %w", err)
		}
		values = make(map[string]string)
	}

	for name, value := range env.ToMap(os.Environ()) {
		// пустая переменная процесса не затеняет значение из файла
		if value == "" {
			continue
		}
		values[name] = value
	}

	return values, nil
}
