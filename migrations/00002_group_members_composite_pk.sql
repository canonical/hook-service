--  Copyright 2026 Canonical Ltd.
--  SPDX-License-Identifier: AGPL-3.0-only

-- +goose Up
-- +goose StatementBegin

ALTER TABLE group_members DROP CONSTRAINT group_members_pkey;
ALTER TABLE group_members ADD PRIMARY KEY (group_id, role, user_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DELETE FROM group_members a USING group_members b
WHERE a.group_id = b.group_id AND a.user_id = b.user_id AND a.role > b.role;
ALTER TABLE group_members DROP CONSTRAINT group_members_pkey;
ALTER TABLE group_members ADD PRIMARY KEY (group_id, user_id);

-- +goose StatementEnd
