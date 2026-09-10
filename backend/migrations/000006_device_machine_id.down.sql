-- 000006_device_machine_id.down.sql
--
-- After this, de-duplication falls back to (org_id, name, type). Any two enrolled machines
-- that share a hostname will collide the next time either one re-enrolls, and the loser
-- is authenticated out of its identity.
DROP INDEX IF EXISTS idx_devices_org_machine_id;
ALTER TABLE devices DROP COLUMN IF EXISTS machine_id;
