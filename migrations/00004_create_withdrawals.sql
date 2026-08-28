-- +goose Up
CREATE TABLE withdrawals (
    id BIGSERIAL PRIMARY KEY CHECK (id > 0),
    user_id BIGINT NOT NULL REFERENCES users (id),
    order_number TEXT NOT NULL,
    sum BIGINT NOT NULL CONSTRAINT withdrawals_sum_positive CHECK (sum > 0),
    processed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT withdrawals_order_number_key UNIQUE (order_number)
);

COMMENT ON COLUMN withdrawals.sum IS 'Списанные баллы в копейках';

-- +goose Down
DROP TABLE IF EXISTS withdrawals;
