-- 000003_device_capabilities.up.sql
--
-- Per-device consent capabilities. When a device enrolls through the agent GUI, the
-- person installing it chooses what that machine exposes: its screen, a remote terminal,
-- or both. Those choices are stored here and enforced by the backend on every viewer
-- session, so a device that never consented to terminal access cannot have a command run
-- on it even by a super admin.
--
-- Defaults are deliberately asymmetric. allow_screen defaults TRUE so existing devices
-- and the plain CLI enroll path keep working exactly as before (screen monitoring was the
-- original purpose). allow_terminal defaults FALSE so the more powerful capability is
-- opt-in: a device only accepts remote commands when someone explicitly turned it on.
ALTER TABLE devices ADD COLUMN IF NOT EXISTS allow_screen   BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE devices ADD COLUMN IF NOT EXISTS allow_terminal BOOLEAN NOT NULL DEFAULT FALSE;
