package api

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/mcsm/api/internal/api/handlers"
	apimw "github.com/mcsm/api/internal/api/middleware"
	"github.com/mcsm/api/internal/api/ws"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/autoupdate"
	"github.com/mcsm/api/internal/mcpserver"
	"github.com/mcsm/api/internal/migrate"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/publicurl"
	"github.com/mcsm/api/internal/store"
)

// machineKeyLookup adapts the store's access-key authentication to the auth
// package's callback shape. The indirection is deliberate: store already
// imports auth (for password hashing), so auth cannot import store, and this is
// the one place that knows about both.
//
// Usage metadata is recorded here rather than in a later middleware because the
// row we just read carries the previous timestamp, which is what makes the
// staleness check free. A failed usage write is logged and ignored — it must
// never turn a valid credential into a 401.
func machineKeyLookup(s *store.Store) auth.KeyLookup {
	return func(ctx context.Context, presented, ip string) (*auth.MachineIdentity, error) {
		res, err := s.AuthenticateAccessKey(ctx, presented)
		if err != nil {
			return nil, err
		}
		if err := s.TouchAccessKey(ctx, res.Key, ip); err != nil {
			slog.Warn("access key usage update failed", "key_id", res.Key.ID, "error", err)
		}
		return &auth.MachineIdentity{
			KeyID:     res.Key.ID,
			UserID:    res.User.ID,
			Email:     res.User.Email,
			Role:      res.User.Role,
			Scopes:    res.Key.Scopes,
			ServerIDs: res.Key.ServerIDs,
			TokenHash: store.HashAccessKey(presented),
		}, nil
	}
}

func NewRouter(s *store.Store, jwtSecret, serverRoot string, updater *autoupdate.Engine, notifier *notify.Service) http.Handler {
	// Pin the WebSocket Origin allowlist to the app origin (defense in depth on
	// top of the single-use ticket auth). APP_ORIGIN unset => any origin, which
	// keeps local dev working; production sets it to the panel's URL.
	if origin := os.Getenv("APP_ORIGIN"); origin != "" {
		ws.SetAllowedOrigins([]string{origin})
	}

	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	// Route HEAD as GET (net/http drops the body): link-preview crawlers
	// probe og:image and status pages with HEAD before fetching.
	r.Use(chimw.GetHead)
	statusH := handlers.NewPublicStatusHandlers(s)
	// Public status pages on their own subdomains: <slug>.<PUBLIC_STATUS_DOMAIN>
	// is answered directly (before path routing); every other host passes
	// through. Unset means no host-based routing — the path route still works.
	if statusDomain := strings.ToLower(os.Getenv("PUBLIC_STATUS_DOMAIN")); statusDomain != "" {
		r.Use(statusH.HostRouter(statusDomain))
	}
	// Strip spoofable X-Forwarded-* from untrusted peers before RealIP consumes
	// them, so client IPs in audit logs and login throttling can't be forged.
	r.Use(apimw.TrustedProxy(apimw.ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))))
	r.Use(chimw.RealIP)
	r.Use(apimw.SecurityHeaders)
	// Cap non-multipart request bodies at 8 MiB; uploads use the multipart path,
	// which is proxied straight to the agent.
	r.Use(apimw.MaxBodyBytes(8 << 20))
	// ...and give those multipart bodies room to actually arrive: the server's
	// 30s ReadTimeout covers the whole request, so without this a world or
	// modpack upload is cut off mid-stream. Must stay ahead of Logger, whose
	// wrapper the deadline has to be set through.
	r.Use(apimw.UploadDeadline(handlers.UploadBudget))
	r.Use(apimw.RequestID)
	r.Use(apimw.Logger)

	tickets := auth.NewTicketStore()
	// One password-guess budget for login and every step-up, so an attacker
	// holding a session cannot multiply their guesses across endpoints.
	passwordThrottles := handlers.NewPasswordThrottles()
	authH := handlers.NewAuthHandlers(s, jwtSecret, tickets, passwordThrottles)
	apiKeyH := handlers.NewAPIKeyHandlers(s, passwordThrottles)
	nodeH := handlers.NewNodeHandlers(s)
	serverH := handlers.NewServerHandlers(s, serverRoot)
	folderH := handlers.NewFolderHandlers(s)
	memberH := handlers.NewServerMemberHandlers(s)
	fileH := handlers.NewFileHandlers(s)
	resourcePackH := handlers.NewResourcePackHandlers(s)
	modH := handlers.NewModHandlers(s, serverRoot, updater, notifier.Engine)
	migrationEngine := migrate.New(s)
	migrateH := handlers.NewMigrationHandlers(s, migrationEngine)
	backupH := handlers.NewBackupHandlers(s, notifier.Engine)
	taskH := handlers.NewTaskHandlers(s)
	userH := handlers.NewUserHandlers(s, jwtSecret)
	consoleH := handlers.NewConsoleHandlers(s)
	playersH := handlers.NewPlayersHandlers(s)
	auditH := handlers.NewAuditHandlers(s)
	mcH := handlers.NewMinecraftHandlers()
	settingsH := handlers.NewSettingsHandlers(s)
	overviewH := handlers.NewOverviewHandlers(s)
	mfaH := handlers.NewMFAHandlers(s)
	sessionH := handlers.NewSessionHandlers(s)
	notifyH := handlers.NewNotificationHandlers(s, notifier)

	// Remote agent access (MCP). The canonical public origin is resolved once
	// here and shared, so discovery documents, the consent redirect, and the
	// audience a token is bound to are all built from the same value.
	mcpURLs := publicurl.FromEnv()
	mcpOAuthH := handlers.NewMCPOAuthHandlers(s, mcpURLs)
	// The approval prompt is delivered on the same live stream as every other
	// alert, so a pending request is visible from any screen rather than only on
	// the settings card that lists them.
	mcpOpts := []mcpserver.Option{mcpserver.WithMigrationStarter(migrationEngine)}
	if notifier != nil {
		mcpOpts = append(mcpOpts, mcpserver.WithNotifier(notifier.Engine))
	}
	mcpGrantH := handlers.NewMCPGrantHandlers(s, mcpURLs, passwordThrottles, mcpOpts...)

	// Per-caller rate limit on authenticated traffic (generous for interactive
	// and polling use; trips only on pathological hammering).
	rateLimiter := apimw.NewRateLimiter(1200, 200)
	// The OAuth endpoints sit outside the authenticated group, so they get their
	// own budget keyed by source address. Tighter than the authenticated one:
	// registration and token exchange happen a handful of times per client, and
	// anything hammering them is not a legitimate flow.
	oauthLimiter := apimw.NewRateLimiter(60, 20)
	// Registration is the most exposed endpoint here (it must work before anyone
	// has authenticated), so it gets a budget of its own rather than sharing.
	registerLimiter := apimw.NewRateLimiter(10, 5)

	// OAuth discovery is origin-rooted by specification, so these live at the
	// root rather than under /api/v1. A reverse-proxied deployment must forward
	// them to the API — see docs/deployment.md.
	r.Group(func(r chi.Router) {
		r.Use(oauthLimiter.KeyedMiddleware(apimw.ClientIPKey))
		for _, path := range []string{
			"/.well-known/oauth-protected-resource",
			"/.well-known/oauth-protected-resource" + publicurl.MCPResourcePath,
		} {
			r.Get(path, mcpOAuthH.ProtectedResourceMetadata)
			r.Options(path, mcpOAuthH.MetadataPreflight)
		}
		// The bare path plus the path-inserted and OIDC aliases, because clients
		// disagree about which one to probe and a missed alias reads to the user
		// as "this server does not support OAuth".
		for _, path := range []string{
			"/.well-known/oauth-authorization-server",
			"/.well-known/oauth-authorization-server/api/v1/oauth",
			"/.well-known/openid-configuration",
			"/.well-known/openid-configuration/api/v1/oauth",
		} {
			r.Get(path, mcpOAuthH.AuthorizationServerMetadata)
			r.Options(path, mcpOAuthH.MetadataPreflight)
		}
	})

	// The MCP resource itself: bearer-authenticated by grant, never by session.
	mountMCP(r, s, mcpURLs, newMCPOperatorBackend(modH, backupH, playersH, serverH, fileH, taskH, mcH), migrationEngine, mcpOpts...)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", handlers.Health)

		// Public auth routes
		r.Post("/auth/login", authH.Login)
		r.Post("/auth/refresh", authH.Refresh)

		// OAuth authorization server for remote agent connections. Public by
		// necessity — a client must reach these before it holds anything — so
		// each carries its own rate-limit budget and generic errors.
		r.Group(func(r chi.Router) {
			r.Use(oauthLimiter.KeyedMiddleware(apimw.ClientIPKey))
			r.Get("/oauth/authorize", mcpOAuthH.Authorize)
			r.Post("/oauth/token", mcpOAuthH.Token)
			r.Post("/oauth/revoke", mcpOAuthH.Revoke)
			r.With(registerLimiter.KeyedMiddleware(apimw.ClientIPKey)).
				Post("/oauth/register", mcpOAuthH.Register)
		})
		r.Get("/public/servers/{id}/resource-pack/{publicID}", resourcePackH.Download)
		// Machine-readable public status (page HTML lives at /status/{slug}).
		r.Get("/public/status/{slug}", statusH.JSON)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(jwtSecret, tickets, machineKeyLookup(s)))
			r.Use(rateLimiter.Middleware)
			// Confine machine principals to the server API family before any
			// route runs, so anything mounted elsewhere is closed to access
			// keys by default. Ordered after the rate limiter so a key probing
			// routes it may not use still spends its own budget.
			r.Use(machineBoundary)

			r.Post("/auth/logout", authH.Logout)
			r.Get("/auth/me", authH.Me)
			// Mint a short-lived ticket for header-less requests (downloads, WS).
			r.Post("/auth/ticket", authH.Ticket)

			// Multi-factor auth (self-service TOTP enrollment).
			r.Get("/auth/mfa", mfaH.Status)
			r.Post("/auth/mfa/setup", mfaH.Setup)
			r.Post("/auth/mfa/enable", mfaH.Enable)
			r.Post("/auth/mfa/disable", mfaH.Disable)

			// Active sessions: review and revoke.
			r.Get("/auth/sessions", sessionH.List)
			r.Post("/auth/sessions/revoke-others", sessionH.RevokeOthers)
			r.Delete("/auth/sessions/{id}", sessionH.Revoke)

			// Agent access keys — scoped, expiring machine credentials.
			// Interactive sign-in only: an access key must never be able to
			// inspect, rotate, or mint another one. Creating and rotating
			// additionally require the current password and, when the account
			// has MFA on, a current TOTP code.
			r.Route("/auth/api-keys", func(r chi.Router) {
				r.Use(requireHuman)
				r.Get("/", apiKeyH.List)
				r.Post("/", apiKeyH.Create)
				r.Post("/{id}/rotate", apiKeyH.Rotate)
				r.Delete("/{id}", apiKeyH.Revoke)
			})

			// Remote agent connections (MCP over OAuth). Every route here is
			// human-only: a delegation is a decision a person makes, and no
			// machine credential may approve one, inspect one, or approve the
			// actions one requests. Consent additionally decides what a *new*
			// credential may do, which is precisely what an existing credential
			// must never be able to widen.
			r.Route("/oauth/consent", func(r chi.Router) {
				r.Use(requireHuman)
				r.Get("/", mcpOAuthH.Consent)
				r.Post("/", mcpOAuthH.Decide)
			})
			r.Route("/mcp/grants", func(r chi.Router) {
				r.Use(requireHuman)
				r.Get("/", mcpGrantH.ListGrants)
				r.Delete("/{id}", mcpGrantH.RevokeGrant)
				r.Patch("/{id}/approval-settings", mcpGrantH.UpdateGrantApprovalSettings)
			})
			r.Route("/mcp/action-requests", func(r chi.Router) {
				r.Use(requireHuman)
				r.Get("/", mcpGrantH.ListActionRequests)
				r.Post("/{id}/approve", mcpGrantH.ApproveAction)
				r.Post("/{id}/deny", mcpGrantH.DenyAction)
			})
			// Approval policy is per-user, not admin-scoped, because approval
			// already is: only the grant owner can approve their own requests.
			r.Route("/mcp/approval-settings", func(r chi.Router) {
				r.Use(requireHuman)
				r.Get("/", mcpGrantH.GetApprovalSettings)
				r.Put("/", mcpGrantH.UpdateApprovalSettings)
			})
			r.With(requireHuman).Get("/mcp/connection", mcpGrantH.ConnectionInfo)

			// Panel wall-clock + timezone (scheduled tasks fire on this clock).
			r.Get("/time", handlers.Time)

			// Minecraft version metadata (global, cached upstream lookups)
			r.Get("/minecraft/versions", mcH.Versions)
			r.Get("/minecraft/loaders", mcH.LoaderVersions)

			// Overview aggregate (scoped to the caller's servers)
			r.Get("/overview", overviewH.Overview)

			// Notifications — per-user alert subscriptions, channels, feed, and
			// the live WebSocket stream. All scoped to the caller.
			r.Route("/notifications", func(r chi.Router) {
				r.Get("/events", notifyH.Events)
				r.Get("/vapid", notifyH.VAPID)
				r.Get("/stream", notifyH.Stream)

				r.Get("/subscriptions", notifyH.ListSubscriptions)
				r.Put("/subscriptions", notifyH.UpsertSubscription)
				r.Delete("/subscriptions/{id}", notifyH.DeleteSubscription)

				r.Get("/channels", notifyH.ListChannels)
				r.Post("/channels", notifyH.CreateChannel)
				r.Put("/channels/{id}", notifyH.UpdateChannel)
				r.Delete("/channels/{id}", notifyH.DeleteChannel)
				r.Post("/channels/{id}/test", notifyH.TestChannel)

				r.Post("/push", notifyH.RegisterPush)
				r.Delete("/push", notifyH.UnregisterPush)

				r.Get("/feed", notifyH.Feed)
				r.Get("/unread-count", notifyH.UnreadCount)
				r.Post("/{id}/read", notifyH.MarkRead)
				r.Post("/read-all", notifyH.MarkAllRead)
			})

			// Nodes (admin only)
			r.Route("/nodes", func(r chi.Router) {
				r.Use(requireAdmin(s))
				r.Get("/", nodeH.List)
				r.Post("/", nodeH.Create)
				r.Get("/{id}", nodeH.Get)
				r.Put("/{id}", nodeH.Update)
				r.Delete("/{id}", nodeH.Delete)
			})

			// Server folders — flat grouping over the fleet. Listing is scoped
			// to folders the caller can see servers in; managing them is an
			// admin concern, like creating the servers themselves.
			r.Route("/server-folders", func(r chi.Router) {
				r.Get("/", folderH.List)
				r.With(requireAdmin(s)).Post("/", folderH.Create)
				r.With(requireAdmin(s)).Put("/{id}", folderH.Update)
				r.With(requireAdmin(s)).Delete("/{id}", folderH.Delete)
			})

			// Servers
			r.Route("/servers", func(r chi.Router) {
				r.Get("/", serverH.List)
				r.With(requireAdmin(s)).Post("/", serverH.Create)
				// Discover existing on-disk servers to import (admin, like create).
				r.With(requireAdmin(s)).Get("/import-candidates", serverH.ImportCandidates)

				r.Route("/{id}", func(r chi.Router) {
					// Atomic permissions.
					viewAccess := requireServerPermission(s, store.ServerPermissionView)
					consoleAccess := requireServerPermission(s, store.ServerPermissionConsole)
					taskAccess := requireServerPermission(s, store.ServerPermissionTasks)
					settingsAccess := requireServerPermission(s, store.ServerPermissionSettings)
					serverAdminAccess := requireServerPermission(s, store.ServerPermissionAdmin)

					// Power — one leaf per lifecycle action.
					startAccess := requireServerPermission(s, store.ServerPermissionPowerStart)
					stopAccess := requireServerPermission(s, store.ServerPermissionPowerStop)
					restartAccess := requireServerPermission(s, store.ServerPermissionPowerRestart)
					killAccess := requireServerPermission(s, store.ServerPermissionPowerKill)

					// Read/list routes use group access (the group or any leaf).
					playersRead := requireServerGroupAccess(s, store.ServerPermissionPlayers)
					filesRead := requireServerGroupAccess(s, store.ServerPermissionFiles)
					modsRead := requireServerGroupAccess(s, store.ServerPermissionMods)
					backupsRead := requireServerGroupAccess(s, store.ServerPermissionBackups)

					// Mutating leaves.
					filesWrite := requireServerPermission(s, store.ServerPermissionFilesWrite)
					filesDelete := requireServerPermission(s, store.ServerPermissionFilesDelete)
					playersDelete := requireServerPermission(s, store.ServerPermissionPlayersDelete)
					playersInspect := requireServerPermission(s, store.ServerPermissionPlayersInspect)
					modsInstall := requireServerPermission(s, store.ServerPermissionModsInstall)
					modsUpdate := requireServerPermission(s, store.ServerPermissionModsUpdate)
					modsRemove := requireServerPermission(s, store.ServerPermissionModsRemove)
					backupsCreate := requireServerPermission(s, store.ServerPermissionBackupsCreate)
					backupsRestore := requireServerPermission(s, store.ServerPermissionBackupsRestore)
					backupsDelete := requireServerPermission(s, store.ServerPermissionBackupsDelete)

					r.With(viewAccess).Get("/", serverH.Get)
					r.With(settingsAccess).Put("/", serverH.Update)
					r.With(serverAdminAccess).Delete("/", serverH.Delete)
					// Clone into a new server (admin only, like Create — it sets
					// host-executed config on the new server).
					r.With(requireAdmin(s)).Post("/clone", modH.Clone)

					r.With(startAccess).Post("/start", serverH.Start)
					// Helper mod: reading is a view-level concern, but turning it
					// on writes a jar into the server directory, so it needs the
					// same authority as other settings changes.
					r.With(viewAccess).Get("/helper-mod", serverH.HelperModStatus)
					r.With(settingsAccess).Post("/helper-mod", serverH.SetHelperMod)
					// Live TPS/MSPT/heap from the helper mod; served from agent
					// memory, so polling it is free for the Minecraft server.
					r.With(viewAccess).Get("/vitals", serverH.Vitals)
					r.With(settingsAccess).Post("/reinstall", serverH.Reinstall)
					r.With(settingsAccess).Post("/migrate", migrateH.Migrate)
					r.With(viewAccess).Get("/migrations", migrateH.List)
					r.With(viewAccess).Get("/migrations/{runId}", migrateH.Get)
					r.With(stopAccess).Post("/stop", serverH.Stop)
					r.With(restartAccess).Post("/restart", serverH.Restart)
					r.With(killAccess).Post("/kill", serverH.Kill)
					r.With(viewAccess).Get("/status", serverH.Status)
					r.With(viewAccess).Get("/java", serverH.JavaInstallations)
					r.With(consoleAccess).Post("/command", serverH.Command)

					// Console & metrics (WebSocket)
					r.With(consoleAccess).Get("/console", consoleH.Console)
					r.With(viewAccess).Get("/metrics", consoleH.Metrics)
					// Sampled resource history (JSON, bucket-averaged)
					r.With(viewAccess).Get("/metrics/history", serverH.MetricsHistory)
					// Aggregate stats page payload (totals, leaderboard, heatmap, daily)
					r.With(viewAccess).Get("/stats", serverH.Stats)

					// Players. Roster reads need any players access; the specific
					// action (whitelist/kick/ban/op) is enforced in the handler.
					r.With(playersRead).Get("/players", playersH.List)
					r.With(playersRead).Get("/players/meta", playersH.Meta)
					r.With(playersRead).Get("/players/bans", playersH.Bans)
					r.With(playersRead).Get("/players/bedrock/resolve", playersH.ResolveBedrock)
					// A player's saved data (inventory, ender chest, position) and
					// their visit history are read only through the detail view,
					// which is its own grant — the roster above says who plays
					// here, this says what they carry and when they were on.
					r.With(playersInspect).Get("/players/sessions", playersH.Sessions)
					r.With(playersRead).Post("/players/action", playersH.Action)
					r.With(playersInspect).Get("/players/{uuid}", playersH.Detail)
					r.With(playersDelete).Delete("/players/{uuid}", playersH.Delete)

					// Files
					r.With(filesRead).Get("/files", fileH.List)
					r.With(filesRead).Get("/files/tree", fileH.Tree)
					r.With(filesRead).Get("/files/content", fileH.GetContent)
					r.With(filesWrite).Put("/files/content", fileH.PutContent)
					r.With(filesDelete).Delete("/files", fileH.Delete)
					r.With(filesWrite).Post("/files/rename", fileH.Rename)
					r.With(filesWrite).Post("/files/mkdir", fileH.Mkdir)
					r.With(filesRead).Get("/files/download", fileH.Download)
					r.With(filesWrite).Post("/files/upload", fileH.Upload)

					// Worlds
					r.With(filesWrite).Post("/worlds/upload", fileH.UploadWorld)

					// Mods
					r.With(modsRead).Get("/mods", modH.List)
					r.With(modsRead).Get("/mods/sources", modH.Sources)
					r.With(modsRead).Get("/mods/categories", modH.Categories)
					r.With(modsRead).Post("/mods/search", modH.Search)
					r.With(modsRead).Get("/mods/project", modH.GetProject)
					r.With(modsRead).Get("/mods/version", modH.GetVersion)
					r.With(modsRead).Get("/mods/versions", modH.GetVersions)
					r.With(modsInstall).Post("/mods/install", modH.Install)
					r.With(modsInstall).Post("/mods/upload", modH.UploadCustom)
					r.With(modsUpdate).Post("/mods/disable-conflict", modH.DisableConflict)
					r.With(modsRead).Post("/mods/resolve-missing", modH.ResolveMissingDeps)
					r.With(modsRead).Get("/mods/conflicts", modH.ListConflicts)
					r.With(modsUpdate).Post("/mods/conflicts", modH.RecordConflict)
					r.With(modsInstall).Post("/mods/install-modpack", modH.InstallModpack)
					r.With(modsRead).Get("/mods/updates", modH.Updates)
					r.With(modsRead).Get("/mods/version-check", modH.VersionCheck)
					r.With(modsUpdate).Post("/mods/auto-update", modH.AutoUpdate)
					r.With(modsRead).Get("/mods/update-runs", modH.ListUpdateRuns)
					r.With(modsRead).Get("/mods/update-runs/{runId}", modH.GetUpdateRun)
					r.With(modsRead).Get("/mods/skipped-versions", modH.ListSkippedVersions)
					r.With(modsUpdate).Delete("/mods/skipped-versions", modH.UnskipVersion)
					r.With(modsUpdate).Post("/mods/{modId}/update", modH.Update)
					r.With(modsUpdate).Post("/mods/{modId}/pin", modH.Pin)
					r.With(modsUpdate).Post("/mods/{modId}/enabled", modH.SetEnabled)
					r.With(modsRead).Get("/mods/{modId}/dependents", modH.Dependents)
					r.With(modsRemove).Delete("/mods/{modId}", modH.Uninstall)

					// Backups
					r.With(backupsRead).Get("/backups", backupH.ListBackups)
					r.With(backupsCreate).Post("/backups", backupH.CreateBackup)
					r.With(backupsRestore).Post("/backups/{backupId}/restore", backupH.RestoreBackup)
					r.With(backupsDelete).Delete("/backups/{backupId}", backupH.DeleteBackup)
					r.With(backupsRead).Get("/backup-targets", backupH.ListTargets)
					r.With(backupsCreate, backupsDelete).Post("/backup-targets", backupH.CreateTarget)

					// Scheduled tasks
					r.With(taskAccess).Get("/tasks", taskH.List)
					r.With(taskAccess, requireHuman).Post("/tasks", taskH.Create)
					r.With(taskAccess, requireHuman).Put("/tasks/{taskId}", taskH.Update)
					r.With(taskAccess).Delete("/tasks/{taskId}", taskH.Delete)

					// Per-server audit trail + indexed log warnings
					r.With(viewAccess).Get("/audit", auditH.ListForServer)
					r.With(viewAccess).Get("/log-events", serverH.LogEvents)

					// Per-server collaborators
					r.With(viewAccess).Get("/members/me", memberH.Me)
					r.With(serverAdminAccess).Get("/members", memberH.List)
					r.With(serverAdminAccess).Post("/members", memberH.Create)
					r.With(serverAdminAccess).Put("/members/{userId}", memberH.Update)
					r.With(serverAdminAccess).Delete("/members/{userId}", memberH.Delete)
				})
			})

			// Users (admin only)
			r.Route("/users", func(r chi.Router) {
				r.Use(requireAdmin(s))
				r.Get("/", userH.List)
				r.Post("/", userH.Create)
				r.Put("/{id}", userH.Update)
				r.Delete("/{id}", userH.Delete)
			})

			// App settings — integration secrets (admin only)
			r.Route("/settings/integrations", func(r chi.Router) {
				r.Use(requireAdmin(s))
				r.Get("/", settingsH.ListIntegrations)
				r.Put("/{key}", settingsH.SetIntegration)
				r.Delete("/{key}", settingsH.DeleteIntegration)
			})

			// Global audit log (admin only)
			r.With(requireAdmin(s)).Get("/audit", auditH.List)
		})
	})

	// Public, unauthenticated status page (opt-in per server; minimal data).
	r.Get("/status/{slug}", statusH.Page)
	r.Get("/status/{slug}/og.png", statusH.OGImage) // live link-preview card

	return r
}
