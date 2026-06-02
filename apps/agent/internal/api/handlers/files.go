package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	agentfiles "github.com/mcsm/agent/internal/files"
	"github.com/mcsm/agent/internal/process"
)

type FileHandlers struct {
	mgr *process.Manager
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

func (h *FileHandlers) GetContent(w http.ResponseWriter, r *http.Request) {
	base, err := h.base(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	path := r.URL.Query().Get("path")
	data, err := agentfiles.ReadContent(base, path)
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		abs, err := agentfiles.ResolveExisting(base, path)
		if err != nil {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(abs)))
		http.ServeFile(w, r, abs)
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

	if err := r.ParseMultipartForm(512 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}

	files := r.MultipartForm.File["files"]
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		err = agentfiles.WriteUpload(base, dirPath, fh.Filename, f)
		f.Close()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploaded": len(files)})
}
