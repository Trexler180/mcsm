package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// worldJobs tracks background world unpacks. Jobs live in memory: the panel
// follows one for minutes, and a job lost to an agent restart is reported as
// unknown, which the panel treats as failed.
type worldJobs struct {
	mu   sync.Mutex
	jobs map[string]*worldJob
}

// worldJobKeep is how long a finished job stays readable.
const worldJobKeep = time.Hour

type worldJob struct {
	ID string

	mu       sync.Mutex
	status   string // running | succeeded | failed
	done     int64
	total    int64
	err      string
	result   map[string]any
	finished time.Time
}

// WorldJobStatus is what GET /worlds/jobs/{id} answers.
type WorldJobStatus struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Done   int64          `json:"done_bytes"`
	Total  int64          `json:"total_bytes"`
	Error  string         `json:"error,omitempty"`
	Result map[string]any `json:"result,omitempty"`
}

func (j *worldJobs) start(total int64, result map[string]any) *worldJob {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	job := &worldJob{ID: hex.EncodeToString(b), status: "running", total: total, result: result}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.jobs == nil {
		j.jobs = map[string]*worldJob{}
	}
	for id, old := range j.jobs {
		old.mu.Lock()
		expired := !old.finished.IsZero() && time.Since(old.finished) > worldJobKeep
		old.mu.Unlock()
		if expired {
			delete(j.jobs, id)
		}
	}
	j.jobs[job.ID] = job
	return job
}

func (j *worldJobs) get(id string) (*worldJob, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	job, ok := j.jobs[id]
	return job, ok
}

func (job *worldJob) progress(done, total int64) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.done, job.total = done, total
}

func (job *worldJob) finish(err error) {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.finished = time.Now()
	if err != nil {
		job.status, job.err = "failed", err.Error()
		return
	}
	job.status, job.done = "succeeded", job.total
}

func (job *worldJob) snapshot() WorldJobStatus {
	job.mu.Lock()
	defer job.mu.Unlock()
	s := WorldJobStatus{ID: job.ID, Status: job.status, Done: job.done, Total: job.total, Error: job.err}
	if job.status == "succeeded" {
		s.Result = job.result
	}
	return s
}
