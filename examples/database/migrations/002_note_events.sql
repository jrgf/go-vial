CREATE TABLE note_events (
    id bigserial PRIMARY KEY,
    note_id bigint NOT NULL REFERENCES notes(id),
    action text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
