package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/mcsm/api/internal/agent"
	"github.com/mcsm/api/internal/operations"
	"github.com/mcsm/api/internal/store"
)

// An asynchronous world upload (?async=true) answers once the bytes have
// arrived and the archive has been checked, then follows the node's unpack as
// a world_upload operation with byte-level progress. The operation is taken
// before the upload streams, so a start or restore cannot begin while the
// world is being replaced.

// WithOperations enables asynchronous world uploads.
func (h *FileHandlers) WithOperations(ops *operations.Manager) *FileHandlers {
	h.ops = ops
	return h
}

// bufferedResponse captures a small proxied response instead of streaming it.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header         { return b.header }
func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
func (b *bufferedResponse) WriteHeader(status int)      { b.status = status }

// worldJobPoll is how often a running unpack is checked (shortened in tests).
var worldJobPoll = time.Second

func (h *FileHandlers) uploadWorldAsync(w http.ResponseWriter, r *http.Request, srv *store.Server, c *agent.Client, ctx context.Context) {
	actor := operations.ActorFromRequest(r, clientIP(r))
	op, err := h.ops.Begin(r.Context(), srv.ID, operations.KindWorldLoad, actor, operations.Options{
		Timeout: 2 * time.Hour, Phase: "uploading", Msg: "Receiving the world",
	})
	if err != nil {
		writeLifecycleError(w, r, err)
		return
	}

	res := &bufferedResponse{header: http.Header{}, status: http.StatusOK}
	c.ProxyHTTP(ctx, res, r, "/agent/v1/servers/"+srv.ID+"/worlds/upload")
	var accepted struct {
		JobID string `json:"job_id"`
		Name  string `json:"name"`
		Files int    `json:"files"`
		Bytes int64  `json:"bytes"`
	}
	if res.status != http.StatusAccepted || json.Unmarshal(res.body.Bytes(), &accepted) != nil || accepted.JobID == "" {
		// The node refused or predates async uploads: pass its answer on.
		op.Done(fmt.Errorf("the upload was not accepted (status %d)", res.status), nil)
		for k, v := range res.header {
			w.Header()[k] = v
		}
		w.WriteHeader(res.status)
		_, _ = w.Write(res.body.Bytes())
		auditOutcome(h.store, r, srv.ID, "world.upload", map[string]any{"name": r.URL.Query().Get("name")}, res.status)
		return
	}
	op.SetRef(accepted.JobID)
	frac := 0.0
	op.Progress("unpacking", "Unpacking "+accepted.Name, &frac)
	rec := op.Record()
	go h.followWorldJob(op, c, srv.ID, accepted.JobID, accepted.Name)

	auditOutcome(h.store, r, srv.ID, "world.upload", map[string]any{
		"name": accepted.Name, "overwrite": r.URL.Query().Get("overwrite") == "true", "operation_id": op.ID(),
	}, http.StatusAccepted)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": "unpacking", "name": accepted.Name, "files": accepted.Files, "bytes": accepted.Bytes,
		"operation": &rec,
	})
}

// followWorldJob polls the node's unpack job until it ends.
func (h *FileHandlers) followWorldJob(op *operations.Op, c *agent.Client, serverID, jobID, name string) {
	misses := 0
	for {
		select {
		case <-op.Context().Done():
			op.Done(op.Context().Err(), nil)
			return
		case <-time.After(worldJobPoll):
		}
		ctx, cancel := context.WithTimeout(op.Context(), 10*time.Second)
		job, err := c.WorldJob(ctx, serverID, jobID)
		cancel()
		if err != nil {
			// A node that forgot the job (restarted mid-unpack) will never
			// finish it; a brief network blip is retried.
			if misses++; errors.Is(err, agent.ErrNotFound) || misses > 30 {
				op.Done(fmt.Errorf("lost track of the unpack: %v", err), nil)
				return
			}
			continue
		}
		misses = 0
		switch job.Status {
		case "succeeded":
			op.Done(nil, job.Result)
			return
		case "failed":
			op.Done(errors.New(job.Error), nil)
			return
		default:
			if job.Total > 0 {
				f := float64(job.Done) / float64(job.Total)
				op.Progress("unpacking", "Unpacking "+name, &f)
			}
		}
	}
}
