package models

// State is ccms-state.json: the active profile/model. Format is 1:1 with
// bash-ccx (statusline.sh reads it — only the profile field) — compatibility
// must survive the migration to the Go binary.
type State struct {
	Profile string `json:"profile"`
	Model   string `json:"model"`
	Updated string `json:"updated"`
}
