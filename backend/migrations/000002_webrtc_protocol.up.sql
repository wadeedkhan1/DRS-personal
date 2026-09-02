-- 000002_webrtc_protocol.up.sql
--
-- Moves the schema onto the WebRTC signaling protocol and closes three gaps the
-- first migration left: enrollment tokens that never expired, an audit trail that
-- erased its own actor, and an "immutable" log that anything could rewrite.

-- 1. Enrollment tokens get their own table.
--
-- They used to be a column on devices, which meant generating a token immediately
-- created a device row. Every unused token left a permanent "Pending Device" ghost in
-- the dashboard, and nothing ever expired. Here the device row is created when the
-- token is redeemed, and only then.
--
-- The token is stored hashed. It is a credential that grants enrollment, so a database
-- read should not be enough to use one.
CREATE TABLE IF NOT EXISTS enrollment_tokens (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash        TEXT NOT NULL UNIQUE,
    device_type       VARCHAR(50) NOT NULL CHECK (device_type IN ('windows', 'android')),
    assigned_admin_id UUID REFERENCES users(id) ON DELETE SET NULL,
    group_id          UUID REFERENCES device_groups(id) ON DELETE SET NULL,
    created_by        UUID REFERENCES users(id) ON DELETE SET NULL,
    expires_at        TIMESTAMPTZ NOT NULL,
    used_at           TIMESTAMPTZ,
    device_id         UUID REFERENCES devices(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_enrollment_tokens_hash ON enrollment_tokens(token_hash);
CREATE INDEX IF NOT EXISTS idx_enrollment_tokens_org ON enrollment_tokens(org_id);

-- 2. Devices: the secret is renamed to match the protocol's vocabulary, and is now
-- nullable because a device exists from redemption onward and the hash is written in
-- that same transaction.
ALTER TABLE devices RENAME COLUMN device_secret_hash TO agent_secret_hash;
ALTER TABLE devices ALTER COLUMN agent_secret_hash DROP NOT NULL;
ALTER TABLE devices ALTER COLUMN agent_secret_hash SET DEFAULT NULL;

-- The old inline enrollment column is superseded by the table above.
ALTER TABLE devices DROP COLUMN IF EXISTS enrollment_token;

-- 3. Audit trail: keep the actor.
--
-- actor_user_id was ON DELETE SET NULL, so deleting a user quietly erased who did what
-- across the entire history. For an audit log that is the one thing that must not
-- happen, so the foreign key goes and the id remains as a plain value. actor_email is
-- already denormalised alongside it, which is what makes the record still readable
-- after the user is gone.
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_actor_user_id_fkey;

-- 4. Make "immutable" true instead of aspirational.
--
-- NFR-8 and SDS 8 both claim the audit log is append-only and tamper-evident. Nothing
-- enforced it: the application connects as the owner and could UPDATE or DELETE at
-- will. A trigger enforces it at the only layer that cannot be talked out of it.
CREATE OR REPLACE FUNCTION drs_prevent_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_logs is append-only: % is not permitted', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_logs_append_only ON audit_logs;
CREATE TRIGGER audit_logs_append_only
    BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION drs_prevent_mutation();
