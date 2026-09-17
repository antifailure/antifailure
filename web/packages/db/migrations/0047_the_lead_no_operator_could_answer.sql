-- The one grant that lets an operator answer a lead from the portal.
--
-- WHY A MIGRATION IS UNAVOIDABLE HERE, and it is the same shape as 0033. 0023
-- gives antifailure_admin SELECT on every table and enumerates its writes one
-- at a time, and 0035 created enterprise_leads AFTER that ALTER DEFAULT
-- PRIVILEGES, so the operator role already reads this table and holds no write
-- on it at all. The operator CLI, af-control-plane-backup leads, marks a lead
-- handled on the migration role or the cluster superuser, neither of which the
-- portal has: adminProcedure reads through antifailure_admin, and that role
-- cannot UPDATE here. So the operator page could list a waiting lead, open it,
-- offer a "mark handled" button, and fail with 42501 at the instant somebody
-- pressed it. This grant is the button made real.
--
-- WHAT IS DELIBERATELY NOT TOUCHED. antifailure_app keeps INSERT and nothing
-- else. 0035 arranged this whole table so that the anonymous public form can
-- only add to it and can never read a row back, because a role that could also
-- SELECT here is one query bug away from publishing every prospect's contact
-- details. That boundary is the point of the table and it stays exactly as
-- 0035 drew it: this migration widens the OPERATOR role, which already holds
-- BYPASSRLS and is a credential the serving process cannot acquire, and it does
-- not go near the serving role.
--
-- UPDATE AND ONLY UPDATE, the same belt and braces 0033 applies to
-- engine_tokens. Marking a lead handled sets handled_at, handled_by and
-- handled_note on a row that already exists; it never creates a lead, and it
-- never removes one. The REVOKE names that intent out loud even though the
-- default privileges never granted these three, so a later reader sees that the
-- operator role's reach on this table is a read and a handled-mark, and nothing
-- that could forge or destroy somebody's request to buy.

BEGIN;

GRANT UPDATE ON enterprise_leads TO antifailure_admin;
REVOKE INSERT, DELETE, TRUNCATE ON enterprise_leads FROM antifailure_admin;

COMMIT;
