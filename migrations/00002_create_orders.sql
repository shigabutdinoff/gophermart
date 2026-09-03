-- +goose Up
CREATE TABLE orders (
    id BIGSERIAL PRIMARY KEY CHECK (id > 0),
    number TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users (id),
    status TEXT NOT NULL DEFAULT 'NEW'
        CHECK (status IN ('NEW', 'PROCESSING', 'INVALID', 'PROCESSED')),
    uploaded_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT orders_number_key UNIQUE (number)
);

-- +goose Down
DROP TABLE IF EXISTS orders;
