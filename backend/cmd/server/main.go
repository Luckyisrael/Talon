package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/luckyisrael/talon/backend/internal/ai"
	"github.com/luckyisrael/talon/backend/internal/api"
	"github.com/luckyisrael/talon/backend/internal/auth"
	"github.com/luckyisrael/talon/backend/internal/billing"
	"github.com/luckyisrael/talon/backend/internal/blobstore"
	"github.com/luckyisrael/talon/backend/internal/composer"
	"github.com/luckyisrael/talon/backend/internal/devclient"
	"github.com/luckyisrael/talon/backend/internal/gitremote"
	"github.com/luckyisrael/talon/backend/internal/harness/compaction"
	"github.com/luckyisrael/talon/backend/internal/harness/composition"
	"github.com/luckyisrael/talon/backend/internal/harness/instructions"
	"github.com/luckyisrael/talon/backend/internal/harness/skills"
	"github.com/luckyisrael/talon/backend/internal/knowledge"
	"github.com/luckyisrael/talon/backend/internal/logx"
	"github.com/luckyisrael/talon/backend/internal/notify"
	"github.com/luckyisrael/talon/backend/internal/orchestrator"
	"github.com/luckyisrael/talon/backend/internal/redroid"
	"github.com/luckyisrael/talon/backend/internal/skr"
	"github.com/luckyisrael/talon/backend/internal/store"
	"github.com/luckyisrael/talon/backend/internal/workspace"
	"github.com/luckyisrael/talon/backend/internal/ws"

	_ "modernc.org/sqlite"
)

func main() {
	godotenv.Load()

	// H12/W14.4: --dump-config resolves the composition (base → profile →
	// preset, LWW) and prints it as JSON, then exits before anything is
	// opened — no database, no AI client, no listener — so the CI golden
	// diff runs secret-free. It doubles as the human answer to "what did
	// this boot actually wire?".
	dumpConfig := flag.Bool("dump-config", false,
		"print the resolved composition (base → profile → preset, LWW) as JSON and exit")
	flag.Parse()
	if *dumpConfig {
		if err := dumpComposition("server"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./talon.db"
	}

	projectRoot := findProjectRoot()
	templateDir := resolveDir("TEMPLATE_DIR",
		filepath.Join(projectRoot, "template"),
		filepath.Join(projectRoot, "backend", "template"))
	workspaceDir := resolveDir("WORKSPACE_DIR",
		filepath.Join(projectRoot, "workspaces"),
		filepath.Join(projectRoot, "workspaces"))

	log.Printf("template dir:  %s", templateDir)
	log.Printf("workspace dir: %s", workspaceDir)

	s, err := store.New(dbPath)
	if err != nil {
		log.Fatalf("could not open the database at %s: %v", dbPath, err)
	}
	defer s.Close()

	// Export jobs left running/queued by a previous process can never finish —
	// their gradle run died with it. Fail them loudly at boot so no job sits
	// in a terminally hopeful state.
	if n, err := s.FailStaleExportJobs(); err != nil {
		log.Printf("WARNING: could not sweep stale export jobs: %v", err)
	} else if n > 0 {
		log.Printf("marked %d stale export job(s) as failed (server restart)", n)
	}

	// Agent turns left running/waiting/paused by a previous process can
	// never finish either — their in-memory session state (probe counts,
	// cooldowns, task tracker latches, crash index) died with it. Mark them
	// interrupted so resume is honest about what was lost instead of
	// pretending the loop paused gracefully.
	if ids, err := s.MarkInterruptedPrompts(); err != nil {
		log.Printf("WARNING: could not sweep interrupted prompts: %v", err)
	} else if len(ids) > 0 {
		log.Printf("marked %d prompt(s) interrupted by restart: %v", len(ids), ids)
	}

	authSvc := auth.New()

	// A misconfigured AI provider is fatal: the whole product is the agent, and
	// starting up only to fail on the first prompt hides the real problem.
	aiClient, err := ai.New()
	if err != nil {
		log.Fatalf("AI provider is not configured: %v", err)
	}
	log.Printf("AI provider ready (model %s)", aiClient.Model())

	// The knowledge layer (always-on rules + load-on-demand skills) is embedded in
	// the binary, so Load only fails on malformed authored content — which a unit
	// test guards against. We still degrade rather than die: a build that boots and
	// serves without rules beats one that refuses to start, and the log makes the
	// gap loud.
	knowledge, err := knowledge.Load(filepath.Join(templateDir, "package.json"))
	if err != nil {
		log.Printf("WARNING: agent knowledge layer unavailable: %v — builds will run without rules and skills", err)
	} else {
		log.Printf("agent knowledge ready (%d skills)", len(knowledge.SkillNames()))
	}

	orchestratorSvc := orchestrator.New(aiClient, s, knowledge)

	// H12/W14.2: main is a composer — the composition file is the wiring
	// source, env defaults are only its seed (the settings-document
	// precedent: data beats restart). Absent file: comp is nil and the
	// boot is byte-identical. A malformed row or an unknown component
	// fails the boot — wiring you cannot trust must not serve.
	if comp, err := composer.Load("server"); err != nil {
		log.Fatalf("composition: %v", err)
	} else if comp != nil {
		applied, err := composer.Apply(comp, orchestratorSvc)
		if err != nil {
			log.Fatalf("composition: %v", err)
		}
		log.Printf("composition: %s (profile %q, preset %q) applied %d row(s): %s",
			comp.Path, comp.Profile, comp.Preset, len(applied), strings.Join(applied, ", "))
	}

	// H12: model settings become data. The settings document rides beside
	// the database; ROUTING_POLICY_JSON remains the env seed, and edits to
	// the file flip routing on the next turn — no restart. A malformed file
	// holds the last good copy and logs loudly.
	ai.AttachSettingsFile(filepath.Join(filepath.Dir(dbPath), "settings.json"))

	// H6: the multi-source skill catalog — embedded knowledge is
	// authoritative, and each workspace's .talon/skills merge in (winner per
	// name: embedded). The catalog loads at construction (FM-H6a — an
	// unloaded catalog rendered a skills section with zero skills and made
	// read_skill answer "none"); Reload is the refresh seam. Workspace
	// skills are listed to the model but their bodies load only through
	// read_skill, same trust boundary as today.
	skillCatalog := skills.NewCatalog(
		skills.EmbeddedProvider{Registry: knowledge},
		skills.RegistryProvider{
			Pins:     skills.ParsePluginPins(os.Getenv("TALON_PLUGINS")),
			CacheDir: filepath.Join(workspaceDir, "..", "plugin-cache"),
		},
	)

	// H6: the AGENTS.md instructions chain factory — the user's global
	// conventions ($TALON_HOME/AGENTS.md or ~/.talon/AGENTS.md) plus each
	// project's own AGENTS.md files, budgeted, re-read on every enrichment.
	talonHome := os.Getenv("TALON_HOME")
	if talonHome == "" {
		if home, err := os.UserHomeDir(); err == nil {
			talonHome = filepath.Join(home, ".talon")
		}
	}
	globalAGENTS := filepath.Join(talonHome, "AGENTS.md")
	orchestratorSvc.AttachInstructions(func(workspacePath string) string {
		return instructions.Loader{WorkspaceRoot: workspacePath, GlobalPath: globalAGENTS}.Load().Text()
	})
	orchestratorSvc.AttachSkills(skillCatalog)
	// Per-project plugin pins: each workspace's .talon/plugins.json resolves
	// through the same verified provider and cache, memoized 5 minutes per
	// workspace so manifest renders never hit the network. The pin file is
	// the approval; resolutions are logged, which is the audit trail.
	orchestratorSvc.AttachPluginRegistry(skills.RegistryProvider{
		CacheDir: filepath.Join(workspaceDir, "..", "plugin-cache"),
	}, 5*time.Minute)

	// H8: the spill store files oversized tool outputs (from run_shell,
	// inspect_package, and friends) under a locator instead of truncating
	// them destructively. Rooted beside the database so it shares its
	// lifetime; a failure to create degrades to today's hard cap, logged.
	// Hoisted out of the if: the same store is attached to the prompt
	// handler below so read_file can resolve the locators back (W1.2).
	var spillStore *compaction.SpillStore
	if ss, err := compaction.NewSpillStore(filepath.Join(filepath.Dir(dbPath), "spill")); err != nil {
		log.Printf("WARNING: spill store unavailable (%v) — oversized outputs keep the hard cap", err)
	} else {
		spillStore = ss
		orchestratorSvc.AttachSpill(spillStore)
	}

	portAlloc := redroid.NewPortAllocator(15555, 16555)
	// DV2: the phone-relay range — disjoint from the containers' so a relay
	// port can never collide with an ADB port.
	relayAlloc := redroid.NewPortAllocator(17555, 18555)
	maxContainers := envInt("MAX_CONTAINERS", 5)
	// DV4: the warm pool — containers booted ahead of demand, claimed on
	// create. One warm slot by default (a warm container holds 1GB/1CPU
	// while idle); 0 disables (always cold, the pre-DV4 path).
	warmPoolSize := envInt("WARM_POOL_SIZE", 1)

	containerMgr, err := redroid.NewManager(portAlloc, maxContainers)
	if err != nil {
		log.Printf("WARNING: container manager unavailable: %v — sessions will run without a device", err)
	} else {
		if err := containerMgr.Reconcile(s); err != nil {
			log.Printf("WARNING: could not reconcile running containers: %v", err)
		}
		containerMgr.StartCleanupLoop(s, 5*time.Minute)
		log.Printf("container manager ready (max %d devices)", maxContainers)
	}

	wsMgr, err := workspace.New(templateDir, workspaceDir)
	if err != nil {
		log.Printf("WARNING: workspace manager unavailable: %v — new projects will start empty", err)
	}

	devClientMgr, err := devclient.NewManager(templateDir)
	if err != nil {
		log.Printf("WARNING: dev-client manager unavailable: %v — the dev client will not be installed", err)
	} else {
		log.Printf("dev-client manager ready (template v%s)", devClientMgr.Allowlist().TemplateVersion)
	}

	walletMgr, err := devclient.NewWalletManager(templateDir)
	if err != nil {
		log.Printf("WARNING: wallet manager unavailable: %v — the mock wallet will not be installed", err)
	} else if !walletMgr.IsInstalled() {
		log.Printf("WARNING: the mock wallet APK is missing — run build-mock-wallet.sh to enable wallet testing")
	}

	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		log.Fatalf("could not create the workspace directory %s: %v", workspaceDir, err)
	}

	// OAuth sign-in (D): providers are dark until credentials exist, exactly
	// like gitremote. The callback base follows the public origin so a phone
	// on the LAN goes through the same flow as the browser.
	oauthBase := os.Getenv("AUTH_CALLBACK_BASE")
	if oauthBase == "" {
		if host := os.Getenv("PUBLIC_HOST"); host != "" {
			oauthBase = "http://" + host + ":8080"
		} else {
			oauthBase = "http://localhost:8080"
		}
	}
	oauthProviders := auth.OAuthProviderConfig(oauthBase)
	if len(oauthProviders) > 0 {
		names := make([]string, 0, len(oauthProviders))
		for p := range oauthProviders {
			names = append(names, string(p))
		}
		log.Printf("auth: OAuth sign-in enabled for %s", strings.Join(names, ", "))
	} else {
		log.Print("auth: no OAuth providers configured — set AUTH_GITHUB_CLIENT_ID/SECRET or AUTH_GOOGLE_CLIENT_ID/SECRET to enable sign-in")
	}
	// E: push notifications. OneSignal when fully configured, the honest
	// Noop otherwise; the subscriber resolver is the store's push-target
	// lookup. Dark mode is named in the log, never silent. Built before the
	// auth handlers so sign-in alerts ride the real notifier, not the dark.
	pushNotifier := notify.Get(func(ctx context.Context, userID int64) (string, error) {
		return s.GetOneSignalID(userID)
	})
	oauthHandler := api.NewOAuthAuthHandler(s, authSvc, oauthProviders, pushNotifier)
	projectHandler := api.NewProjectHandler(s, wsMgr, workspaceDir)
	exportHandler := api.NewExportHandler(s, api.LoadReleaseCipher(), workspaceDir, templateDir, filepath.Join(workspaceDir, "..", "exports"))
	log.Printf("release export ready (keystores + submission bundles)")
	triageHandler := api.NewTriageHandler(s)
	promptHandler := api.NewPromptHandler(s, aiClient, workspaceDir, templateDir, orchestratorSvc, devClientMgr, knowledge, pushNotifier)
	// Prompt attachments: images to Cloudflare R2 when configured, local
	// disk otherwise. Docs always stay local (small text, already inlined
	// into the transcript).
	if r2, ok := blobstore.NewR2StoreFromEnv(); ok {
		promptHandler.SetR2Store(r2)
		log.Print("attachments: images to Cloudflare R2")
	} else {
		log.Print("attachments: R2 env absent — images stay on local disk")
	}
	// H8 retrieval (W1.2): read_file resolves spill locators through the
	// same store the loop writes into — filed outputs come back verbatim.
	if spillStore != nil {
		promptHandler.AttachSpill(spillStore)
	}
	// W3.2: plan-mode prompt assembly reads the same AGENTS.md chain the
	// orchestrator does — one factory, two consumers, plan and build cannot
	// drift on the project's conventions.
	promptHandler.AttachInstructionsChain(func(workspacePath string) string {
		return instructions.Loader{WorkspaceRoot: workspacePath, GlobalPath: globalAGENTS}.Load().Text()
	})
	var warmPool *redroid.Pool
	if warmPoolSize > 0 && containerMgr != nil {
		warmPool = redroid.NewPool(containerMgr, warmPoolSize)
	}
	sessionHandler := api.NewSessionHandler(s, containerMgr, portAlloc, relayAlloc, warmPool, workspaceDir, devClientMgr, walletMgr, pushNotifier)

	// DV4: the idle reaper. Devices nobody watches pause after 10 minutes
	// and die after 30 — same teardown path as a user destroy, metering
	// included. Viewer joins and agent turns reset the clock.
	reaper := api.NewReaper(sessionHandler)
	reaper.Start()
	usageHandler := api.NewUsageHandler(s, aiClient)
	notificationHandler := api.NewNotificationHandler(s)
	metricsHandler := api.NewMetricsHandler(s, containerMgr)
	errorsHandler := api.NewErrorsHandler()
	terminalHandler := ws.NewTerminalHandler(authSvc, s)
	streamHandler := ws.NewStreamHandler(authSvc, s)
	// DV4: a viewer joining a reaper-paused device wakes it.
	streamHandler.Unpause = containerMgr.UnpauseContainer
	inputHandler := ws.NewInputHandler(authSvc, s)

	// Payment/billing (Phase 9), test-mode only. The mock provider stands in for a
	// real processor behind the billing.Provider seam; going live is a deliberate
	// swap here — constructing a real provider — not a runtime toggle (spec 2.5,
	// 2.8). The webhook secret is a test credential kept out of the repo (spec 2.9).
	appBaseURL := strings.TrimSpace(os.Getenv("APP_BASE_URL"))
	if appBaseURL == "" {
		appBaseURL = "http://localhost:3000"
	}
	webhookSecret := strings.TrimSpace(os.Getenv("BILLING_WEBHOOK_SECRET"))
	if webhookSecret == "" {
		webhookSecret = "dev-test-webhook-secret"
		log.Printf("WARNING: BILLING_WEBHOOK_SECRET unset — using a development secret (test-mode billing only)")
	}
	billingProvider := billing.NewMock(webhookSecret, appBaseURL+"/checkout")
	billingHandler := api.NewBillingHandler(s, billingProvider, appBaseURL)
	meHandler := api.NewMeHandler(s)
	log.Printf("billing ready (provider %s, test mode)", billingProvider.Name())

	// SKR payment for release-export credits. The endpoint, mint and treasury
	// are read once, here, and are unreachable from any request: a caller who
	// could choose them would be choosing the oracle that judges their own
	// payment. With them absent the paywall still quotes its price and the
	// export gate still refuses correctly — only verify reports itself
	// unavailable, which is the honest answer when there is no chain to ask.
	skrVerifier := skr.New(skr.ConfigFromEnv())
	skrHandler := api.NewSkrHandler(s, skrVerifier)
	if cfg := skrVerifier.Config(); cfg.RPCURL != "" && cfg.Mint != "" {
		// Probe the mint now rather than at the first purchase. A mistyped
		// SKR_MINT would otherwise be a silent black hole where nobody can
		// ever buy, and the first symptom would be a customer's failure
		// instead of a warning in the boot log.
		decimals, owner, err := skrVerifier.ProbeMint(context.Background())
		switch {
		case err != nil:
			log.Printf("WARNING: SKR mint probe failed (%v) — /v1/skr/verify will refuse every payment until SKR_MINT and SKR_RPC_URL are right", err)
		case owner != skr.TokenProgram:
			log.Printf("WARNING: SKR_MINT is owned by %s, not the SPL token program — it is not a mint, so no transfer will ever match it", owner)
		case cfg.Decimals > 0 && cfg.Decimals != decimals:
			// Verification never uses SKR_DECIMALS — it reads the exponent off
			// the very balances it is judging — so this can only ever be an
			// operator noticing their config disagrees with the chain.
			log.Printf("WARNING: SKR_DECIMALS=%d but the chain says %d — the chain wins; fix the env", cfg.Decimals, decimals)
		default:
			log.Printf("skr payment ready (%d SKR for %d exports, mint %s)", cfg.Price, cfg.Credits, cfg.Mint)
		}
	} else {
		log.Printf("skr payment unconfigured (SKR_RPC_URL / SKR_MINT unset) — verify reports itself unavailable, the paywall still quotes its price")
	}

	// Premium GitHub integration (W4). gitremote.Load reads the OAuth app config
	// and the token-encryption key from the environment; when the OAuth app is
	// unset the integration reports itself unconfigured and every credential-bound
	// route degrades to a clear "not configured" state rather than failing oddly.
	gitCfg := gitremote.Load()
	gitHandler := api.NewGitHandler(s, gitCfg, wsMgr, workspaceDir)
	if gitCfg.Configured() {
		log.Printf("github integration ready (oauth configured)")
	} else {
		log.Printf("github integration dark (set GITHUB_OAUTH_CLIENT_ID/SECRET and GIT_TOKEN_ENCRYPTION_KEY to enable)")
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	// RealIP rewrites RemoteAddr from X-Forwarded-For, which any client can set.
	// It is only correct behind a proxy that overwrites that header, so it is
	// enabled by the same switch the rate limiter uses.
	if api.TrustingProxyHeaders() {
		r.Use(middleware.RealIP)
	}
	// Structured request log with the correlation id, replacing chi's
	// unstructured Logger. One line per request: join on req=<id>.
	r.Use(logx.RequestLogger)
	// Captures panics with the request identity instead of printing them
	// bare; the client still gets a plain 500 with no internals.
	r.Use(logx.Recoverer)
	r.Use(corsMiddleware(allowedOrigins()))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeStatus(w, map[string]string{
			"status": "ok",
			"time":   time.Now().UTC().Format(time.RFC3339),
		})
	})

	// readyz reports whether the server can actually serve work, which is what a
	// load balancer needs to know. healthz only says the process is alive.
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		body := map[string]string{"status": "ready", "database": "ok", "ai": "ok"}
		code := http.StatusOK
		if err := s.Ping(r.Context()); err != nil {
			body["status"], body["database"] = "degraded", err.Error()
			code = http.StatusServiceUnavailable
		}
		// The ai field used to be a hardcoded "ok". It now reports the
		// provider's real state: unconfigured, or failed within the recent
		// window. Ancient failures age out inside Health.
		if ok, detail := aiClient.Health(); !ok {
			body["status"], body["ai"] = "degraded", detail
			code = http.StatusServiceUnavailable
		}
		w.WriteHeader(code)
		writeStatus(w, body)
	})

	authLimiter := api.AuthRateLimiter()
	promptLimiter := api.PromptRateLimiter()

	// Sign-in routes. The provider list and the exchange are cheap local
	// operations inside the auth limiter; start/callback are browser
	// navigations to and from the provider and stay unthrottled public
	// routes (the state and code tokens are single-use, the flow is
	// bounded by them).
	r.Group(func(r chi.Router) {
		r.Use(authLimiter.Middleware)
		r.Get("/v1/auth/providers", oauthHandler.Providers)
		r.Post("/v1/auth/exchange", oauthHandler.Exchange)
		r.Post("/v1/auth/refresh", oauthHandler.Refresh)
		r.Post("/v1/auth/logout", oauthHandler.Logout)
		// P17: wallet sign-in — challenge/verify, same account-linking law
		// as the OAuth exchange, same auth limiter.
		walletHandler := api.NewWalletAuthHandler(s, authSvc, pushNotifier)
		r.Post("/v1/auth/wallet/challenge", walletHandler.Challenge)
		r.Post("/v1/auth/wallet/verify", walletHandler.Verify)
	})
	r.Get("/v1/auth/{provider}/start", oauthHandler.Start)
	r.Get("/v1/auth/{provider}/callback", oauthHandler.Callback)

	r.Group(func(r chi.Router) {
		r.Use(authMiddleware(authSvc))

		r.Get("/v1/projects", projectHandler.List)
		r.Post("/v1/projects", projectHandler.Create)
		r.Get("/v1/projects/{id}", projectHandler.Get)
		r.Put("/v1/projects/{id}", projectHandler.UpdatePlan)
		r.Delete("/v1/projects/{id}", projectHandler.Delete)
		r.Get("/v1/projects/{id}/files", projectHandler.GetFiles)
		r.Put("/v1/projects/{id}/files", projectHandler.WriteFiles)
		r.Delete("/v1/projects/{id}/files", projectHandler.DeleteFile)

		// Prompts. Rate limited separately: each one costs real money.
		r.Group(func(r chi.Router) {
			r.Use(promptLimiter.Middleware)
			r.Post("/v1/prompts", promptHandler.Create)
			r.Post("/v1/prompts/stream", promptHandler.Stream)
			// Resume runs the agent loop again, so it belongs with the metered
			// routes even though a resumed turn is not charged a second prompt.
			r.Post("/v1/prompts/{id}/resume", promptHandler.Resume)
			// Answer resumes a turn paused on ask_user — same loop, same "one prompt
			// covers the question and its answer" accounting as Resume.
			r.Post("/v1/prompts/{id}/answer", promptHandler.Answer)
			// Steer delivers a mid-turn message to a running build (H2): the
			// text lands in the next model request the loop makes. Metered with
			// the prompt routes because it spends the same provider budget.
			r.Post("/v1/prompts/{id}/steer", promptHandler.Steer)
		})
		r.Get("/v1/prompts/{id}", promptHandler.Get)
		// Discard only flips a status marker — no loop, no metering.
		r.Post("/v1/prompts/{id}/discard", promptHandler.Discard)

		// Project knowledge and history.
		r.Get("/v1/projects/{projectID}/memory", promptHandler.GetMemory)
		r.Get("/v1/projects/{projectID}/audit", promptHandler.ListAuditLog)
		metroHandler := api.NewMetroHandler(promptHandler)
		testInfoHandler := api.NewTestInfoHandler(promptHandler, devClientMgr)

		r.Get("/v1/projects/{projectID}/conversation", promptHandler.GetConversation)
		r.Get("/v1/projects/{projectID}/conversation/search", promptHandler.SearchConversation)
		// W6's durable sweep row, read for the header badge: absence is
		// 404 — "no verification recorded", never a green claim.
		r.Get("/v1/projects/{projectID}/verify-state", promptHandler.GetVerifyState)
		r.Get("/v1/projects/{id}/prompts", promptHandler.ListPrompts)
		r.Post("/v1/projects/{projectID}/feedback", promptHandler.RecordFeedback)
		r.Get("/v1/projects/{projectID}/feedback", promptHandler.ProjectFeedback)
		r.Get("/v1/projects/{projectID}/live-session", sessionHandler.LiveForProject)
		r.Get("/v1/projects/{projectID}/diff", promptHandler.GitDiff)
		r.Get("/v1/projects/{projectID}/metro", metroHandler.Serve)
		r.Get("/v1/projects/{projectID}/test-info", testInfoHandler.Info)
		r.Get("/v1/projects/{projectID}/dev-client.apk", testInfoHandler.DevClientAPK)
		r.Delete("/v1/projects/{projectID}/conversation", promptHandler.ClearConversation)

		// Prompt attachments — images and markdown docs a turn carries.
		// Uploaded unclaimed, claimed exactly once by the send.
		r.Post("/v1/projects/{id}/attachments", promptHandler.UploadAttachment)
		r.Get("/v1/projects/{id}/attachments/{attID}", promptHandler.DownloadAttachment)

		// Chat threads — multiple chats per project.
		r.Get("/v1/projects/{projectID}/conversations", promptHandler.ListConversations)
		r.Post("/v1/projects/{projectID}/conversations", promptHandler.CreateConversation)

		// Checkpoints — undo for anything the agent changed.
		r.Get("/v1/projects/{projectID}/checkpoints", promptHandler.ListCheckpoints)
		r.Post("/v1/projects/{projectID}/checkpoints/{checkpointID}/restore", promptHandler.RestoreCheckpoint)

		// Release export (Phase 11) — tier-gated server-side, async, isolated
		// from the dev loop. Produces a signed APK + submission bundle the
		// user submits themselves with their own publisher wallet.
		r.Post("/v1/projects/{id}/export", exportHandler.Create)
		r.Post("/v1/projects/{id}/assets", exportHandler.UploadAsset)
		r.Get("/v1/projects/{id}/assets", exportHandler.ListAssets)
		r.Get("/v1/projects/{id}/triage", triageHandler.Get)
		r.Put("/v1/projects/{id}/triage", triageHandler.Save)
		r.Get("/v1/exports/{id}", exportHandler.Get)
		r.Get("/v1/exports/{id}/download", exportHandler.Download)

		// SKR payment for export credits: quote the pack the paywall offers,
		// then re-derive the payment from the chain before the ledger moves.
		r.Post("/v1/skr/quote", skrHandler.Quote)
		r.Post("/v1/skr/verify", skrHandler.Verify)

		r.Post("/v1/sessions", sessionHandler.Create)
		r.Get("/v1/sessions/{id}", sessionHandler.Get)
		r.Get("/v1/sessions/{id}/screenshot", sessionHandler.Screenshot)
		r.Post("/v1/sessions/{id}/pause", sessionHandler.Pause)
		r.Post("/v1/sessions/{id}/resume", sessionHandler.Resume)
		r.Delete("/v1/sessions/{id}", sessionHandler.Destroy)

		r.Get("/v1/usage", usageHandler.Get)

		// E: notifications. The in-app history (every product notification,
		// whether or not OneSignal is configured) and the device push-token
		// registration. List/mark-read/read-state all require identity.
		r.Get("/v1/notifications", notificationHandler.List)
		r.Post("/v1/notifications/{id}/read", notificationHandler.MarkRead)
		r.Post("/v1/users/push-token", notificationHandler.RegisterToken)

		// Operational metrics in Prometheus format, same auth as everything
		// else under /v1. Gauges read live sources at scrape time.
		r.Get("/v1/metrics", metricsHandler.Get)

		// Captured errors (backend panics and frontend reports), newest first.
		r.Get("/v1/errors", errorsHandler.List)
		r.Post("/v1/client-errors", errorsHandler.Report)

		// Profile + billing (Phase 9). Reading and changing the profile, and
		// starting/cancelling a subscription, all require the caller's identity, so
		// they sit inside the auth group. The webhook does not (see below).
		r.Get("/v1/me", meHandler.Get)
		r.Put("/v1/me", meHandler.Update)
		// Sessions: the caller's live logins for inspection and revocation.
		// Identity comes from the access token, so these sit in the auth
		// group — unlike /v1/auth/logout, which authenticates by refresh
		// token and stays public.
		r.Get("/v1/auth/sessions", oauthHandler.ListSessions)
		r.Delete("/v1/auth/sessions/{id}", oauthHandler.RevokeSession)
		r.Post("/v1/auth/logout/all", oauthHandler.LogoutAll)
		r.Post("/v1/billing/upgrade", billingHandler.StartUpgrade)
		r.Post("/v1/billing/cancel", billingHandler.Cancel)
		// Test-mode only: simulates finishing checkout. Under a real provider this
		// route 404s (the handler feature-detects the simulator), so it cannot be
		// used to grant a tier in production.
		r.Post("/v1/billing/test/complete", billingHandler.TestComplete)

		// Premium GitHub integration (W4). Status is readable by any signed-in user
		// (it reports paid/configured so the UI knows which surface to show);
		// Connect and Push enforce the paid gate server-side. The OAuth callback is
		// NOT here — a browser navigation carries no Bearer token — it sits in the
		// public group below.
		r.Get("/v1/git/status", gitHandler.Status)
		r.Post("/v1/git/github/connect", gitHandler.Connect)
		r.Delete("/v1/git/github", gitHandler.Disconnect)
		r.Get("/v1/projects/{id}/git/remote", gitHandler.GetProjectRemote)
		r.Get("/v1/projects/{id}/git/status", gitHandler.GetProjectGitStatus)
		r.Get("/v1/projects/{id}/git/branches", gitHandler.GetProjectGitBranches)
		r.Get("/v1/projects/{id}/git/log", gitHandler.GetProjectGitLog)
		r.Post("/v1/projects/{id}/git/push", gitHandler.Push)
	})

	// Billing webhook: unauthenticated by necessity — the payment provider has no
	// JWT — and trusted solely by its signature, which the handler verifies before
	// reading the body. It sits outside the auth group for the same reason the
	// WebSocket routes do: it cannot use header-based auth.
	r.Post("/v1/billing/webhook", billingHandler.Webhook)

	// GitHub OAuth callback: unauthenticated by necessity — GitHub redirects the
	// user's browser here as a top-level navigation, which carries no JWT. The
	// handler recovers the user from the single-use state token it minted at
	// Connect, then redirects back to the settings page. It sits outside the auth
	// group for the same reason the billing webhook and WebSocket routes do.
	r.Get("/v1/git/github/callback", gitHandler.Callback)

	// WebSocket endpoints authenticate from the query string, because a browser
	// cannot set headers on a WebSocket handshake. Each handler validates the
	// token and confirms the caller owns the session before attaching to a
	// device, so these sit outside the header-based auth group by necessity, not
	// by omission.
	r.Get("/v1/sessions/{id}/terminal", terminalHandler.Handle)
	r.Get("/v1/sessions/{id}/stream", streamHandler.Handle)
	r.Get("/v1/sessions/{id}/input", inputHandler.Handle)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
		// A slow client must not be able to hold a connection open forever with a
		// partial request. WriteTimeout is deliberately unset: SSE builds and
		// WebSocket terminals are long-lived by design and any write deadline
		// would sever them mid-stream.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("talon backend listening on :%s", port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server stopped: %v", err)
		}
	}()

	<-shutdown
	log.Println("shutting down — letting in-flight builds finish (up to 60s)")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("some connections were closed before they finished: %v", err)
	}
	log.Println("stopped")
}

func authMiddleware(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				writeAuthError(w, "you need to sign in")
				return
			}

			token := strings.TrimPrefix(header, "Bearer ")
			if token == header {
				writeAuthError(w, "the Authorization header must be 'Bearer <token>'")
				return
			}

			claims, err := svc.ValidateToken(token)
			if err != nil {
				writeAuthError(w, "your session has expired — sign in again")
				return
			}

			ctx := context.WithValue(r.Context(), api.UserIDKey, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeStatus(w http.ResponseWriter, body map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(body)
}

// allowedOrigins is the CORS allowlist. Development hosts are built in;
// WEB_ORIGIN (the web app the OAuth hand-off targets) is allowed by the
// same fact; CORS_ALLOWED_ORIGINS adds a comma-separated list for anywhere
// else.
func allowedOrigins() map[string]bool {
	origins := map[string]bool{
		"http://localhost:3000": true,
		"http://localhost:3001": true,
		"http://127.0.0.1:3000": true,
		"http://127.0.0.1:3001": true,
	}
	// The web origin is where the browser UI lives — the OAuth hand-off
	// sends it there, so it is an allowed caller by the same fact.
	if web := strings.TrimSpace(os.Getenv("WEB_ORIGIN")); web != "" {
		origins[strings.TrimRight(web, "/")] = true
	}
	for _, key := range []string{"CORS_ALLOWED_ORIGIN", "CORS_ALLOWED_ORIGINS"} {
		for _, origin := range strings.Split(os.Getenv(key), ",") {
			if origin = strings.TrimSpace(origin); origin != "" {
				origins[origin] = true
			}
		}
	}
	return origins
}

func corsMiddleware(allowed map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if origin := r.Header.Get("Origin"); allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// resolveDir picks a directory from an env var, falling back to the first
// candidate that exists and then to the last one.
func resolveDir(envKey string, candidates ...string) string {
	if dir := strings.TrimSpace(os.Getenv(envKey)); dir != "" {
		return dir
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[len(candidates)-1]
	}
	return "."
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n := 0
	for _, c := range raw {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return fallback
	}
	return n
}

// findProjectRoot walks up from the executable, then the working directory,
// looking for go.mod.
func findProjectRoot() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		for i := 0; i < 10; i++ {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	for _, rel := range []string{".", "..", filepath.Join("..", "..")} {
		candidate := filepath.Join(cwd, rel)
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate
		}
	}
	return cwd
}

// dumpComposition prints the resolved document as JSON and returns — one
// encoder, one trailing newline, map keys sorted by encoding/json, rows in
// the composition's own apply order: byte-stable for the golden diff. A
// dark boot (no file) still prints, as an empty document, so the diff has
// bytes to compare in every state.
func dumpComposition(profile string) error {
	comp, err := composer.Load(profile)
	if err != nil {
		return err
	}
	if comp == nil {
		comp = &composition.Composition{Profile: profile}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(comp)
}
