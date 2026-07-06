package gophermart

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/caarlos0/env/v11"
)

// Parse читает флаги и окружение, явный флаг важнее переменной окружения.
func Parse(args []string) (Config, error) {
	cfg := Default()
	fs := flag.NewFlagSet("gophermart", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bindFlags(fs, &cfg)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(os.Stderr)
			fs.Usage()
		}
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}
	// Снимок значений флагов до применения окружения
	flags := cfg
	if err := env.Parse(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse environment: %w", err)
	}
	overrideWithFlags(fs, &cfg, flags)
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
