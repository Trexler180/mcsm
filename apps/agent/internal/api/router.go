package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/mcsm/agent/internal/api/handlers"
	"github.com/mcsm/agent/internal/api/middleware"
	"github.com/mcsm/agent/internal/link"
	"github.com/mcsm/agent/internal/metrics"
	"github.com/mcsm/agent/internal/process"
)

// NewRouter builds the agent's HTTP surface.
//
// links may be nil, in which case the helper-mod endpoint is simply not mounted
// and every server falls back to log scraping and stdin. linkSink may likewise
// be nil; the vitals endpoint then reports every server as unlinked.
func NewRouter(token string, mgr *process.Manager, collector *metrics.Collector, serverRoot string, links *link.Registry, linkSink *link.MemorySink) http.Handler {
	r := chi.NewRouter()
	r.Use(chimw.Recoverer)
	r.Use(chimw.RealIP)
	r.Use(middleware.SecurityHeaders)
	// Cap non-multipart bodies (file-content writes, JSON) at 32 MiB; large file
	// transfers use the multipart upload path, which streams to disk.
	r.Use(middleware.MaxBodyBytes(32 << 20))
	// Those streamed transfers arrive at whatever speed the user's connection
	// manages, so they need more than the server's 30s whole-request ReadTimeout.
	r.Use(middleware.UploadDeadline(2 * time.Hour))

	// The helper-mod link is deliberately outside the agent-token group: it
	// authenticates with the per-launch token issued to that specific server
	// process, which is a narrower credential than the agent token and must not
	// be interchangeable with it.
	if links != nil {
		r.Get("/agent/v1/link/{id}", links.Handle)
	}

	h := handlers.NewServerHandlers(mgr, serverRoot)
	ch := handlers.NewConsoleHandlers(mgr, serverRoot)
	mh := handlers.NewMetricsHandlers(mgr, collector, linkSink)
	fh := handlers.NewFileHandlers(mgr)
	bh := handlers.NewBackupHandlers(mgr, serverRoot)
	wh := handlers.NewWorldHandlers(mgr)
	ph := handlers.NewPlayersHandlers(mgr)
	vh := handlers.NewVitalsHandlers(linkSink)

	r.Group(func(r chi.Router) {
		r.Use(middleware.Auth(token))

		r.Route("/agent/v1", func(r chi.Router) {
			r.Get("/health", handlers.Health)
			r.Get("/info", handlers.Info)
			r.Get("/java", handlers.JavaInstallations)
			r.Get("/metrics", mh.HostMetrics)
			// Discover existing server directories on disk so the panel can import them.
			r.Get("/import/scan", h.ScanImports)

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
				r.Post("/port", bh.ApplyPort)
				r.Post("/backup", bh.Backup)
				r.Post("/backups/{backupId}/restore", bh.Restore)
				r.Delete("/backups/{backupId}", bh.DeleteBackup)
				r.Get("/backups/{backupId}/download", bh.DownloadBackup)
				r.Get("/players", ph.List)
				r.Get("/players/meta", ph.Meta)
				r.Get("/players/bans", ph.Bans)
				r.Get("/players/bedrock/resolve", ph.ResolveBedrock)
				r.Post("/players/action", ph.Action)
				r.Get("/players/{uuid}", ph.Detail)
				r.Delete("/players/{uuid}", ph.Delete)

				r.Get("/console", ch.Console)
				r.Get("/metrics", mh.ServerMetrics)
				r.Get("/stats", mh.Stats)
				r.Get("/vitals", vh.Vitals)

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

				// Worlds get their own upload route rather than reusing the file
				// upload: the zip has to be validated and unpacked into a folder,
				// not dropped on disk as-is.
				r.Post("/worlds/upload", wh.Upload)
			})
		})
	})

	return r
}
