package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// slowBody writes total bytes in chunks spread over roughly span, so the request
// takes longer to arrive than the server's ReadTimeout allows.
func slowBody(chunks int, gap time.Duration) io.Reader {
	pr, pw := io.Pipe()
	go func() {
		for i := 0; i < chunks; i++ {
			time.Sleep(gap)
			if _, err := pw.Write([]byte("0123456789")); err != nil {
				return
			}
		}
		pw.Close()
	}()
	return pr
}

func echoLength(w http.ResponseWriter, r *http.Request) {
	n, err := io.Copy(io.Discard, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fmt.Fprintf(w, "%d", n)
}

func serveWithReadTimeout(t *testing.T, h http.Handler, timeout time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout = timeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

// A multipart upload slower than ReadTimeout must still arrive intact. This is
// the world-upload regression: a 133 MB archive over a home connection takes
// minutes, and ReadTimeout covers the whole request body, not just the headers.
func TestUploadDeadlineSurvivesSlowMultipartBody(t *testing.T) {
	srv := serveWithReadTimeout(t, UploadDeadline(time.Minute)(http.HandlerFunc(echoLength)), 300*time.Millisecond)

	req, err := http.NewRequest(http.MethodPost, srv.URL, slowBody(6, 150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("slow multipart upload was severed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if string(body) != "60" {
		t.Fatalf("body truncated: read %s of 60 bytes", body)
	}
}

// Control: the same slow body without a multipart content type is still cut off,
// which proves the test above is really exercising ReadTimeout and not just
// passing because the harness never enforced one.
func TestUploadDeadlineLeavesNonMultipartAlone(t *testing.T) {
	srv := serveWithReadTimeout(t, UploadDeadline(time.Minute)(http.HandlerFunc(echoLength)), 300*time.Millisecond)

	req, err := http.NewRequest(http.MethodPost, srv.URL, slowBody(6, 150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return // severed, as expected
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("expected ReadTimeout to sever a slow non-multipart body")
	}
}

// The deadline has to be reachable through whatever wraps the ResponseWriter;
// Logger's wrapper returning ErrNotSupported is exactly how this fix would
// regress silently.
func TestUploadDeadlineThroughLoggerWrapper(t *testing.T) {
	srv := serveWithReadTimeout(t, Logger(UploadDeadline(time.Minute)(http.HandlerFunc(echoLength))), 300*time.Millisecond)

	req, err := http.NewRequest(http.MethodPost, srv.URL, slowBody(6, 150*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("slow multipart upload was severed behind the logging wrapper: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
