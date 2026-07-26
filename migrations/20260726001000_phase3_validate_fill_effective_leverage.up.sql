-- Validate the additive effective-leverage constraint after the v4 cutover.
--
-- PostgreSQL enforces a NOT VALID CHECK constraint for every new or changed
-- row. Historical rows received NULL from the metadata-only column add and are
-- therefore already known to satisfy this constraint. Keeping validation in a
-- separate transaction avoids retaining the cutover's ACCESS EXCLUSIVE lock
-- while PostgreSQL scans historical fill pages.
--
-- VALIDATE CONSTRAINT uses SHARE UPDATE EXCLUSIVE, which permits ordinary
-- SELECT and INSERT/UPDATE/DELETE traffic. Both lock acquisition and the scan
-- are bounded; failure leaves the enforced-but-unvalidated constraint in place
-- for a clean retry.

SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE trading.fills
    VALIDATE CONSTRAINT fills_effective_leverage_positive;
