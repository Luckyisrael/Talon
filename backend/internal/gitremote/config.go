// Package gitremote is the premium GitHub integration: it connects a user's
// GitHub account over OAuth, remembers which repository each project pushes to,
// and pushes the project's workspace up over HTTPS in an isolated subprocess.
//
// The OAuth access token is the only real secret here. It is encrypted at rest
// (see internal/secrets) and never logged, never written into .git/config, and
// never persisted into the push URL — it lives only in memory for the moment a
// push runs. Everything credential-dependent degrades to a clear "not
// configured" state when the OAuth app is absent, so the surrounding product
// runs unchanged until the operator supplies credentials.
package gitremote

import (
	"log"
	"os"
	"strings"

	"github.com/luckyisrael/talon/backend/internal/secrets"
)

// Config holds the OAuth application settings and the cipher that seals tokens.
// A zero-value ClientID/ClientSecret means the integration is not configured;
// callers gate on Configured() and present the "connect" surfaces accordingly.
type Config struct {
	ClientID     string
	ClientSecret string
	CallbackURL  string
	AppBaseURL   string
	cipher       *secrets.Cipher
}

const (
	defaultCallbackURL = "http://localhost:8080/v1/git/github/callback"
	defaultAppBaseURL  = "http://localhost:3000"
	// The encryption passphrase gates a real credential store, so in production
	// it must carry real entropy. This mirrors the auth package's bar.
	minEncryptionKeyLen = 32
)

// Load reads the integration's configuration from the environment and derives
// the token cipher.
//
// The fail-closed rule is deliberately narrow: a missing or weak encryption key
// is fatal in production ONLY when the OAuth app is actually configured (client
// id and secret both present). An operator who has not set up GitHub at all —
// the default state — gets a running server with the integration dark, not a
// crash. An operator who HAS wired up GitHub but left the token key unset or
// weak would otherwise be storing user tokens under a predictable dev key, so
// that combination stops the server before it can persist a single one.
func Load() Config {
	clientID := strings.TrimSpace(os.Getenv("GITHUB_OAUTH_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("GITHUB_OAUTH_CLIENT_SECRET"))
	configured := clientID != "" && clientSecret != ""

	callback := strings.TrimSpace(os.Getenv("GIT_OAUTH_CALLBACK_URL"))
	if callback == "" {
		callback = defaultCallbackURL
	}
	appBase := strings.TrimSpace(os.Getenv("APP_BASE_URL"))
	if appBase == "" {
		appBase = defaultAppBaseURL
	}

	key := strings.TrimSpace(os.Getenv("GIT_TOKEN_ENCRYPTION_KEY"))
	switch {
	case len(key) >= minEncryptionKeyLen:
		// Good key — use it whether or not the OAuth app is configured.
	case configured && isProduction():
		// Configured to store tokens, in production, with no real key: refuse.
		log.Fatal("gitremote: GITHUB_OAUTH_* is set but GIT_TOKEN_ENCRYPTION_KEY is missing or shorter than 32 chars; refusing to store OAuth tokens under a weak key in production")
	case configured:
		log.Println("gitremote: WARNING — GIT_TOKEN_ENCRYPTION_KEY is weak or unset; using an insecure development key. Set a 32+ char key before production.")
		if key == "" {
			key = "insecure-development-git-token-key-do-not-ship"
		}
	default:
		// Not configured: the cipher is never exercised, but we still build a
		// usable one so the store layer has a non-nil dependency.
		if key == "" {
			key = "insecure-development-git-token-key-do-not-ship"
		}
	}

	return Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		CallbackURL:  callback,
		AppBaseURL:   appBase,
		cipher:       secrets.FromPassphrase(key),
	}
}

// Configured reports whether the OAuth application credentials are present. When
// false, the handlers advertise the integration as unavailable rather than
// starting a flow that could only fail at GitHub.
func (c Config) Configured() bool {
	return c.ClientID != "" && c.ClientSecret != ""
}

// Cipher exposes the token cipher for the store layer to seal and open tokens.
func (c Config) Cipher() *secrets.Cipher {
	return c.cipher
}

// isProduction mirrors internal/auth: treat the usual deploy environments as
// production so a real deployment cannot fall back to a development key.
func isProduction() bool {
	env := strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV")))
	if env == "" {
		env = strings.ToLower(strings.TrimSpace(os.Getenv("ENV")))
	}
	switch env {
	case "production", "prod", "staging":
		return true
	default:
		return false
	}
}
