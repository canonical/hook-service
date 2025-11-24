--  Copyright 2025 Canonical Ltd.
--  SPDX-License-Identifier: AGPL-3.0

-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS groups
(
    id SERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL DEFAULT 'default',
    description TEXT,
    type SMALLINT NOT NULL DEFAULT 0,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_name ON groups(tenant_id, name);

CREATE TABLE IF NOT EXISTS group_members
(
    id SERIAL PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL DEFAULT 'default',
    role SMALLINT NOT NULL DEFAULT 0,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_group_members_user_id ON group_members(tenant_id, user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_group_members_unique ON group_members(tenant_id, group_id, user_id);

CREATE TABLE IF NOT EXISTS application_groups
(
    id SERIAL PRIMARY KEY,
    group_id INTEGER NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    application_id VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL DEFAULT 'default',

    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_application_groups_application_id ON application_groups(tenant_id, application_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_application_groups_unique ON application_groups(tenant_id, group_id, application_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_application_groups_unique;
DROP INDEX IF EXISTS idx_application_groups_application_id;
DROP INDEX IF EXISTS idx_group_members_unique;
DROP INDEX IF EXISTS idx_group_members_user_id;
DROP INDEX IF EXISTS idx_groups_name;

DROP TABLE IF EXISTS application_groups;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;

-- +goose StatementEnd
