-- +goose Up
CREATE TABLE random_games (
    owner UUID NOT NULL,
    id UUID NOT NULL,
    difficulty TEXT NOT NULL CHECK (difficulty IN ('easy', 'medium', 'hard', 'expert', 'master')),
    givens TEXT NOT NULL CHECK (givens ~ '^[0-9]{81}$'),
    entries TEXT NOT NULL CHECK (entries ~ '^[0-9]{81}$'),
    notes TEXT NOT NULL CHECK (notes ~ '^([0-7][0-9A-Za-z_-]){81}$'),
    revision BIGINT NOT NULL CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    CHECK (completed_at IS NULL OR archived),
    PRIMARY KEY (owner, id)
);
CREATE INDEX random_games_list ON random_games (owner, archived, updated_at DESC, id DESC);

CREATE TABLE puzzle_collections (
    key TEXT PRIMARY KEY,
    digest BYTEA NOT NULL CHECK (octet_length(digest) = 32)
);

CREATE TABLE puzzle_attempts (
    owner UUID NOT NULL,
    collection TEXT NOT NULL REFERENCES puzzle_collections (key),
    slot INTEGER NOT NULL CHECK (slot BETWEEN 1 AND 256),
    entries TEXT NOT NULL CHECK (entries ~ '^[0-9]{81}$'),
    notes TEXT NOT NULL CHECK (notes ~ '^([0-7][0-9A-Za-z_-]){81}$'),
    revision BIGINT NOT NULL CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (owner, collection, slot)
);

-- +goose Down
DROP TABLE puzzle_attempts;
DROP TABLE puzzle_collections;
DROP TABLE random_games;
