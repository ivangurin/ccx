package models

// Daemon — параметры локального демона ccx (единственный HTTP-эндпоинт,
// на который указывает ANTHROPIC_BASE_URL; auth_token — статический токен
// клиента: и для Claude Code → демон, и для admin-API).
type Daemon struct {
	Port      int    `json:"port"`
	AuthToken string `json:"auth_token"`
}

// Providers — корень providers.json v2.
type Providers struct {
	Version  int        `json:"version"`
	Daemon   Daemon     `json:"daemon"`
	Profiles ProfileMap `json:"profiles"`
}

// ProfileMap — профили в порядке файла (см. OrderedMap).
type ProfileMap = OrderedMap[*Profile]
