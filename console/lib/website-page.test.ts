import assert from "node:assert/strict";
import test from "node:test";
import { emptyWebsiteDocument, pageBlockPrefix, pageContentKey, validateWebsiteDocument } from "@antifailure/website";
import { createAuthoredPage, removeAuthoredPageDocument } from "./website-page.ts";

test("the New article action creates a private page-shaped draft with a stable path", () => {
  const { document, path } = createAuthoredPage(emptyWebsiteDocument(), ["/blog/existing"], {
    kind: "post", title: "A safer release", description: "What the rehearsal found.", today: "2026-09-27",
  });
  assert.equal(path, "/blog/a-safer-release");
  assert.deepEqual(document.pages, [{ path, kind: "post" }]);
  assert.equal(document.fields[pageContentKey(path, "title")], "A safer release");
  assert.equal(document.fields[pageContentKey(path, "published")], "2026-09-27");
  assert.equal(validateWebsiteDocument(document).ok, true);
  assert.throws(() => createAuthoredPage(document, [path], { kind: "post", title: "Duplicate", description: "No", path, today: "2026-09-27" }), /already belongs/);
});

test("a new route cannot shadow a built site page or escape the article namespace", () => {
  const blank = emptyWebsiteDocument();
  for (const path of ["/about", "/admin/secret", "/blog/post.html", "//other-site", "/docs/../bad"]) {
    assert.throws(() => createAuthoredPage(blank, [], { kind: "page", title: "Page", description: "Intro", path, today: "2026-09-27" }));
  }
  assert.throws(() => createAuthoredPage(blank, ["/product/twins"], { kind: "page", title: "Twins", description: "Intro", path: "/product/twins", today: "2026-09-27" }), /already belongs/);
  assert.throws(() => createAuthoredPage(blank, [], { kind: "post", title: "Outside", description: "Intro", path: "/outside", today: "2026-09-27" }), /under|lowercase/);
});

test("removing an authored page clears its content and blocks without touching another route", () => {
  const first = createAuthoredPage(emptyWebsiteDocument(), [], { kind: "page", title: "First", description: "One", today: "2026-09-27" });
  const second = createAuthoredPage(first.document, [first.path], { kind: "page", title: "Second", description: "Two", today: "2026-09-27" });
  const block = `${pageBlockPrefix(first.path)}7e6d1890-144a-4acf-9a12-579a96ab91ac`;
  second.document.sections.custom.push({ id: block, kind: "text", group: "page", after: `page-first` });
  second.document.fields[`${block}.heading`] = "First block";
  const removed = removeAuthoredPageDocument(second.document, first.path);
  assert.deepEqual(removed.pages, [{ path: second.path, kind: "page" }]);
  assert.equal(removed.fields[pageContentKey(first.path, "title")], undefined);
  assert.equal(removed.fields[`${block}.heading`], undefined);
  assert.equal(removed.sections.custom.length, 0);
  assert.equal(removed.fields[pageContentKey(second.path, "title")], "Second");
});
