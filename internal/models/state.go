package models

// State — ccx-state.json: активный профиль/модель. Формат 1:1 с bash-ccx
// (его читает statusline.sh — только поле profile) — совместимость обязана
// пережить миграцию на Go-бинарник.
type State struct {
	Profile string `json:"profile"`
	Model   string `json:"model"`
	Updated string `json:"updated"`
}
