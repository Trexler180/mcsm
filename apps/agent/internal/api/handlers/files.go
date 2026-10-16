package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/go-chi/chi/v5"
	agentfiles "github.com/mcsm/agent/internal/files"
	"github.com/mcsm/agent/internal/process"
)

type FileHandlers struct {
	mgr *process.Manager
}

// fileUploadRequestLimit keeps the existing 512 MiB file allowance while
// leaving room for multipart headers and boundaries. ParseMultipartForm's
// argument only controls memory use; MaxBytesReader is what bounds the total
// request (including temporary files on disk).
const (
	fileUploadDataLimit    = int64(512) << 20
	fileUploadRequestLimit = fileUploadDataLimit + (1 << 20)
)

func parseMultipartUpload(w http.ResponseWriter, r *http.Request, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	return r.ParseMultipartForm(64 << 20)
}

func multipartFilesFit(files []*multipart.FileHeader, limit int64) bool {
	var total int64
	for _, file := range files {
		if file.Size < 0 || file.Size > limit-total {
			return false
		}
		total += file.Size
	}
	return true
}

func NewFileHandlers(mgr *process.Manager) *FileHandlers {
	return &FileHandlers{mgr: mgr}
}

func (h *FileHandlers) base(r *http.Request) (string, error) {
	id := chi.URLParam(r, "id")
	dir, ok := h.mgr.GetDir(id)
	if !ok {
		return "", fmt.Errorf("server directory not registered")
	}
	return dir, nil
}

func (h *FileHandlers) List(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	listing, err := agentfiles.List(base, path)
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// writeFileErr answers a failed file operation with what it means for the
// person asking: a path that isn't there is "not found", one that already
// exists is a conflict, and a refusal by the operating system is forbidden.
// The OS error text (with its syscall and path) stays out of the answer.
func writeFileErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "no such file or folder")
	case errors.Is(err, fs.ErrExist):
		writeError(w, http.StatusConflict, "something with that name already exists")
	case errors.Is(err, fs.ErrPermission):
		writeError(w, http.StatusForbidden, "the server's files don't allow that")
	default:
		var pe *fs.PathError
		if errors.As(err, &pe) {
			writeError(w, http.StatusInternalServerError, pe.Op+" failed: "+pe.Err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (h *FileHandlers) Tree(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	depth := atoiDefault(r.URL.Query().Get("depth"), 0)
	max := atoiDefault(r.URL.Query().Get("max"), 0)
	// sizes=1 also totals each child's size; agents from before it ignore
	// the parameter and the response simply has no sizes.
	tree, err := agentfiles.ListTreeWith(base, path, agentfiles.TreeOptions{
		MaxDepth:   depth,
		MaxEntries: max,
		Sizes:      r.URL.Query().Get("sizes") == "1",
	})
	if err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

func (h *FileHandlers) GetContent(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	// tail_bytes is optional and additive: absent means the whole file, which is
	// what every caller written before this parameter existed asks for.
	data, err := agentfiles.ReadContentTail(base, path, int64(atoiDefault(r.URL.Query().Get("tail_bytes"), 0)))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

func (h *FileHandlers) PutContent(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	if err := agentfiles.WriteContent(base, path, data); err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (h *FileHandlers) Delete(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	if err := agentfiles.Delete(base, path); err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (h *FileHandlers) Rename(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := agentfiles.Rename(base, body.From, body.To); err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

// Hashes computes per-file fingerprints (sha512 + CurseForge murmur2) for each
// requested server-relative path and returns a path->fingerprint map. Unreadable
// paths are omitted so one bad entry doesn't fail the batch. Used by the panel to
// recognize imported jars against Modrinth (sha512) and CurseForge (murmur2).
func (h *FileHandlers) Hashes(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	type fileFP struct {
		SHA512  string `json:"sha512"`
		Murmur2 uint32 `json:"murmur2"`
	}
	out := make(map[string]fileFP, len(body.Paths))
	for _, p := range body.Paths {
		if sha, mur, err := agentfiles.FileFingerprints(base, p); err == nil {
			out[p] = fileFP{SHA512: sha, Murmur2: mur}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": out})
}

func (h *FileHandlers) Mkdir(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := agentfiles.Mkdir(base, body.Path); err != nil {
		writeFileErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
}

func (h *FileHandlers) Download(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	path := r.URL.Query().Get("path")

	isDir, err := agentfiles.IsDir(base, path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if isDir {
		name := filepath.Base(path)
		if name == "." || name == "" {
			name = "files"
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, name))
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusOK)
		if err := agentfiles.ZipDir(base, path, w); err != nil {
			return
		}
	} else {
		f, info, err := agentfiles.OpenFile(base, path)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		defer f.Close()
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(path)))
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	}
}

func (h *FileHandlers) Upload(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/"
	}

	if err := parseMultipartUpload(w, r, fileUploadRequestLimit); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds 512 MiB limit")
			return
		}
		writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}
	defer r.MultipartForm.RemoveAll()

	files := r.MultipartForm.File["files"]
	if !multipartFilesFit(files, fileUploadDataLimit) {
		writeError(w, http.StatusRequestEntityTooLarge, "upload exceeds 512 MiB limit")
		return
	}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		err = agentfiles.WriteUpload(base, dirPath, fh.Filename, f)
		f.Close()
		if err != nil {
			writeFileErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": len(files)})
}
