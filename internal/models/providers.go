package models

// Daemon holds the local ccx daemon's parameters (the single HTTP endpoint
// ANTHROPIC_BASE_URL points to; auth_token is the static client token:
// used both for Claude Code → daemon and for the admin API).
type Daemon struct {
	Port      int    `json:"port"`
	AuthToken string `json:"auth_token"`
}

// Providers is the root of providers.json v2.
type Providers struct {
	Version  int        `json:"version"`
	Daemon   Daemon     `json:"daemon"`
	Profiles ProfileMap `json:"profiles"`
}

// ProfileMap is profiles in file order (see OrderedMap).
type ProfileMap = OrderedMap[*Profile]
