-- 000004_team_membership.up.sql
--
-- Teams stop being a label and start granting access.
--
-- Until now device_groups was decorative: a device carried a group_id, the dashboard
-- printed the group name, and not one authorization check ever looked at it. An Admin's
-- device set was exactly one scalar column, devices.assigned_admin_id, which meant every
-- device had to be handed to exactly one person by hand. This table is the missing half
-- of SRS FR-6.4 ("organizing devices into teams for Admin-level segregation"): it says
-- which portal users belong to which team, so team membership can confer visibility.
--
-- Membership is deliberately ADDITIVE, never restrictive. An Admin sees a device if it is
-- assigned to them OR it sits in a team they belong to. Making it restrictive (only
-- devices in my teams) would silently revoke access to every individually-assigned device
-- the moment this migration ran, and would make an empty membership table mean "no Admin
-- can see anything". Additive means existing installs behave identically until someone
-- actually adds a member.
CREATE TABLE IF NOT EXISTS user_group_members (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    group_id   UUID NOT NULL REFERENCES device_groups(id) ON DELETE CASCADE,
    added_by   UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, group_id)
);

-- The membership lookup runs on every device list, every session open and every presence
-- socket. user_id is the primary key's leading column so that direction is already
-- covered; this index serves the other direction, "who is on this team".
CREATE INDEX IF NOT EXISTS idx_ugm_group ON user_group_members(group_id);

-- devices.group_id has existed since migration 1 with no index, which was correct while
-- nothing filtered on it. The RBAC subquery above now does, on every list.
CREATE INDEX IF NOT EXISTS idx_devices_group_id ON devices(group_id);

-- Two teams with the same name in one org were harmless when a team was a caption. Now
-- that adding someone to "Finance" grants real access, a second "Finance" is a way to
-- believe you granted access you did not. Fold any pre-existing duplicates into distinct
-- names first — failing the migration on data that was legal when it was written would
-- leave the schema half-applied on exactly the installs that need it most.
UPDATE device_groups g
SET name = g.name || ' (' || LEFT(g.id::text, 4) || ')'
WHERE EXISTS (
    SELECT 1 FROM device_groups o
    WHERE o.org_id = g.org_id
      AND LOWER(o.name) = LOWER(g.name)
      AND (o.created_at, o.id) < (g.created_at, g.id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_device_groups_org_name
    ON device_groups(org_id, LOWER(name));
