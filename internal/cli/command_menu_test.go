package cli

import (
	"testing"

	"github.com/ivangurin/ccms/internal/models"
	"github.com/stretchr/testify/require"
)

// menuTestProviders — two profiles: the first with two models, the second
// without any (Keys order = insertion order: "withmodels", "bare").
func menuTestProviders() *models.Providers {
	p := &models.Providers{Daemon: models.Daemon{Port: 4567}}
	p.Profiles.Set("withmodels", &models.Profile{
		Name:     "Has models",
		Protocol: models.ProtocolAnthropic,
		Models: []models.Model{
			{ID: "m-one", Label: "One", Default: true},
			{ID: "m-two"},
		},
	})
	p.Profiles.Set("bare", &models.Profile{Name: "No models", Protocol: models.ProtocolAnthropic})

	return p
}

func TestMenuStages_EndsAfterModelLevel(t *testing.T) {
	next := menuStages(menuTestProviders(), 0)

	stage, ok := next(nil)
	require.True(t, ok)
	require.Equal(t, "Choose a provider", stage.Header)

	stage, ok = next([]int{0})
	require.True(t, ok)
	require.Equal(t, "Choose a model · withmodels", stage.Header)

	// the chain is complete after provider+model: ok=false lets the session
	// finish instead of pushing a third model menu (the reported bug)
	_, ok = next([]int{0, 1})
	require.False(t, ok, "the chain must end after provider+model")
}

func TestMenuStages_ProfileWithoutModelsEndsChain(t *testing.T) {
	next := menuStages(menuTestProviders(), 0)

	_, ok := next([]int{1})

	require.False(t, ok, "a profile without models ends the chain — runMenu reports it")
}
