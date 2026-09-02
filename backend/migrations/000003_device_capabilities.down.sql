-- 000003_device_capabilities.down.sql
ALTER TABLE devices DROP COLUMN IF EXISTS allow_terminal;
ALTER TABLE devices DROP COLUMN IF EXISTS allow_screen;
