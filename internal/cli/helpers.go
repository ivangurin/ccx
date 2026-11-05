package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

// loadProvidersOrHint читает providers.json; при отсутствии/устаревании
// печатает подсказку и возвращает nil (выход — 0: паритет bash-семантики
// «подсказка вместо ошибки»).
func loadProvidersOrHint(cfg *config.Config, stdout io.Writer) *models.Providers {
	providers, err := config.LoadProviders(cfg.ProvidersPath())
	if err == nil {
		return providers
	}
	if errors.Is(err, config.ErrUnsupportedVersion) {
		fmt.Fprintln(stdout, "providers.json старого формата (v1) — выполните ccx init для миграции")

		return nil
	}
	fmt.Fprint(stdout, "ccx: providers.json не найден — сначала ccx init\n")

	return nil
}
