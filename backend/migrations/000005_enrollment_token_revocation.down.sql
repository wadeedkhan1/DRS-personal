-- 000005_enrollment_token_revocation.down.sql
--
-- Dropping revoked_at un-revokes every link that was ever revoked: redemption stops
-- filtering on it, so a token someone deliberately killed becomes usable again. Anything
-- revoked because it leaked should be deleted outright before rolling this back.
DROP INDEX IF EXISTS idx_devices_enrolled_via_token;
ALTER TABLE devices DROP COLUMN IF EXISTS enrolled_via_token;

DROP INDEX IF EXISTS idx_enrollment_tokens_created_by;
ALTER TABLE enrollment_tokens
    DROP COLUMN IF EXISTS label,
    DROP COLUMN IF EXISTS revoked_by,
    DROP COLUMN IF EXISTS revoked_at;
