package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
