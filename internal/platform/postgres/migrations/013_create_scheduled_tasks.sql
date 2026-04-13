-- +goose Up
CREATE TABLE scheduled_tasks (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID NOT NULL REFERENCES users(id),
    conversation_id UUID REFERENCES conversations(id),
    name            VARCHAR(200) NOT NULL,
    prompt          TEXT NOT NULL,
    cron_expression VARCHAR(50) NOT NULL,
    timezone        VARCHAR(50) DEFAULT 'Asia/Shanghai',
    output_config   JSONB NOT NULL DEFAULT '{"targets":[{"type":"conversation"}]}',
    status          VARCHAR(20) DEFAULT 'active',
    max_tokens      INTEGER DEFAULT 50000,
    enable_tools    BOOLEAN DEFAULT true,
    last_run_at     TIMESTAMPTZ,
    last_run_status VARCHAR(20),
    total_runs      INTEGER DEFAULT 0,
    total_tokens    BIGINT DEFAULT 0,
    created_at      TIMESTAMPTZ DEFAULT now(),
    updated_at      TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_scheduled_tasks_user ON scheduled_tasks(user_id);
CREATE INDEX idx_scheduled_tasks_status ON scheduled_tasks(status);

CREATE TABLE task_executions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id         UUID NOT NULL REFERENCES scheduled_tasks(id) ON DELETE CASCADE,
    status          VARCHAR(20) NOT NULL,
    result_summary  TEXT,
    output_targets  JSONB,
    input_tokens    INTEGER,
    output_tokens   INTEGER,
    tools_used      JSONB,
    duration_ms     INTEGER,
    error_message   TEXT,
    created_at      TIMESTAMPTZ DEFAULT now()
);

CREATE INDEX idx_task_executions_task ON task_executions(task_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS task_executions;
DROP TABLE IF EXISTS scheduled_tasks;
