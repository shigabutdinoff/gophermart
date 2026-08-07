-- +goose Up
CREATE TABLE users (
    id BIGSERIAL PRIMARY KEY CHECK (id > 0),
    login VARCHAR(255) NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT users_login_key UNIQUE (login)
);

-- +goose Down
DROP TABLE IF EXISTS users;
