# Antifailure Enterprise License

Copyright (c) 2026 Antifailure

## Summary in one sentence

The source in this directory is public so that you can read it, audit it, and
modify it, but running it requires a valid Antifailure enterprise license key
or subscription, and you may not resell it or offer it to third parties as a
hosted service.

Everything outside this `ee` directory is MIT licensed and carries none of the
restrictions below.

## Terms

With regard to the Antifailure software:

This software and associated documentation files (the "Software") may only be
used in production if you (and any entity that you represent) have agreed to,
and are in compliance with, a written agreement between you and Antifailure
(the "Enterprise Terms"), and otherwise have a valid Antifailure Enterprise
subscription or license key for the correct number of seats, clusters, and
features used ("Enterprise License"). Subject to the foregoing sentence, you
are free to modify this Software and publish patches to it.

### Why this does not name a public terms page

It did. This clause used to accept the Antifailure Terms of Service at
https://antifailure.dev/terms "or a substantially similar written agreement",
which named the self serve route first and a negotiated one second.

The page at that address says, of itself, that it is not a paid-service
agreement, and it leaves the contracting entity, the registered address, the
governing law and the liability cap deliberately blank, because none of them
has been decided. So the condition this clause imposed resolved, for anybody
following the route it named first, to a document that states it is not the
kind of document that could satisfy it. A reader could not comply by reading.

Two ways to resolve that: make the page an agreement, or stop naming it. It is
resolved the second way, because the first would mean publishing a contract
with no contracting entity in it, which is not an agreement either, only one
that hides its own gap better.

Nothing about an existing negotiated agreement changes: that route was always
here and was always the one that worked. What is removed is a route that ended
at a page saying it was not one. If those blanks are ever filled and the page
becomes a real agreement, this clause should name it again;
`web/apps/api/test/legal-facts.test.ts` holds the two documents to each other
and will say so.

To agree terms, use the contact form at https://antifailure.dev/contact. It
writes to a queue a person reads.

You agree that Antifailure or its affiliates retain all right, title, and
interest in and to all such modifications and patches, and all such
modifications and patches may only be used, copied, modified, displayed,
distributed, or otherwise exploited with a valid Enterprise License. You agree
that all such modifications and patches, and all copies of the Software, are
subject to the same restrictions as the original Software.

Notwithstanding the foregoing, you may copy and modify the Software for
development and testing purposes, without requiring a subscription. You agree
that Antifailure or its affiliates retain all right, title, and interest in and
to all such modifications.

You are not granted any other rights beyond what is expressly stated herein.
Subject to the foregoing, it is forbidden to copy, merge, publish, distribute,
sublicense, and/or sell the Software.

**You may not:**

- Provide the Software to third parties as a hosted or managed service, where
  the service provides users with access to any substantial set of the
  features or functionality of the Software.
- Remove, obscure, disable, defeat, circumvent, or otherwise interfere with
  the license key verification, entitlement checks, seat counting, or expiry
  handling in this Software, or with any usage metering it performs.
- Alter, remove, or obscure any licensing, copyright, or other notices of
  Antifailure in the Software.

Any use of the Software in violation of this license will automatically
terminate your rights under this license for the current and all other
versions of the Software.

## Warranty and liability

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## Contributions

Contributions to this directory are accepted under the Developer Certificate
of Origin, the same as the rest of the repository, and are licensed under the
terms above rather than under MIT.

## Questions

The routes that resolve are listed at https://antifailure.dev/contact. A licensing
question that is not commercially sensitive belongs in GitHub Discussions, and
anything commercial belongs in the form on that page, which writes a row a
person reads. There is no address to write to: the antifailure.dev domain has no
mail exchanger and an SPF policy of `v=spf1 -all`, which authorizes no sender in
either direction.
