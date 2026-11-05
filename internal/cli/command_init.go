package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
)

// runInit — ccx init [--force]: создать/мигрировать providers.json.
func runInit(args []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	force := len(args) > 1 && args[1] == "--force"

	res, err := config.InitProviders(cfg, force)
	if err != nil {
		if errors.Is(err, config.ErrProvidersExist) {
			// паритет bash-ccx: не ошибка, а подсказка
			fmt.Fprintf(stdout, "providers.json уже существует: %s\n", cfg.ProvidersPath())
			fmt.Fprintln(stdout, "Используйте 'ccx init --force' для перегенерации (текущий файл будет заменён).")

			return 0
		}
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}

	fmt.Fprintf(stdout, "Создан %s (права 600).\n", res.Path)
	if res.Migrated {
		fmt.Fprintln(stdout, "Выполнена миграция v1 → v2 (proxy-команды → protocol).")
	}
	fmt.Fprintln(stdout, "Профили:")
	for _, key := range res.Profiles {
		fmt.Fprintf(stdout, "  %s\n", key)
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(stdout, "  ⚠ %s\n", w)
	}
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Проверьте токены и списки моделей: ccx edit")

	return 0
}
