--  Copyright 2025 Canonical Ltd.
--  SPDX-License-Identifier: AGPL-3.0

-- +goose Up
-- +goose StatementBegin

CREATE TABLE IF NOT EXISTS groups
(
    id UUID NOT NULL,
    name VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL DEFAULT 'default',
    description TEXT,
    type SMALLINT NOT NULL DEFAULT 0,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now(),

    PRIMARY KEY (id, tenant_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_name ON groups(tenant_id, name);

CREATE TABLE IF NOT EXISTS group_members
(
    group_id UUID NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL DEFAULT 'default',
    role SMALLINT NOT NULL DEFAULT 0,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now(),

    PRIMARY KEY (group_id, user_id),

    FOREIGN KEY (group_id, tenant_id)
        REFERENCES groups(id, tenant_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_group_members_user_id ON group_members(tenant_id, user_id);

CREATE TABLE IF NOT EXISTS application_groups
(
    group_id UUID NOT NULL,
    application_id VARCHAR(255) NOT NULL,
    tenant_id VARCHAR(255) NOT NULL DEFAULT 'default',

    created_at TIMESTAMP WITH TIME ZONE DEFAULT now(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT now(),

    PRIMARY KEY (group_id, application_id),

    FOREIGN KEY (group_id, tenant_id)
        REFERENCES groups(id, tenant_id)
        ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_application_groups_application_id ON application_groups(tenant_id, application_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP INDEX IF EXISTS idx_application_groups_application_id;
DROP INDEX IF EXISTS idx_group_members_user_id;
DROP INDEX IF EXISTS idx_groups_name;

DROP TABLE IF EXISTS application_groups;
DROP TABLE IF EXISTS group_members;
DROP TABLE IF EXISTS groups;

-- +goose StatementEnd
