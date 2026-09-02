-- 000002_webrtc_protocol.down.sql

DROP TRIGGER IF EXISTS audit_logs_append_only ON audit_logs;
DROP FUNCTION IF EXISTS drs_prevent_mutation();

ALTER TABLE audit_logs
    ADD CONSTRAINT audit_logs_actor_user_id_fkey
    FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE devices ADD COLUMN IF NOT EXISTS enrollment_token VARCHAR(255) UNIQUE;
ALTER TABLE devices ALTER COLUMN agent_secret_hash SET NOT NULL;
ALTER TABLE devices RENAME COLUMN agent_secret_hash TO device_secret_hash;

DROP TABLE IF EXISTS enrollment_tokens;
