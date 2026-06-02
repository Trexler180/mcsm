package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/mcsm/api/internal/api/handlers"
	apimw "github.com/mcsm/api/internal/api/middleware"
	"github.com/mcsm/api/internal/auth"
	"github.com/mcsm/api/internal/store"
)

func NewRouter(s *store.Store, jwtSecret, serverRoot string) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(apimw.RequestID)
	r.Use(apimw.Logger)

	authH := handlers.NewAuthHandlers(s, jwtSecret)
	nodeH := handlers.NewNodeHandlers(s)
	serverH := handlers.NewServerHandlers(s, serverRoot)
	fileH := handlers.NewFileHandlers(s)
	modH := handlers.NewModHandlers(s)
	backupH := handlers.NewBackupHandlers(s)
	taskH := handlers.NewTaskHandlers(s)
	userH := handlers.NewUserHandlers(s, jwtSecret)
	consoleH := handlers.NewConsoleHandlers(s)
	playersH := handlers.NewPlayersHandlers(s)
	auditH := handlers.NewAuditHandlers(s)

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth routes
		r.Post("/auth/login", authH.Login)
		r.Post("/auth/refresh", authH.Refresh)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(auth.Middleware(jwtSecret))

			r.Post("/auth/logout", authH.Logout)
			r.Get("/auth/me", authH.Me)

			// Nodes (admin only)
			r.Route("/nodes", func(r chi.Router) {
				r.Use(auth.AdminOnly)
				r.Get("/", nodeH.List)
				r.Post("/", nodeH.Create)
				r.Get("/{id}", nodeH.Get)
				r.Put("/{id}", nodeH.Update)
				r.Delete("/{id}", nodeH.Delete)
			})

			// Servers
			r.Route("/servers", func(r chi.Router) {
				r.Get("/", serverH.List)
				r.With(auth.AdminOnly).Post("/", serverH.Create)

				r.Route("/{id}", func(r chi.Router) {
					r.Use(requireServerAccess(s))
					r.Get("/", serverH.Get)
					r.Put("/", serverH.Update)
					r.Delete("/", serverH.Delete)

					r.Post("/start", serverH.Start)
					r.Post("/stop", serverH.Stop)
					r.Post("/restart", serverH.Restart)
					r.Post("/kill", serverH.Kill)
					r.Get("/status", serverH.Status)
					r.Post("/command", serverH.Command)

					// Console & metrics (WebSocket)
					r.Get("/console", consoleH.Console)
					r.Get("/metrics", consoleH.Metrics)

					// Online players (polled)
					r.Get("/players", playersH.List)

					// Files
					r.Get("/files", fileH.List)
					r.Get("/files/content", fileH.GetContent)
					r.Put("/files/content", fileH.PutContent)
					r.Delete("/files", fileH.Delete)
					r.Post("/files/rename", fileH.Rename)
					r.Post("/files/mkdir", fileH.Mkdir)
					r.Get("/files/download", fileH.Download)
					r.Post("/files/upload", fileH.Upload)

					// Mods
					r.Get("/mods", modH.List)
					r.Post("/mods/search", modH.Search)
					r.Get("/mods/project", modH.GetProject)
					r.Get("/mods/versions", modH.GetVersions)
					r.Post("/mods/install", modH.Install)
					r.Get("/mods/updates", modH.Updates)
					r.Post("/mods/{modId}/update", modH.Update)
					r.Post("/mods/{modId}/pin", modH.Pin)
					r.Delete("/mods/{modId}", modH.Uninstall)

					// Backups
					r.Get("/backups", backupH.ListBackups)
					r.Post("/backups", backupH.CreateBackup)
					r.Post("/backups/{backupId}/restore", backupH.RestoreBackup)
					r.Get("/backup-targets", backupH.ListTargets)
					r.Post("/backup-targets", backupH.CreateTarget)

					// Scheduled tasks
					r.Get("/tasks", taskH.List)
					r.Post("/tasks", taskH.Create)
					r.Put("/tasks/{taskId}", taskH.Update)
					r.Delete("/tasks/{taskId}", taskH.Delete)

					// Per-server audit trail
					r.Get("/audit", auditH.ListForServer)
				})
			})

			// Users (admin only)
			r.Route("/users", func(r chi.Router) {
				r.Use(auth.AdminOnly)
				r.Get("/", userH.List)
				r.Post("/", userH.Create)
				r.Delete("/{id}", userH.Delete)
			})

			// Global audit log (admin only)
			r.With(auth.AdminOnly).Get("/audit", auditH.List)
		})
	})

	return r
}
