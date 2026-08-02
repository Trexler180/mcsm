package handlers

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/mcsm/api/internal/store"
)

// Public, unauthenticated status pages: one per server that opted in with a
// slug (servers.public_status + public_slug). Reachable as /status/<slug> and,
// when PUBLIC_STATUS_DOMAIN is set, as https://<slug>.<that domain>/ via the
// Host-routing middleware below.
//
// Deliberately minimal surface: server name, online/offline, sampled player
// count, Minecraft version, and availability derived from the uptime tracker.
// No IDs, ports, addresses, player names, node or panel internals. A disabled
// or unknown slug is a plain 404 either way.

const (
	// publicCacheTTL bounds DB work under public traffic: at most one rebuild
	// per slug per TTL, everything else is served from memory.
	publicCacheTTL = 15 * time.Second
	// playersFreshFor treats the once-a-minute sampler feed as live; anything
	// older (sampler stopped, server just started) hides the count instead of
	// showing a stale number.
	playersFreshFor = 5 * time.Minute
	publicDays      = 90
)

// publicSlugRe is the DNS-label shape (the slug doubles as a subdomain).
var publicSlugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$`)

// publicSlugReserved blocks labels that collide with common infrastructure
// hostnames — a panel user must not be able to claim www.<status domain>.
// Only genuinely sensitive labels belong here: an explicitly configured vhost
// (say, a real staging site) always outranks the wildcard in nginx, so this
// list guards against confusion/phishing lookalikes, not routing conflicts.
var publicSlugReserved = map[string]bool{
	"www": true, "api": true, "app": true, "mail": true, "smtp": true,
	"imap": true, "pop": true, "webmail": true, "ftp": true, "ns1": true,
	"ns2": true, "admin": true, "panel": true, "dashboard": true,
	"status": true, "autoconfig": true, "autodiscover": true, "mta-sts": true,
	"root": true,
}

// ValidatePublicSlug enforces the subdomain-safe slug shape. Shared by the
// server update handler.
func ValidatePublicSlug(slug string) error {
	if len(slug) < 3 || len(slug) > 63 {
		return fmt.Errorf("public_slug must be 3-63 characters")
	}
	if !publicSlugRe.MatchString(slug) {
		return fmt.Errorf("public_slug may contain only lowercase letters, digits, and inner hyphens")
	}
	if publicSlugReserved[slug] {
		return fmt.Errorf("public_slug %q is reserved", slug)
	}
	return nil
}

type PublicStatusHandlers struct {
	store *store.Store

	mu      sync.Mutex
	cache   map[string]publicCacheEntry
	ogCache map[string]ogCacheEntry // rendered og.png per slug, same TTL
}

type publicCacheEntry struct {
	view    *publicStatusView
	expires time.Time
}

type ogCacheEntry struct {
	png     []byte
	expires time.Time
}

func NewPublicStatusHandlers(s *store.Store) *PublicStatusHandlers {
	return &PublicStatusHandlers{
		store:   s,
		cache:   map[string]publicCacheEntry{},
		ogCache: map[string]ogCacheEntry{},
	}
}

// publicDay is one bar of the availability strip, in UTC days.
type publicDay struct {
	Date            string  `json:"date"` // YYYY-MM-DD
	AvailabilityPct float64 `json:"availability_pct"`
	HasData         bool    `json:"has_data"`
}

type publicStatusView struct {
	Name         string      `json:"name"`
	Slug         string      `json:"slug"`
	Online       bool        `json:"online"`
	OnlineSince  int64       `json:"online_since,omitempty"`  // unix seconds
	OfflineSince int64       `json:"offline_since,omitempty"` // unix seconds
	MCVersion    string      `json:"mc_version,omitempty"`
	Players      *int        `json:"players,omitempty"` // sampled count; absent when unknown
	Avail24h     *float64    `json:"availability_24h,omitempty"`
	Avail7d      *float64    `json:"availability_7d,omitempty"`
	Avail90d     *float64    `json:"availability_90d,omitempty"`
	Days         []publicDay `json:"days"`
	UpdatedAt    int64       `json:"updated_at"`
}

// view returns the (possibly cached) public view for a slug, or nil when the
// slug resolves to nothing public.
func (h *PublicStatusHandlers) view(ctx context.Context, slug string) *publicStatusView {
	if err := ValidatePublicSlug(slug); err != nil {
		return nil
	}

	h.mu.Lock()
	if e, ok := h.cache[slug]; ok && time.Now().Before(e.expires) {
		h.mu.Unlock()
		return e.view
	}
	h.mu.Unlock()

	v := h.build(ctx, slug)
	if v == nil {
		return nil
	}
	h.mu.Lock()
	h.cache[slug] = publicCacheEntry{view: v, expires: time.Now().Add(publicCacheTTL)}
	h.mu.Unlock()
	return v
}

func (h *PublicStatusHandlers) build(ctx context.Context, slug string) *publicStatusView {
	srv, err := h.store.GetServerByPublicSlug(ctx, slug)
	if err != nil {
		return nil
	}

	now := time.Now()
	v := &publicStatusView{
		Name:      srv.Name,
		Slug:      slug,
		Online:    srv.Status == "online",
		MCVersion: srv.MCVersion,
		UpdatedAt: now.Unix(),
		Days:      []publicDay{},
	}

	report, err := h.store.UptimeReport(ctx, srv.ID, now.AddDate(0, 0, -publicDays), now)
	if err == nil && report.TrackedSince > 0 {
		if v.Online {
			v.OnlineSince = report.OnlineSince
		} else {
			for _, seg := range report.Segments {
				if seg.EndedAt > v.OfflineSince {
					v.OfflineSince = seg.EndedAt
				}
			}
		}
		if report.WindowSeconds > 0 {
			p := round3(report.AvailabilityPct)
			v.Avail90d = &p
		}
		v.Days = dailyAvailability(report, now)

		for _, w := range []struct {
			hours int
			dst   **float64
		}{{24, &v.Avail24h}, {24 * 7, &v.Avail7d}} {
			r, err := h.store.UptimeReport(ctx, srv.ID, now.Add(-time.Duration(w.hours)*time.Hour), now)
			if err == nil && r.WindowSeconds > 0 {
				p := round3(r.AvailabilityPct)
				*w.dst = &p
			}
		}
	}

	if v.Online {
		if players, ts, err := h.store.LatestServerPlayers(ctx, srv.ID); err == nil &&
			ts > 0 && now.Unix()-ts < int64(playersFreshFor.Seconds()) {
			v.Players = &players
		}
	}
	return v
}

// dailyAvailability folds the 90-day report's segments into per-UTC-day
// availability. Days entirely before tracking began carry HasData=false.
func dailyAvailability(report *store.UptimeReport, now time.Time) []publicDay {
	nowU := now.Unix()
	out := make([]publicDay, 0, publicDays)
	dayStartT := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(publicDays - 1))
	for i := 0; i < publicDays; i++ {
		start := dayStartT.AddDate(0, 0, i).Unix()
		end := start + 86400
		// Denominator: the part of the day that was tracked and has elapsed.
		lo, hi := max(start, report.TrackedSince), min(end, nowU)
		d := publicDay{Date: dayStartT.AddDate(0, 0, i).Format("2006-01-02")}
		if hi > lo {
			var up int64
			for _, seg := range report.Segments {
				segEnd := seg.EndedAt
				if segEnd == 0 {
					segEnd = nowU
				}
				s, e := max(seg.StartedAt, lo), min(segEnd, hi)
				if e > s {
					up += e - s
				}
			}
			d.HasData = true
			d.AvailabilityPct = round3(min(100, float64(up)/float64(hi-lo)*100))
		}
		out = append(out, d)
	}
	return out
}

// round3 keeps availability precise enough that a fourth nine still shows:
// 0.001% of a 90-day window is ~78 seconds, right at the tracker's resolution.
func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}

// fmtPct renders a rounded percentage without trailing zeros: "99.987%",
// "99.9%", "100%".
func fmtPct(f float64) string {
	return strconv.FormatFloat(round3(f), 'f', -1, 64) + "%"
}

// fmtDurShort renders an elapsed span at the coarse granularity the status
// page uses everywhere: "42s", "12m", "3h 4m", "5d 2h".
func fmtDurShort(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 48*3600:
		return fmt.Sprintf("%dh %dm", seconds/3600, seconds%3600/60)
	default:
		return fmt.Sprintf("%dd %dh", seconds/86400, seconds%86400/3600)
	}
}

// ── link-preview embeds (Open Graph) ─────────────────────────────────────────
//
// Discord, Discourse, Slack, etc. unfurl links from server-rendered Open Graph
// tags — no JS runs — so the tags below are computed live per request and the
// preview reflects status at unfurl time. Discord additionally reads
// theme-color for the embed accent strip and twitter:card for the large-image
// layout; the og:image is a live-rendered PNG card (public_status_og.go).

func embedTitle(v *publicStatusView) string {
	if v.Online {
		return v.Name + " is Online"
	}
	return v.Name + " is Offline"
}

func embedDescription(v *publicStatusView, now int64) string {
	var parts []string
	if v.Online {
		s := "🟢 Online"
		if v.OnlineSince > 0 {
			s += " for " + fmtDurShort(now-v.OnlineSince)
		}
		parts = append(parts, s)
		if v.Players != nil {
			n := *v.Players
			if n == 1 {
				parts = append(parts, "1 player")
			} else {
				parts = append(parts, fmt.Sprintf("%d players", n))
			}
		}
	} else {
		s := "🔴 Offline"
		if v.OfflineSince > 0 {
			s += " for " + fmtDurShort(now-v.OfflineSince)
		}
		parts = append(parts, s)
	}
	if v.MCVersion != "" {
		parts = append(parts, "Minecraft "+v.MCVersion)
	}
	if v.Avail90d != nil {
		parts = append(parts, fmtPct(*v.Avail90d)+" uptime (90d)")
	}
	return strings.Join(parts, " · ")
}

func embedThemeColor(v *publicStatusView) string {
	if v.Online {
		return "#22c55e"
	}
	return "#ef4444"
}

// requestBaseURL reconstructs the external origin for the absolute URLs
// crawlers require in og:url/og:image. Behind nginx the scheme arrives via
// X-Forwarded-Proto; anything but http/https falls back to the direct view.
func requestBaseURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme != "http" && scheme != "https" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	return scheme + "://" + r.Host
}

// statusPageModel is the view plus the per-request embed metadata.
type statusPageModel struct {
	*publicStatusView
	PageURL    string
	ImageURL   string
	OGTitle    string
	OGDesc     string
	ThemeColor string
}

// ── HTTP surface ─────────────────────────────────────────────────────────────

// Page renders the HTML status page for /status/{slug}.
func (h *PublicStatusHandlers) Page(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	h.servePage(w, r, slug, "/status/"+slug+"/og.png")
}

// JSON serves the machine-readable view for /api/v1/public/status/{slug}.
func (h *PublicStatusHandlers) JSON(w http.ResponseWriter, r *http.Request) {
	h.serveJSON(w, r, chi.URLParam(r, "slug"))
}

// OGImage serves the live preview card for /status/{slug}/og.png.
func (h *PublicStatusHandlers) OGImage(w http.ResponseWriter, r *http.Request) {
	h.serveOGImage(w, r, chi.URLParam(r, "slug"))
}

// servePage renders the page; ogPath is the same-origin path of the preview
// image (it differs between path and subdomain routing).
func (h *PublicStatusHandlers) servePage(w http.ResponseWriter, r *http.Request, slug, ogPath string) {
	v := h.view(r.Context(), slug)
	if v == nil {
		http.NotFound(w, r)
		return
	}
	base := requestBaseURL(r)
	pagePath := strings.TrimSuffix(ogPath, "/og.png")
	if pagePath == "" {
		pagePath = "/"
	}
	m := &statusPageModel{
		publicStatusView: v,
		PageURL:          base + pagePath,
		ImageURL:         base + ogPath,
		OGTitle:          embedTitle(v),
		OGDesc:           embedDescription(v, time.Now().Unix()),
		ThemeColor:       embedThemeColor(v),
	}
	// The page uses inline CSS only; relax the API-wide default-src 'none' CSP
	// exactly that far. No scripts, no external fetches.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=30")
	if err := statusPageTmpl.Execute(w, m); err != nil {
		// Headers are already gone; just stop writing.
		return
	}
}

func (h *PublicStatusHandlers) serveJSON(w http.ResponseWriter, r *http.Request, slug string) {
	v := h.view(r.Context(), slug)
	if v == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=30")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, v)
}

// HostRouter serves status pages on <slug>.<domain> hosts and passes every
// other request through. domain is PUBLIC_STATUS_DOMAIN, already lowercased.
func (h *PublicStatusHandlers) HostRouter(domain string) func(http.Handler) http.Handler {
	suffix := "." + strings.TrimPrefix(domain, ".")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := strings.ToLower(r.Host)
			if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
				host = host[:i]
			}
			slug, ok := strings.CutSuffix(host, suffix)
			if !ok || slug == "" || strings.Contains(slug, ".") {
				next.ServeHTTP(w, r)
				return
			}
			switch r.URL.Path {
			case "/", "":
				h.servePage(w, r, slug, "/og.png")
			case "/status.json":
				h.serveJSON(w, r, slug)
			case "/og.png":
				h.serveOGImage(w, r, slug)
			case "/favicon.ico":
				w.WriteHeader(http.StatusNoContent)
			default:
				http.NotFound(w, r)
			}
		})
	}
}

// ── page template ────────────────────────────────────────────────────────────

// barColorHex maps a day to its strip color; shared with the OG image renderer.
func barColorHex(d publicDay) string {
	if !d.HasData {
		return "#2a2a2a"
	}
	switch {
	case d.AvailabilityPct >= 99:
		return "#16a34a"
	case d.AvailabilityPct >= 90:
		return "#ca8a04"
	case d.AvailabilityPct > 0:
		return "#dc2626"
	default:
		return "#7f1d1d"
	}
}

var statusPageTmpl = template.Must(template.New("status").Funcs(template.FuncMap{
	"fmtDur": func(fromUnix int64) string {
		return fmtDurShort(time.Now().Unix() - fromUnix)
	},
	"barColor": barColorHex,
	"barTitle": func(d publicDay) string {
		if !d.HasData {
			return d.Date + " — no data"
		}
		return d.Date + " — " + fmtPct(d.AvailabilityPct) + " uptime"
	},
	"pct": pctText,
	"fmtClock": func(unix int64) string {
		return time.Unix(unix, 0).UTC().Format("15:04 UTC")
	},
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta http-equiv="refresh" content="60">
<title>{{.Name}} — status</title>
<link rel="canonical" href="{{.PageURL}}">
<meta name="description" content="{{.OGDesc}}">
<meta name="theme-color" content="{{.ThemeColor}}">
<meta property="og:type" content="website">
<meta property="og:site_name" content="Minecraft server status">
<meta property="og:title" content="{{.OGTitle}}">
<meta property="og:description" content="{{.OGDesc}}">
<meta property="og:url" content="{{.PageURL}}">
<meta property="og:image" content="{{.ImageURL}}">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="630">
<meta property="og:image:type" content="image/png">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="{{.OGTitle}}">
<meta name="twitter:description" content="{{.OGDesc}}">
<meta name="twitter:image" content="{{.ImageURL}}">
<style>
  :root { color-scheme: dark; }
  * { box-sizing: border-box; margin: 0; }
  body {
    background: #101010; color: #e5e5e5;
    font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif;
    display: flex; justify-content: center; padding: 48px 16px;
  }
  main { width: 100%; max-width: 640px; }
  .card { background: #181818; border: 1px solid #2a2a2a; border-radius: 10px; padding: 24px; }
  h1 { font-size: 20px; font-weight: 600; }
  .row { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
  .state { display: inline-flex; align-items: center; gap: 8px; font-weight: 600; }
  .dot { width: 10px; height: 10px; border-radius: 50%; }
  .up .dot { background: #22c55e; box-shadow: 0 0 8px rgba(34,197,94,.6); }
  .down .dot { background: #ef4444; }
  .up { color: #4ade80; } .down { color: #f87171; }
  .sub { color: #8a8a8a; font-size: 13px; margin-top: 4px; }
  .tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(110px, 1fr)); gap: 10px; margin-top: 20px; }
  .tile { background: #131313; border: 1px solid #262626; border-radius: 8px; padding: 10px 12px; }
  .tile b { display: block; font-size: 16px; font-weight: 600; color: #f0f0f0; }
  .tile span { font-size: 11px; color: #8a8a8a; text-transform: uppercase; letter-spacing: .04em; }
  .bars { display: flex; gap: 2px; margin-top: 20px; height: 34px; align-items: stretch; }
  .bars div { flex: 1; border-radius: 1.5px; min-width: 2px; }
  .barlabel { display: flex; justify-content: space-between; color: #666; font-size: 11px; margin-top: 6px; }
  footer { color: #555; font-size: 12px; margin-top: 16px; text-align: center; }
</style>
</head>
<body>
<main>
  <div class="card">
    <div class="row">
      <div>
        <h1>{{.Name}}</h1>
        {{if .MCVersion}}<p class="sub">Minecraft {{.MCVersion}}</p>{{end}}
      </div>
      {{if .Online}}
        <span class="state up"><span class="dot"></span>Online</span>
      {{else}}
        <span class="state down"><span class="dot"></span>Offline</span>
      {{end}}
    </div>

    <div class="tiles">
      {{if .Online}}
        {{if .OnlineSince}}<div class="tile"><b>{{fmtDur .OnlineSince}}</b><span>Uptime</span></div>{{end}}
        {{if .Players}}<div class="tile"><b>{{.Players}}</b><span>Players online</span></div>{{end}}
      {{else if .OfflineSince}}
        <div class="tile"><b>{{fmtDur .OfflineSince}}</b><span>Down for</span></div>
      {{end}}
      <div class="tile"><b>{{pct .Avail24h}}</b><span>24h uptime</span></div>
      <div class="tile"><b>{{pct .Avail7d}}</b><span>7d uptime</span></div>
      <div class="tile"><b>{{pct .Avail90d}}</b><span>90d uptime</span></div>
    </div>

    <div class="bars">
      {{range .Days}}<div style="background: {{barColor .}}" title="{{barTitle .}}"></div>{{end}}
    </div>
    <div class="barlabel"><span>90 days ago</span><span>today</span></div>
  </div>
  <footer>Updated {{fmtClock .UpdatedAt}} · refreshes automatically</footer>
</main>
</body>
</html>`))
