package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/store"
)

// A server's identity is what a player sees of it: the address they type, the
// MOTD in their server list, its icon, and how many can join. The address is
// panel metadata (the operator knows the public name; the panel only knows the
// node's), the rest is read from the server's own files.

type serverIdentity struct {
	// Address is what a player types to join.
	Address string `json:"address"`
	// AddressSource is "custom" when set in the server's settings, "node" when
	// derived from the node's host and the server's port.
	AddressSource string `json:"address_source"`
	// Read from the server's files; absent when its node could not be reached.
	MaxPlayers *int    `json:"max_players,omitempty"`
	MOTD       *string `json:"motd,omitempty"`
	// MOTDPlain is the MOTD without § formatting codes.
	MOTDPlain *string `json:"motd_plain,omitempty"`
	// Icon is server-icon.png as a data URL, small enough to inline.
	Icon      *string `json:"icon,omitempty"`
	FilesRead bool    `json:"files_read"`
}

const (
	maxIdentityProps = 1 << 20
	maxIconBytes     = 64 << 10
)

var (
	pngMagic       = []byte("\x89PNG\r\n\x1a\n")
	formatCodeRe   = regexp.MustCompile(`(?i)§[0-9a-fk-orx]`)
	connectAddrRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?(:[0-9]{1,5})?$`)
	unicodeEscapes = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
)

// validConnectAddress accepts host or host:port.
func validConnectAddress(s string) bool {
	if !connectAddrRe.MatchString(s) {
		return false
	}
	if _, p, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(p)
		return err == nil && n > 0 && n <= 65535
	}
	return true
}

// connectAddress is the address a player types: the custom one from settings,
// else the node's host with the server's port (omitted when it is the default).
func connectAddress(srv *store.Server, node *store.Node) (string, string) {
	var settings struct {
		ConnectAddress string `json:"connect_address"`
	}
	if len(srv.Settings) > 0 {
		_ = json.Unmarshal(srv.Settings, &settings)
	}
	if settings.ConnectAddress != "" {
		return settings.ConnectAddress, "custom"
	}
	host := ""
	if node != nil {
		host = node.FQDN
	}
	if srv.Port == 25565 || host == "" {
		return host, "node"
	}
	return net.JoinHostPort(host, strconv.Itoa(srv.Port)), "node"
}

// Identity answers GET /servers/{id}/identity.
func (h *ServerHandlers) Identity(w http.ResponseWriter, r *http.Request) {
	srv, err := h.store.GetServer(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeLookupError(w, r, "server", err)
		return
	}
	node, _ := h.store.GetNode(r.Context(), srv.NodeID)
	out := serverIdentity{}
	out.Address, out.AddressSource = connectAddress(srv, node)
	if node != nil {
		h.readIdentityFiles(r.Context(), srv, node, &out)
	}
	writeJSON(w, http.StatusOK, out)
}

// readIdentityFiles fills in what the server's files say. Best effort: a node
// that is down leaves these out rather than failing the whole answer.
func (h *ServerHandlers) readIdentityFiles(ctx context.Context, srv *store.Server, node *store.Node, out *serverIdentity) {
	c := agent.New(node.Scheme, node.FQDN, node.Port, node.Token)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := c.RegisterDir(ctx, srv.ID, srv.DirectoryPath); err != nil {
		return
	}
	if props, err := c.ReadFile(ctx, srv.ID, "/server.properties", maxIdentityProps); err == nil {
		out.FilesRead = true
		values := parseProperties(props)
		if v, ok := values["max-players"]; ok {
			if n, err := strconv.Atoi(v); err == nil {
				out.MaxPlayers = &n
			}
		}
		if v, ok := values["motd"]; ok {
			plain := strings.TrimSpace(formatCodeRe.ReplaceAllString(v, ""))
			out.MOTD, out.MOTDPlain = &v, &plain
		}
	}
	if icon, err := c.ReadFile(ctx, srv.ID, "/server-icon.png", maxIconBytes+1); err == nil &&
		len(icon) <= maxIconBytes && bytes.HasPrefix(icon, pngMagic) {
		url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(icon)
		out.Icon = &url
	}
}

// parseProperties reads a Java properties file's simple key=value lines,
// decoding the \uXXXX escapes the server writes for non-ASCII MOTDs.
func parseProperties(body []byte) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		v = unicodeEscapes.ReplaceAllStringFunc(v, func(m string) string {
			n, err := strconv.ParseUint(m[2:], 16, 32)
			if err != nil {
				return m
			}
			return string(rune(n))
		})
		v = strings.NewReplacer(`\:`, ":", `\=`, "=", `\\`, `\`).Replace(v)
		out[strings.TrimSpace(k)] = v
	}
	return out
}
