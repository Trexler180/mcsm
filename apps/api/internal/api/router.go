package api

import (
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/mcsm/api/internal/api/handlers"
	apimw "github.com/mcsm/api/internal/api/middleware"
	"github.com/mcsm/api/internal/api/ws"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/autoupdate"
	"github.com/mcsm/api/internal/migrate"
	"github.com/mcsm/api/internal/notify"
	"github.com/mcsm/api/internal/store"
)

func NewRouter(s *store.Store, jwtSecret, serverRoot string, updater *autoupdate.Engine, notifier *notify.Service) http.Handler {
	// Pin the WebSocket Origin allowlist to the app origin (defense in depth on
	// top of the single-use ticket auth). APP_ORIGIN unset => any origin, which
	// keeps local dev working; production sets it to the panel's URL.
	if origin := os.Getenv("APP_ORIGIN"); origin != "" {
		ws.SetAllowedOrigins([]string{origin})
	}

	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	// Strip spoofable X-Forwarded-* from untrusted peers before RealIP consumes
	// them, so client IPs in audit logs and login throttling can't be forged.
	r.Use(apimw.TrustedProxy(apimw.ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES"))))
	r.Use(chimw.RealIP)
	r.Use(apimw.SecurityHeaders)
	// Cap non-multipart request bodies at 8 MiB; uploads use the multipart path,
	// which is proxied straight to the agent.
	r.Use(apimw.MaxBodyBytes(8 << 20))
	r.Use(apimw.RequestID)
	r.Use(apimw.Logger)

	tickets := auth.NewTicketStore()
	authH := handlers.NewAuthHandlers(s, jwtSecret, tickets)
	nodeH := handlers.NewNodeHandlers(s)
	serverH := handlers.NewServerHandlers(s, serverRoot)
	memberH := handlers.NewServerMemberHandlers(s)
	fileH := handlers.NewFileHandlers(s)
	resourcePackH := handlers.NewResourcePackHandlers(s)
	modH := handlers.NewModHandlers(s, updater, notifier.Engine)
	migrateH := handlers.NewMigrationHandlers(s, migrate.New(s))
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

	// Per-caller rate limit on authenticated traffic (generous for interactive
	// and polling use; trips only on pathological hammering).
	rateLimiter := apimw.NewRateLimiter(1200, 200)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", handlers.Health)

		// Public auth routes
		r.Post("/auth/login", authH.Login)
		r.Post("/auth/refresh", authH.Refresh)
		r.Get("/public/servers/{id}/resource-pack/{publicID}", resourcePackH.Download)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(jwtSecret, tickets))
			r.Use(rateLimiter.Middleware)

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
					modsInstall := requireServerPermission(s, store.ServerPermissionModsInstall)
					modsUpdate := requireServerPermission(s, store.ServerPermissionModsUpdate)
					modsRemove := requireServerPermission(s, store.ServerPermissionModsRemove)
					backupsCreate := requireServerPermission(s, store.ServerPermissionBackupsCreate)
					backupsRestore := requireServerPermission(s, store.ServerPermissionBackupsRestore)
					backupsDelete := requireServerPermission(s, store.ServerPermissionBackupsDelete)

					r.With(viewAccess).Get("/", serverH.Get)
					r.With(settingsAccess).Put("/", serverH.Update)
					r.With(serverAdminAccess).Delete("/", serverH.Delete)

					r.With(startAccess).Post("/start", serverH.Start)
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

					// Players. Roster reads need any players access; the specific
					// action (whitelist/kick/ban/op) is enforced in the handler.
					r.With(playersRead).Get("/players", playersH.List)
					r.With(playersRead).Get("/players/meta", playersH.Meta)
					r.With(playersRead).Get("/players/bans", playersH.Bans)
					r.With(playersRead).Post("/players/action", playersH.Action)
					r.With(playersRead).Get("/players/{uuid}", playersH.Detail)
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
					r.With(modsRemove).Delete("/mods/{modId}", modH.Uninstall)

					// Backups
					r.With(backupsRead).Get("/backups", backupH.ListBackups)
					r.With(backupsCreate).Post("/backups", backupH.CreateBackup)
					r.With(backupsRestore).Post("/backups/{backupId}/restore", backupH.RestoreBackup)
					r.With(backupsDelete).Delete("/backups/{backupId}", backupH.DeleteBackup)
					r.With(backupsRead).Get("/backup-targets", backupH.ListTargets)
					r.With(backupsCreate).Post("/backup-targets", backupH.CreateTarget)

					// Scheduled tasks
					r.With(taskAccess).Get("/tasks", taskH.List)
					r.With(taskAccess).Post("/tasks", taskH.Create)
					r.With(taskAccess).Put("/tasks/{taskId}", taskH.Update)
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

	return r
}
