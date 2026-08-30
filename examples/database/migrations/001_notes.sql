CREATE TABLE notes (
    id bigserial PRIMARY KEY,
    body text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
