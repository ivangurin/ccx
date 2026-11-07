package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"github.com/ivangurin/ccx/internal/clients/codex"
	copilot_client "github.com/ivangurin/ccx/internal/clients/copilot"
	"github.com/ivangurin/ccx/internal/config"
	"github.com/ivangurin/ccx/internal/models"
)

// copilotAuthClient is the device-flow slice of the copilot client the auth
// command needs (a test seam — see newCopilotAuthClient).
type copilotAuthClient interface {
	StartDeviceFlow(ctx context.Context) (*copilot_client.DeviceFlowResult, error)
	PollDeviceFlow(ctx context.Context, deviceCode string) (string, error)
}

// newCopilotAuthClient builds the device-flow client over a store path
// (var for tests — the same seam pattern as copilotQuota in command_status).
var newCopilotAuthClient = func(store string) copilotAuthClient {
	return copilot_client.New(store)
}

// codexLogin runs the ChatGPT OAuth login into an auth store (var for
// tests; the default is the codex client's Login).
var codexLogin = codex.Login

// runAuth — ccx auth: the login flow of the named provider (copilot device
// flow, codex OAuth PKCE); without arguments it lists every provider's auth
// state. Credentials live per provider: <CCX_HOME>/auth/<key>.json.
func runAuth(args []string, stdout io.Writer) int {
	cfg, err := config.NewFromEnv()
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	if len(args) > 2 {
		fmt.Fprintln(stdout, "ccx: usage: ccx auth [provider]")

		return 1
	}

	providers, code := loadProvidersOrHint(cfg, stdout)
	if providers == nil {
		return code
	}

	if len(args) == 1 {
		return listAuthState(cfg, providers, stdout)
	}

	key := args[1]
	profile := providers.Providers.Get(key)
	if profile == nil {
		fmt.Fprintf(stdout, "ccx: unknown provider %q\n", key)

		return 1
	}

	switch profile.Protocol {
	case models.ProtocolCopilot:
		return authCopilot(cfg, key, stdout)
	case models.ProtocolCodex:
		return authCodex(cfg, key, stdout)
	default:
		fmt.Fprintf(stdout, "ccx: provider %s (protocol %s) does not support auth\n", key, profile.Protocol)

		return 1
	}
}

// listAuthState — one line per provider: authorized / not authorized for
// the OAuth protocols, "auth not supported" for the rest. A local file
// probe only — no network in a listing.
func listAuthState(cfg *config.Config, providers *models.Providers, stdout io.Writer) int {
	for _, key := range providers.Providers.Keys {
		profile := providers.Providers.Get(key)
		state := "auth not supported"
		switch profile.Protocol {
		case models.ProtocolCopilot:
			if copilot_client.Authenticated(cfg.AuthStorePath(key)) {
				state = "authorized"
			} else {
				state = "not authorized"
			}
		case models.ProtocolCodex:
			if codex.Authenticated(cfg.AuthStorePath(key)) {
				state = "authorized"
			} else {
				state = "not authorized"
			}
		default:
			// state stays "auth not supported"
		}
		fmt.Fprintf(stdout, "  %s (%s) — %s\n", key, profile.Protocol, state)
	}

	return 0
}

// authCopilot — the GitHub OAuth device flow into the provider's store.
func authCopilot(cfg *config.Config, key string, stdout io.Writer) int {
	store := cfg.AuthStorePath(key)
	client := newCopilotAuthClient(store)

	res, err := client.StartDeviceFlow(context.Background())
	if err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintln(stdout, "Open in browser: "+res.VerificationURI)
	fmt.Fprintln(stdout, "Enter code:      "+res.UserCode)
	fmt.Fprintln(stdout, "Waiting for confirmation...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if _, err := client.PollDeviceFlow(ctx, res.DeviceCode); err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintf(stdout, "Done: tokens saved to %s\n", store)

	return 0
}

// authCodex — the ChatGPT OAuth login into the provider's store.
func authCodex(cfg *config.Config, key string, stdout io.Writer) int {
	store := cfg.AuthStorePath(key)
	browserOpener := func(url string) error {
		// always printed: the exec below may fail (SSH, no opener) — the URL
		// stays on screen for a manual open
		fmt.Fprintln(stdout, "Open in browser: "+url)

		return openBrowser(url)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if err := codexLogin(ctx, store, browserOpener); err != nil {
		fmt.Fprintf(stdout, "ccx: %v\n", err)

		return 1
	}
	fmt.Fprintf(stdout, "Done: login saved to %s\n", store)

	return 0
}

// openBrowser opens the URL in the desktop browser (darwin `open`, the
// XDG world's `xdg-open`).
func openBrowser(url string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}

	return launchOpener(opener, url)
}

// launchOpener starts the opener and reaps it in the background: `open`
// exits right after handing off to the browser, and an unreaped child would
// sit as a zombie for the login's whole 15-minute wait. A blocking Wait
// would tie the login to the opener's lifetime — the goroutine keeps the
// login independent.
func launchOpener(opener, url string) error {
	cmd := exec.Command(opener, url) //nolint:gosec // fixed opener, argument is a URL
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()

	return nil
}
