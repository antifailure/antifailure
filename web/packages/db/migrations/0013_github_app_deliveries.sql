-- What a verified GitHub webhook delivery may write.
--
-- A delivery arrives at an unauthenticated endpoint. What makes it trustworthy
-- is the HMAC over its raw body, checked in the application before the body is
-- parsed. That check cannot happen in Postgres, so the question this migration
-- answers is narrower and is the right one to ask: GIVEN that the application
-- has verified a delivery about one GitHub account, what should the connection
-- be able to reach?
--
-- The answer is that account's rows and nothing else. `antifailure.github_account`
-- carries the login out of the verified payload, and the policies below key on
-- it. So a bug in a handler -- a mixed-up variable, a loop that reuses the wrong
-- id -- writes a row for the account the delivery was about or writes nothing.
-- It cannot reach another tenant.
--
-- This is the same shape as the sign-in policies in 0007, with one honest
-- difference worth stating. There, the caller declares a secret it could only
-- hold by having just been given it. Here it declares a name, and the thing
-- that makes the name believable is a signature check one layer up. So this is
-- defence in depth rather than the primary control, and the primary control is
-- verifySignature in src/github/app.ts. Written down because a reader who
-- assumed otherwise would draw the wrong conclusion about what a leaked
-- database connection could do.
--
-- WHY IT IS NEEDED AT ALL. Sign-in reads github_installations to decide which
-- organizations a person may enter. Nothing wrote that table: the App did not
-- exist, and when it did, every delivery would have been refused by the tenant
-- policies, because a webhook has no tenant until it creates one.

BEGIN;

CREATE OR REPLACE FUNCTION current_github_account() RETURNS text
  LANGUAGE sql STABLE
  AS $$ SELECT lower(nullif(current_setting('antifailure.github_account', true), '')) $$;

-- The organization for an account the delivery names.
--
-- INSERT is what makes an installation able to begin a tenant: somebody
-- installing the App is the first moment a tenant exists, and there is no
-- earlier point at which to have created one. The WITH CHECK ties the row being
-- written to the account in the payload, so this cannot mint an organization
-- for a name the delivery did not carry.
CREATE POLICY github_delivery_writes_org ON organizations
  FOR ALL TO antifailure_app
  USING (lower(github_login) = current_github_account())
  WITH CHECK (lower(github_login) = current_github_account());

CREATE POLICY github_delivery_writes_installation ON github_installations
  FOR ALL TO antifailure_app
  USING (lower(account_login) = current_github_account())
  WITH CHECK (lower(account_login) = current_github_account());

-- Repositories are reached through the installation rather than by name, so a
-- delivery cannot write a repository row against an organization that this
-- account has no installation for. Naming a repository "someone-else/thing"
-- writes it under the account's own organization or not at all.
CREATE POLICY github_delivery_writes_repository ON repositories
  FOR ALL TO antifailure_app
  USING (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()))
  WITH CHECK (org_id IN (
    SELECT org_id FROM github_installations
    WHERE lower(account_login) = current_github_account()));

COMMIT;
