-- 000005_enrollment_token_revocation.up.sql
--
-- Gives an invite link an off switch.
--
-- Until now enrollment_tokens was write-once, read-forever: nothing listed tokens, nothing
-- deleted them, `used_at` was never written, and `expires_at` was set 100 years out and
-- never checked. That was tolerable while a token was inert text somebody had to type into
-- an agent by hand.
--
-- It stops being tolerable now that the download endpoint bakes the token into the agent
-- binary. The installer becomes a self-enrolling payload: whoever runs it joins the org
-- and registers autostart, with nothing typed. A file like that WILL get forwarded, and
-- without revocation the only remedy is editing the database by hand.
--
-- Revocation stops NEW enrollments. It deliberately does not touch devices the token has
-- already enrolled — those hold their own agent secrets and are revoked by deleting the
-- device, which is a different decision an admin should make deliberately.
ALTER TABLE enrollment_tokens
    ADD COLUMN IF NOT EXISTS revoked_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS revoked_by UUID REFERENCES users(id) ON DELETE SET NULL,
    -- A human label, so a list of links is something an admin can reason about. Without it
    -- the only distinguishing features are a UUID and a timestamp, and the whole point of
    -- the list is deciding which link to kill.
    ADD COLUMN IF NOT EXISTS label TEXT;

-- Listing links is scoped by creator for an Admin (mirroring ListGroups), and every
-- redemption now filters on revoked_at. Both directions want this.
CREATE INDEX IF NOT EXISTS idx_enrollment_tokens_created_by
    ON enrollment_tokens(created_by);

-- How many devices a link has enrolled is the number that tells an admin whether revoking
-- it is safe, so devices remember which token created them. ON DELETE SET NULL because a
-- revoked-and-deleted token must not cascade into deleting real machines.
ALTER TABLE devices
    ADD COLUMN IF NOT EXISTS enrolled_via_token UUID
        REFERENCES enrollment_tokens(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_devices_enrolled_via_token
    ON devices(enrolled_via_token);
