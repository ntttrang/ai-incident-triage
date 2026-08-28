CREATE TABLE incidents (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source            TEXT NOT NULL,
    external_id       TEXT NOT NULL,
    summary           TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    issue_type        TEXT NOT NULL DEFAULT '',
    priority          TEXT NOT NULL DEFAULT '',
    labels            TEXT[] NOT NULL DEFAULT '{}',
    reporter          TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'received',
    raw               JSONB NOT NULL,
    last_delivery_id  TEXT,
    category          TEXT,
    severity          TEXT,
    priority_score    INTEGER,
    confidence        DOUBLE PRECISION,
    rationale         TEXT,
    suggested_runbook TEXT,
    classified_at     TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Row identity: one incident per source issue.
    CONSTRAINT incidents_issue_unique UNIQUE (source, external_id),
    -- Delivery dedup: replays of the same X-Atlassian-Webhook-Identifier are
    -- no-ops. Postgres unique constraints ignore NULLs, so deliveries without
    -- the header still insert. Known edge, accepted: two rapid updates to the
    -- same issue can race past this constraint; retries of one delivery are
    -- the failure mode Jira actually produces.
    CONSTRAINT incidents_delivery_unique UNIQUE (source, last_delivery_id),
    CONSTRAINT incidents_status_check CHECK (status IN ('received', 'queued', 'classified', 'failed'))
);

CREATE INDEX incidents_status_idx ON incidents (status);
CREATE INDEX incidents_severity_idx ON incidents (severity);
CREATE INDEX incidents_created_at_idx ON incidents (created_at DESC);
