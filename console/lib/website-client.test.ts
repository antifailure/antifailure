import { describe, it } from "node:test";
import assert from "node:assert/strict";
import { emptyWebsiteDocument, validateWebsiteDocument, type WebsiteManifest } from "@antifailure/website";
import { addSection, duplicateSection, moveSection, pageSections, removeCustomSection, resetSection } from "./website-client.ts";

const first = "custom-11111111-1111-4111-8111-111111111111";
const second = "custom-22222222-2222-4222-8222-222222222222";
const manifest: WebsiteManifest = {
  schemaVersion: 1, sourceVersion: "test", fonts: [], builtinAssets: [], collections: [],
  sections: ["migration", "twins", "firewall"].map((id) => ({ id, label: id, group: "page" })),
  fields: [{ key: "migration.heading", label: "Heading", sectionId: "migration", kind: "text", defaultValue: "Current source heading" }],
};

describe("website editor operations", () => {
  it("reorders source sections in both directions and retains future defaults", () => {
    const empty = emptyWebsiteDocument();
    const moved = moveSection(empty, "page", ["migration", "twins", "firewall"], "firewall", 0);
    assert.deepEqual(pageSections(moved, manifest, "page").map((item) => item.id), ["firewall", "migration", "twins"]);
    const returned = moveSection(moved, "page", ["firewall", "migration", "twins"], "firewall", 2);
    assert.deepEqual(pageSections(returned, manifest, "page").map((item) => item.id), ["migration", "twins", "firewall"]);
    const nextSource = { ...manifest, sections: [...manifest.sections, { id: "new-source", label: "New feature", group: "page" as const }] };
    assert.equal(pageSections(moved, nextSource, "page").filter((item) => item.id === "new-source").length, 1);
    assert.deepEqual(empty.sections.moves, []);
  });

  it("creates valid publishable presets and places them after the selection", () => {
    for (const kind of ["text", "image", "video", "split", "features", "cta", "shape", "divider", "embed", "spacer"] as const) {
      const doc = addSection(emptyWebsiteDocument(), kind, "page", "twins", first);
      assert.equal(validateWebsiteDocument(doc).ok, true, kind);
      assert.deepEqual(pageSections(doc, manifest, "page").map((item) => item.id), ["migration", "twins", first, "firewall"]);
    }
  });

  it("duplicates only custom content and design without changing its source", () => {
    const doc = addSection(emptyWebsiteDocument(), "split", "page", "twins", first);
    doc.styles[first] = { desktop: { paddingTop: 40 } };
    doc.styles[`${first}.heading`] = { mobile: { fontSize: 30 } };
    doc.collections[`${first}.items`] = { hidden: ["feature-one"], moves: [], custom: [{ id: "new-feature", fields: { title: "A custom feature" } }] };
    const copy = duplicateSection(doc, first, second);
    assert.equal(validateWebsiteDocument(copy).ok, true);
    assert.deepEqual(copy.fields[`${second}.body`], doc.fields[`${first}.body`]);
    assert.notEqual(copy.fields[`${second}.body`], doc.fields[`${first}.body`]);
    assert.deepEqual(copy.styles[`${second}.heading`], { mobile: { fontSize: 30 } });
    assert.deepEqual(copy.collections[`${second}.items`], doc.collections[`${first}.items`]);
    assert.notEqual(copy.collections[`${second}.items`], doc.collections[`${first}.items`]);
    assert.deepEqual(pageSections(copy, manifest, "page").map((item) => item.id), ["migration", "twins", first, second, "firewall"]);
    assert.equal(doc.sections.custom.length, 1);
  });

  it("custom rich-text headings have a readable label", () => {
    const doc = addSection(emptyWebsiteDocument(), "text", "page", null, first);
    doc.fields[`${first}.heading`] = { type: "doc", content: [{ type: "paragraph", content: [{ type: "text", text: "A real headline" }] }] };
    assert.equal(pageSections(doc, manifest, "page")[0]?.label, "A real headline");
  });

  it("removes custom fields, styles, collection content and dangling moves together", () => {
    const doc = duplicateSection(addSection(emptyWebsiteDocument(), "split", "page", "twins", first), first, second);
    doc.styles[first] = { desktop: { gap: 30 } };
    doc.sections.moves.push({ id: first, after: "twins", group: "page" });
    doc.sections.hidden.push(first);
    doc.collections[`${first}.features`] = { hidden: [], moves: [], custom: [] };
    const removed = removeCustomSection(doc, first);
    assert.equal(validateWebsiteDocument(removed).ok, true);
    assert.equal(removed.sections.custom.some((item) => item.id === first), false);
    assert.equal(removed.sections.custom[0]?.after, null);
    assert.equal(Object.keys(removed.fields).some((key) => key.startsWith(first)), false);
    assert.equal(Object.keys(removed.styles).some((key) => key.startsWith(first)), false);
    assert.equal(Object.keys(removed.collections).some((key) => key.startsWith(first)), false);
  });

  it("reset follows current defaults and leaves unrelated edits intact", () => {
    const doc = emptyWebsiteDocument();
    doc.fields["migration.heading"] = "My heading"; doc.fields["twins.heading"] = "Keep this";
    doc.styles.migration = { desktop: { gap: 20 } }; doc.styles["migration.heading"] = { mobile: { fontSize: 30 } };
    doc.sections.hidden.push("migration");
    const reset = resetSection(doc, "migration", manifest);
    assert.equal(Object.hasOwn(reset.fields, "migration.heading"), false);
    assert.equal(Object.hasOwn(reset.styles, "migration.heading"), false);
    assert.equal(reset.fields["twins.heading"], "Keep this");
    assert.equal(reset.sections.hidden.includes("migration"), false);
  });
});
