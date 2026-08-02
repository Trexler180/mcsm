package handlers

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	agentfiles "github.com/mcsm/agent/internal/files"
	"github.com/mcsm/agent/internal/process"
	"github.com/shirou/gopsutil/v4/disk"
)

// WorldHandlers serves world-level operations on a server directory. Uploading
// a world is deliberately not just a file upload: the archive has to be
// inspected for a level.dat, unpacked into a folder of its own, and kept away
// from a world the server currently has open.
type WorldHandlers struct {
	mgr *process.Manager
}

func NewWorldHandlers(mgr *process.Manager) *WorldHandlers {
	return &WorldHandlers{mgr: mgr}
}

func (h *WorldHandlers) base(r *http.Request) (string, error) {
	id := chi.URLParam(r, "id")
	dir, ok := h.mgr.GetDir(id)
	if !ok {
		return "", fmt.Errorf("server directory not registered")
	}
	return dir, nil
}

// serverHoldsWorld reports whether the running server has this world open. A
// live server keeps chunks in memory and rewrites region files on its own
// schedule, so replacing the directory underneath it corrupts both the upload
// and whatever the server flushes next.
func (h *WorldHandlers) serverHoldsWorld(id, dir, name string) bool {
	switch h.mgr.Status(id).Status {
	case process.StatusOffline, process.StatusCrashed, process.StatusStartupFailure:
		return false
	}
	return strings.EqualFold(name, process.LevelName(dir))
}

// unpackBlockedMsg refuses an import that would leave the filesystem
// dangerously full, mirroring the guard backups use — a world that fills the
// disk takes down every server on the node, not just this one.
func unpackBlockedMsg(free, total, estimate int64) string {
	if total <= 0 {
		return ""
	}
	floor := dangerFloor(total)
	if free-estimate >= floor {
		return ""
	}
	return fmt.Sprintf(
		"not enough disk space to unpack this world: it needs up to %.1f GB but only %.1f GB is free, and at least %.1f GB must stay free",
		float64(estimate)/float64(gib), float64(free)/float64(gib), float64(floor)/float64(gib))
}

// Upload accepts a zipped world and installs it as a folder in the server root.
//
// The request is a multipart body with one file part; it is streamed to a temp
// file beside the server directory because a zip's index lives at its end and
// can't be read from a stream. The name query parameter picks the destination
// folder (defaulting to the folder the archive wraps the world in), and
// overwrite=true is required to replace a world that already exists.
func (h *WorldHandlers) Upload(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "create server directory: "+err.Error())
		return
	}

	overwrite := r.URL.Query().Get("overwrite") == "true"
	requested := strings.TrimSpace(r.URL.Query().Get("name"))

	// Everything we can check before reading the body, we check before reading
	// the body — there is no point streaming several gigabytes only to reject
	// the name at the end.
	if requested != "" {
		name, err := agentfiles.CleanWorldName(requested)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if msg, code := h.checkDestination(id, base, name, overwrite); msg != "" {
			writeError(w, code, msg)
			return
		}
	}
	// The upload itself has to fit on disk alongside the world it unpacks into.
	if du, derr := disk.Usage(base); derr == nil && du != nil && r.ContentLength > 0 {
		if msg := unpackBlockedMsg(int64(du.Free), int64(du.Total), r.ContentLength*2); msg != "" {
			writeError(w, http.StatusInsufficientStorage, msg)
			return
		}
	}

	part, err := firstFilePart(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	defer part.Close()

	// Staged inside the server directory so it lands on the same filesystem the
	// world unpacks to, which is the one the space check measured.
	tmp, err := os.CreateTemp(base, ".mcsm-world-upload-*.zip")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create upload staging file: "+err.Error())
		return
	}
	defer os.Remove(tmp.Name())
	_, copyErr := io.Copy(tmp, part)
	closeErr := tmp.Close()
	if copyErr != nil {
		writeError(w, http.StatusBadRequest, "upload interrupted: "+copyErr.Error())
		return
	}
	if closeErr != nil {
		writeError(w, http.StatusInternalServerError, "save upload: "+closeErr.Error())
		return
	}

	arc, err := agentfiles.InspectWorldZip(tmp.Name())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Fall back to the folder the archive wraps the world in; a world zipped at
	// its own root carries no name, so the caller has to supply one.
	name := requested
	if name == "" {
		name = arc.SuggestedName
	}
	if name == "" {
		writeError(w, http.StatusBadRequest,
			"this archive has the world at its top level, so it has no folder name — give the world a name and upload it again")
		return
	}
	name, err = agentfiles.CleanWorldName(name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Re-checked here because the name may have come from the archive, and
	// because the server could have been started while the upload streamed.
	if msg, code := h.checkDestination(id, base, name, overwrite); msg != "" {
		writeError(w, code, msg)
		return
	}
	if du, derr := disk.Usage(base); derr == nil && du != nil {
		if msg := unpackBlockedMsg(int64(du.Free), int64(du.Total), arc.Bytes); msg != "" {
			writeError(w, http.StatusInsufficientStorage, msg)
			return
		}
	}

	replaced, _ := agentfiles.WorldExists(base, name)
	if err := agentfiles.ExtractWorld(base, tmp.Name(), arc, name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"name":     name,
		"files":    arc.Files,
		"bytes":    arc.Bytes,
		"replaced": replaced,
	})
}

// checkDestination returns a refusal message and status for a destination that
// can't be written, or "" when the import may proceed.
func (h *WorldHandlers) checkDestination(id, base, name string, overwrite bool) (string, int) {
	if h.serverHoldsWorld(id, base, name) {
		return fmt.Sprintf("%q is the world this server currently has open; stop the server before replacing it", name), http.StatusConflict
	}
	exists, err := agentfiles.WorldExists(base, name)
	if err != nil {
		return "check destination: " + err.Error(), http.StatusInternalServerError
	}
	if exists && !overwrite {
		return fmt.Sprintf("a world folder named %q already exists", name), http.StatusConflict
	}
	if !exists {
		// A file (not a directory) sitting on the name would make the swap fail
		// deep inside extraction; catch it here where the message is useful.
		if _, err := os.Lstat(filepath.Join(base, name)); err == nil {
			return fmt.Sprintf("%q already exists in the server directory and isn't a world folder", name), http.StatusConflict
		}
	}
	return "", 0
}

// firstFilePart returns the first file part of a multipart request, read as a
// stream. ParseMultipartForm would buffer the whole archive through memory and
// a temp file we don't control, which for a multi-gigabyte world is exactly
// what we're trying to avoid.
func firstFilePart(r *http.Request) (*multipart.Part, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, errors.New("expected a multipart upload")
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return nil, errors.New("no file was included in the upload")
		}
		if err != nil {
			return nil, fmt.Errorf("read upload: %w", err)
		}
		if part.FileName() != "" {
			return part, nil
		}
		part.Close()
	}
}
