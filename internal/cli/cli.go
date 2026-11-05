// Package cli — stdlib-диспетчер команд ccx. Команды — простые глаголы
// с 0–2 аргументами; cobra не нужен (why: минимум зависимостей, команды
// портируются из bash-ccx 1:1 вместе с тестами).
package cli

import (
	"fmt"
	"io"
	"strings"
)

// Version подставляется при сборке (-X ...cli.Version=...), dev — локально.
var Version = "dev"

const usage = `ccx — переключение LLM-провайдеров Claude Code

Использование:
  ccx                    интерактивное меню: профиль → модель
  ccx use <профиль> [модель]
  ccx list               профили и модели
  ccx status             активный профиль/модель, состояние демона
  ccx init [--force]     создать providers.json (импорт/миграция)
  ccx edit               открыть providers.json в $EDITOR
  ccx serve              демон (foreground, отладка)
  ccx stop               остановить демон
  ccx auth copilot       OAuth GitHub device flow
  ccx auth codex         проверить логин codex CLI
  ccx version            версия
  ccx help               эта справка
`

// Execute исполняет команду; возвращает код выхода процесса.
func Execute(args []string, stdout io.Writer) int {
	if len(args) == 0 {
		return runMenu(args, stdout)
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "ccx %s\n", Version)

		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)

		return 0
	case "init":
		return runInit(args, stdout)
	case "list", "-l":
		return runList(args, stdout)
	case "status", "-s":
		return runStatus(args, stdout)
	case "edit", "-e":
		return runEdit(args, stdout)
	case "use":
		return runUse(args, stdout)
	case "serve":
		return runServe(args, stdout)
	case "stop":
		return runStop(args, stdout)
	default:
		// не команда — вероятно, профиль: ccx zai / ccx zai glm-5.3 (паритет bash)
		if !strings.HasPrefix(args[0], "-") {
			useArgs := append([]string{"use", args[0]}, args[1:]...)

			return runUse(useArgs, stdout)
		}
		fmt.Fprintf(stdout, "ccx: неизвестная команда %q\n\n%s", args[0], usage)

		return 1
	}
}
