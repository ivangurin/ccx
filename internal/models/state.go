package models

// State is state.json: the active provider/model. Format is 1:1 with
// bash-ccx (statusline.sh reads it — only the profile field) — compatibility
// must survive the migration to the Go binary.
type State struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Updated  string `json:"updated"`
}
