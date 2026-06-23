package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/mcsm/agent/internal/api/handlers"
	"github.com/mcsm/agent/internal/api/middleware"
	"github.com/mcsm/agent/internal/metrics"
	"github.com/mcsm/agent/internal/process"
)

func NewRouter(token string, mgr *process.Manager, collector *metrics.Collector, serverRoot string) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.Auth(token))

	h := handlers.NewServerHandlers(mgr, serverRoot)
	ch := handlers.NewConsoleHandlers(mgr, serverRoot)
	mh := handlers.NewMetricsHandlers(mgr, collector)
	fh := handlers.NewFileHandlers(mgr)
	bh := handlers.NewBackupHandlers(mgr, serverRoot)
	ph := handlers.NewPlayersHandlers(mgr)

	r.Route("/agent/v1", func(r chi.Router) {
		r.Get("/health", handlers.Health)
		r.Get("/info", handlers.Info)
		r.Get("/java", handlers.JavaInstallations)
		r.Get("/metrics", mh.HostMetrics)

		r.Route("/servers/{id}", func(r chi.Router) {
			r.Post("/start", h.Start)
			r.Delete("/", h.Purge)
			r.Post("/reinstall", h.Reinstall)
			r.Post("/stop", h.Stop)
			r.Post("/restart", h.Restart)
			r.Post("/kill", h.Kill)
			r.Get("/status", h.Status)
			r.Post("/command", h.Command)
			r.Post("/mods/disable", h.DisableMods)
			r.Post("/register", ch.RegisterDir)
			r.Post("/setup", bh.Setup)
			r.Post("/backup", bh.Backup)
			r.Post("/backups/{backupId}/restore", bh.Restore)
			r.Delete("/backups/{backupId}", bh.DeleteBackup)
			r.Get("/backups/{backupId}/download", bh.DownloadBackup)
			r.Get("/players", ph.List)
			r.Get("/players/meta", ph.Meta)
			r.Get("/players/bans", ph.Bans)
			r.Post("/players/action", ph.Action)
			r.Get("/players/{uuid}", ph.Detail)

			r.Get("/console", ch.Console)
			r.Get("/metrics", mh.ServerMetrics)

			r.Get("/files", fh.List)
			r.Get("/files/tree", fh.Tree)
			r.Get("/files/content", fh.GetContent)
			r.Put("/files/content", fh.PutContent)
			r.Delete("/files", fh.Delete)
			r.Post("/files/rename", fh.Rename)
			r.Post("/files/mkdir", fh.Mkdir)
			r.Post("/files/hashes", fh.Hashes)
			r.Get("/files/download", fh.Download)
			r.Post("/files/upload", fh.Upload)
		})
	})

	return r
}
