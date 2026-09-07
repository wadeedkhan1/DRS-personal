-- 000004_team_membership.down.sql
--
-- Dropping this table silently narrows every Admin's visibility back to their
-- individually-assigned devices. Any access that was granted only through a team is gone,
-- and the membership rows are not recoverable from anything else.
DROP INDEX IF EXISTS idx_device_groups_org_name;
DROP INDEX IF EXISTS idx_devices_group_id;
DROP INDEX IF EXISTS idx_ugm_group;
DROP TABLE IF EXISTS user_group_members;
