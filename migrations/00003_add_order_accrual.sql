-- +goose Up
ALTER TABLE orders ADD COLUMN accrual BIGINT
    CONSTRAINT orders_accrual_non_negative CHECK (accrual >= 0);

COMMENT ON COLUMN orders.accrual IS 'Начисленные баллы в копейках';

-- +goose Down
ALTER TABLE orders DROP COLUMN IF EXISTS accrual;
