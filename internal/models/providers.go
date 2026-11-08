package models

import "encoding/json"

// Daemon holds the local ccx daemon's launch parameters: the listen port
// and the debug flag (both optional — absent/0/false at resolution time).
// The block is SWITCHER-OWNED: the switcher reads it and spawns the daemon
// with --port/--debug; the daemon itself never reads it (its flags are the
// single input). There is no daemon-owned secret anymore: openai
// credentials are pass-through (the token Claude Code sends travels
// upstream), copilot/codex own theirs in the translators; the surface is
// localhost-only.
type Daemon struct {
	Port  int  `json:"port,omitempty"`
	Debug bool `json:"debug,omitempty"`
}

// Providers is the root of providers.json (unversioned). The provider
// map's file key is "providers"; files from older releases use "profiles" —
// both load forever (decoder-level backward compat), ccx never writes
// "profiles".
type Providers struct {
	Daemon    *Daemon    `json:"daemon,omitempty"`
	Providers ProfileMap `json:"providers"`
}

// ProfileMap is providers in file order (see OrderedMap).
type ProfileMap = OrderedMap[*Profile]

// DaemonPort is daemon.port, nil-safe (no daemon block = 0 = the default at
// resolution time). Keeps the port-resolution call sites simple.
func (p *Providers) DaemonPort() int {
	if p.Daemon == nil {
		return 0
	}

	return p.Daemon.Port
}

// DaemonDebug is daemon.debug, nil-safe (no daemon block = no debug).
func (p *Providers) DaemonDebug() bool {
	return p.Daemon != nil && p.Daemon.Debug
}

// UnmarshalJSON — why custom: the legacy "profiles" key must keep loading
// forever, and the choice must be made on KEY PRESENCE (an explicit empty
// "providers": {} must not be overridden by a populated legacy "profiles"),
// which a second struct field cannot express. Decoding the winner as a
// RawMessage also means garbage under the UNUSED key never fails the load.
func (p *Providers) UnmarshalJSON(b []byte) error {
	var raw struct {
		Providers json.RawMessage `json:"providers"`
		Legacy    json.RawMessage `json:"profiles"`
		Daemon    *Daemon         `json:"daemon"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	src := raw.Providers
	if len(src) == 0 {
		src = raw.Legacy
	}
	p.Providers = ProfileMap{}
	if len(src) > 0 {
		if err := json.Unmarshal(src, &p.Providers); err != nil {
			return err
		}
	}
	p.Daemon = raw.Daemon
	// An empty daemon block is semantically "no block": normalizing at decode
	// time guarantees every re-save omits the key (a value struct with
	// omitempty can never be omitted — hence the pointer). A block with only
	// debug:false is as empty as one with only port:0.
	if p.Daemon != nil && p.Daemon.Port == 0 && !p.Daemon.Debug {
		p.Daemon = nil
	}

	return nil
}
