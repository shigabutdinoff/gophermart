-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY orders_user_processed_idx ON orders (user_id) WHERE status = 'PROCESSED';

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS orders_user_processed_idx;
