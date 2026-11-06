package models

// Daemon holds the local ccms daemon's only parameter: the listen port
// (optional — absent/0 means the default 4567 at resolution time). There is
// no daemon-owned secret anymore: openai credentials are pass-through (the
// token Claude Code sends travels upstream), copilot/codex own theirs in
// the translators; the surface is localhost-only.
type Daemon struct {
	Port int `json:"port,omitempty"`
}

// Providers is the root of providers.json (unversioned: the layout is flat
// profiles; a legacy "version" field in the file is ignored by the decoder).
type Providers struct {
	Daemon   Daemon     `json:"daemon"`
	Profiles ProfileMap `json:"profiles"`
}

// ProfileMap is profiles in file order (see OrderedMap).
type ProfileMap = OrderedMap[*Profile]
