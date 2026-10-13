-- +goose Up

-- One row per firing of a scheduled task, so the schedules page can show past
-- runs and their failures. Before this a failed run left a line in the API's
-- log and nothing anyone could see. operation_id links a run that started a
-- tracked operation (a backup, a safe update). Times are unix seconds.
CREATE TABLE scheduled_task_runs (
    id           TEXT PRIMARY KEY,
    task_id      TEXT NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    server_id    TEXT NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    action       TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'running', -- running|succeeded|failed|skipped
    message      TEXT NOT NULL DEFAULT '',
    operation_id TEXT,
    started_at   INTEGER NOT NULL,
    finished_at  INTEGER
);

CREATE INDEX scheduled_task_runs_task ON scheduled_task_runs(task_id, started_at DESC);
CREATE INDEX scheduled_task_runs_server ON scheduled_task_runs(server_id, started_at DESC);

-- +goose Down
DROP TABLE IF EXISTS scheduled_task_runs;
