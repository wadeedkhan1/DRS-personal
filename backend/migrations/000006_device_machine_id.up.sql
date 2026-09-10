-- 000006_device_machine_id.up.sql
--
-- Identifies a machine by something the machine actually owns, rather than by its name.
--
-- Devices have been de-duplicated on (org_id, name, type) since migration 1, where `name`
-- is whatever hostname the agent reported. That held up while enrollment was a manual,
-- one-at-a-time act. Handing a single self-configuring installer to a fleet breaks it:
-- cloned VMs and imaged corporate PCs routinely share a hostname, and when the second one
-- enrolls it matches the first one's row, rotates the secret, and the first agent is
-- authenticated out of its own identity. It then reconnects forever with a secret the
-- server no longer recognises. Silent, and very hard to read from either end.
--
-- machine_id is a UUID the agent generates once and keeps beside its identity file. Two
-- machines called WIN-PC now coexist as two devices.
ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS machine_id TEXT;

-- Partial unique index, not a plain UNIQUE: every device enrolled before this migration
-- has machine_id NULL, and several NULLs must stay legal. Postgres treats NULLs as
-- distinct in a unique index anyway, but stating WHERE makes the intent explicit and keeps
-- the index off rows that can never match.
CREATE UNIQUE INDEX IF NOT EXISTS idx_devices_org_machine_id
    ON devices(org_id, machine_id)
    WHERE machine_id IS NOT NULL;
