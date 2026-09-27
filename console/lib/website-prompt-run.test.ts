import assert from "node:assert/strict";
import test from "node:test";
import { WebsitePromptRunGuard } from "./website-prompt-run.ts";

test("A request finishing after a page switch cannot become that page's proposal", () => {
  const guard = new WebsitePromptRunGuard("/product/twins");
  const twins = guard.begin("/product/twins");
  guard.pageChanged("/docs/reference/mcp");
  assert.equal(guard.isCurrent(twins), false);
  const docs = guard.begin("/docs/reference/mcp");
  assert.equal(guard.isCurrent(docs), true);
  assert.equal(guard.isCurrent(twins), false);
});

test("a later request wins when responses arrive out of order on one page", () => {
  const guard = new WebsitePromptRunGuard("/");
  const first = guard.begin("/");
  const second = guard.begin("/");
  assert.equal(guard.isCurrent(first), false);
  assert.equal(guard.isCurrent(second), true);
  guard.invalidate();
  assert.equal(guard.isCurrent(second), false);
});
