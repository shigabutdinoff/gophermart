-- +goose Up
CREATE TABLE orders (
    id BIGSERIAL PRIMARY KEY CHECK (id > 0),
    number TEXT NOT NULL,
    number_hash BYTEA NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users (id),
    status TEXT NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED')),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT orders_number_hash_key UNIQUE (number_hash)
);

CREATE INDEX orders_user_uploaded_idx ON orders (user_id, uploaded_at DESC, id DESC);

-- +goose Down
DROP TABLE IF EXISTS orders;
