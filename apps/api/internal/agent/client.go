package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(scheme, fqdn string, port int, token string) *Client {
	return &Client{
		BaseURL: fmt.Sprintf("%s://%s:%d", scheme, fqdn, port),
		Token:   token,
		// Generous absolute timeout — per-call deadlines come from the
		// passed context (StartServer waits through JAR auto-download,
		// Backup waits through zipping a multi-GB world, etc.).
		HTTP: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

func (c *Client) Health(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/agent/v1/health", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) Info(ctx context.Context) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/agent/v1/info", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// JavaInstallations returns the agent host's detected Java runtimes and OS,
// proxied verbatim to the panel ({installations: [...], os: "windows"|...}).
func (c *Client) JavaInstallations(ctx context.Context) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/agent/v1/java", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

// ImportCandidate is an existing server directory the agent found on disk, with
// best-effort detected settings the panel pre-fills into the import dialog.
type ImportCandidate struct {
	Directory      string `json:"directory"`
	AbsPath        string `json:"abs_path"`
	Platform       string `json:"platform"`
	MCVersion      string `json:"mc_version"`
	JarFile        string `json:"jar_file"`
	Port           int    `json:"port"`
	HasWorld       bool   `json:"has_world"`
	EULA           bool   `json:"eula_accepted"`
	ModCount       int    `json:"mod_count"`
	PluginCount    int    `json:"plugin_count"`
	HasRuntimeFile bool   `json:"has_runtime_file"`
}

// ScanImports asks the agent for importable server directories under its server
// root, each with detected settings. Read-only on the agent side.
func (c *Client) ScanImports(ctx context.Context) ([]ImportCandidate, error) {
	resp, err := c.do(ctx, http.MethodGet, "/agent/v1/import/scan", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	var out []ImportCandidate
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) StartServer(ctx context.Context, serverID string, cfg map[string]any) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/start", cfg)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("agent error: %s", e["error"])
	}
	return nil
}

// Reinstall asks the agent to wipe and re-fetch the server runtime for the given
// platform/version (used when changing Minecraft or loader versions).
func (c *Client) Reinstall(ctx context.Context, serverID string, cfg map[string]any) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/reinstall", cfg)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("agent error: %s", e["error"])
	}
	return nil
}

func (c *Client) StopServer(ctx context.Context, serverID string, graceful bool, timeoutSec int) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/stop", map[string]any{
		"graceful":    graceful,
		"timeout_sec": timeoutSec,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

func (c *Client) RestartServer(ctx context.Context, serverID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/restart", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

func (c *Client) KillServer(ctx context.Context, serverID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/kill", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

func (c *Client) GetStatus(ctx context.Context, serverID string) (map[string]any, error) {
	resp, err := c.do(ctx, http.MethodGet, "/agent/v1/servers/"+serverID+"/status", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	return result, nil
}

// DisableConflictMods asks the agent to disable (rename to .disabled) the jars
// matching the given Fabric mod ids. Returns the disabled filenames.
func (c *Client) DisableConflictMods(ctx context.Context, serverID string, modIDs []string) ([]string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/mods/disable", map[string]any{
		"mod_ids": modIDs,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		return nil, fmt.Errorf("agent: %s", e["error"])
	}
	var out struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Disabled, nil
}

func (c *Client) SendCommand(ctx context.Context, serverID, command string) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/command", map[string]string{
		"command": command,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

func (c *Client) RegisterDir(ctx context.Context, serverID, directory string) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/register", map[string]string{
		"directory": directory,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

// Setup creates the server directory and writes eula.txt if missing.
func (c *Client) Setup(ctx context.Context, serverID, directory string) error {
	path := fmt.Sprintf("/agent/v1/servers/%s/setup?dir=%s", serverID, url.QueryEscape(directory))
	resp, err := c.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

type BackupResult struct {
	BackupID  string `json:"backup_id"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
}

// Backup zips the server directory on the agent host. The agent decides where
// to store the zip; the panel records the metadata.
func (c *Client) Backup(ctx context.Context, serverID, backupID string) (*BackupResult, error) {
	path := fmt.Sprintf("/agent/v1/servers/%s/backup?backup_id=%s",
		serverID, url.QueryEscape(backupID))
	resp, err := c.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		return nil, fmt.Errorf("agent backup: %s", e["error"])
	}
	var out BackupResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Restore asks the agent to stop the server, wipe its directory, and extract a
// previously-created backup zip back into place.
func (c *Client) Restore(ctx context.Context, serverID, backupID string) error {
	path := fmt.Sprintf("/agent/v1/servers/%s/backups/%s/restore", serverID, url.PathEscape(backupID))
	resp, err := c.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

// DeleteBackup removes a backup zip on the agent host.
func (c *Client) DeleteBackup(ctx context.Context, serverID, backupID string) error {
	path := fmt.Sprintf("/agent/v1/servers/%s/backups/%s", serverID, url.PathEscape(backupID))
	resp, err := c.do(ctx, http.MethodDelete, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

// PurgeServer asks the agent to permanently delete a server's on-disk data.
// files wipes the live server directory; backups wipes the sibling backups
// folder. Either can be set independently.
func (c *Client) PurgeServer(ctx context.Context, serverID, directory string, files, backups bool) error {
	resp, err := c.do(ctx, http.MethodDelete, "/agent/v1/servers/"+serverID, map[string]any{
		"directory": directory,
		"files":     files,
		"backups":   backups,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

// UploadFile streams a local file to the agent's upload endpoint without
// buffering the whole file in memory: an io.Pipe feeds a multipart writer that
// copies straight from disk.
func (c *Client) UploadFile(ctx context.Context, serverID, destDir, filename, localPath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open temp: %w", err)
	}
	defer f.Close()

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		fw, err := mw.CreateFormFile("files", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(fw, f); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(mw.Close())
	}()

	uploadURL := fmt.Sprintf("%s/agent/v1/servers/%s/files/upload?path=%s",
		c.BaseURL, serverID, url.QueryEscape(destDir))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("upload to agent failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("agent upload returned %d", resp.StatusCode)
	}
	return nil
}

// FileEntry is one item in an agent directory listing (mirrors the agent's
// files.Entry wire shape; only the fields the panel needs are decoded).
type FileEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" | "dir"
	Size int64  `json:"size"`
}

// FileListing is the agent's response for a directory listing.
type FileListing struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

// ListFiles returns the agent's listing of one directory within the server
// directory (server-relative path like "/mods"). The caller must have called
// RegisterDir first so the agent knows the server's base path.
func (c *Client) ListFiles(ctx context.Context, serverID, path string) (*FileListing, error) {
	p := fmt.Sprintf("/agent/v1/servers/%s/files?path=%s", serverID, url.QueryEscape(path))
	resp, err := c.do(ctx, http.MethodGet, p, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	var out FileListing
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

// FileFingerprint identifies a jar against upstream indexes: SHA512 for Modrinth,
// Murmur2 (CurseForge's whitespace-stripped MurmurHash2) for CurseForge.
type FileFingerprint struct {
	SHA512  string `json:"sha512"`
	Murmur2 uint32 `json:"murmur2"`
}

// HashFiles asks the agent to fingerprint each given server-relative path,
// returning a path->fingerprint map. Paths the agent couldn't read are omitted.
// Hashing happens on the agent so jar bytes never cross the network.
func (c *Client) HashFiles(ctx context.Context, serverID string, paths []string) (map[string]FileFingerprint, error) {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/files/hashes",
		map[string]any{"paths": paths})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	var out struct {
		Files map[string]FileFingerprint `json:"files"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Files, nil
}

// DeleteFile removes a file in the server directory. A 404 is treated as
// success — the file is already gone, which is what the caller wanted.
func (c *Client) DeleteFile(ctx context.Context, serverID, path string) error {
	delURL := fmt.Sprintf("%s/agent/v1/servers/%s/files?path=%s",
		c.BaseURL, serverID, url.QueryEscape(path))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, delURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("agent returned %d", resp.StatusCode)
	}
	return nil
}

// RenameFile moves a file within the server directory on the agent (used to
// toggle the .disabled suffix). from/to are server-relative paths like
// "/mods/foo.jar".
func (c *Client) RenameFile(ctx context.Context, serverID, from, to string) error {
	resp, err := c.do(ctx, http.MethodPost, "/agent/v1/servers/"+serverID+"/files/rename",
		map[string]string{"from": from, "to": to})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return checkError(resp)
}

// StartConfig builds the /start payload for a server. When no explicit -Xmx is
// present in jvmArgs, the panel's RAM settings are translated to -Xms/-Xmx.
func StartConfig(directory, javaBinary string, jvmArgs []string, platform, mcVersion string, ramMbMin, ramMbMax int) map[string]any {
	hasXmx := false
	for _, a := range jvmArgs {
		if strings.HasPrefix(a, "-Xmx") {
			hasXmx = true
			break
		}
	}
	if !hasXmx {
		jvmArgs = append(append([]string{}, jvmArgs...),
			"-Xms"+ramArg(ramMbMin),
			"-Xmx"+ramArg(ramMbMax),
		)
	}
	return map[string]any{
		"directory":   directory,
		"java_binary": javaBinary,
		"jvm_args":    jvmArgs,
		"start_args":  []string{"nogui"},
		"platform":    platform,
		"mc_version":  mcVersion,
	}
}

func ramArg(mb int) string {
	if mb >= 1024 && mb%1024 == 0 {
		return fmt.Sprintf("%dg", mb/1024)
	}
	return fmt.Sprintf("%dm", mb)
}

// ProxyHTTP forwards an HTTP request to the agent and writes the response back.
func (c *Client) ProxyHTTP(ctx context.Context, w http.ResponseWriter, r *http.Request, agentPath string) {
	targetURL := c.BaseURL + agentPath
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	req, err := http.NewRequestWithContext(ctx, r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, `{"error":"proxy error"}`, http.StatusBadGateway)
		return
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	for k, v := range r.Header {
		if k == "Authorization" {
			continue
		}
		req.Header[k] = v
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		http.Error(w, `{"error":"agent unreachable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// WebSocketURL returns the agent WS URL for a given path.
func (c *Client) WebSocketURL(path string) string {
	u, _ := url.Parse(c.BaseURL + path)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	return u.String()
}

func checkError(resp *http.Response) error {
	if resp.StatusCode >= 400 {
		var e map[string]string
		json.NewDecoder(resp.Body).Decode(&e)
		return fmt.Errorf("agent: %s", e["error"])
	}
	return nil
}
