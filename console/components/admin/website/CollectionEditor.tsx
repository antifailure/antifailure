"use client";

import { resolveOrder, setFieldOverride, type CollectionDefinition, type FieldValue, type WebsiteDocument } from "@antifailure/website";
import { inputClass } from "@/components/ui";

export function CollectionEditor({ definition, document, onChange, disabled }: {
  definition: CollectionDefinition; document: WebsiteDocument; onChange(document: WebsiteDocument): void; disabled: boolean;
}) {
  const patch = document.collections[definition.key] ?? { hidden: [], moves: [], custom: [] };
  const base = definition.items;
  const ids = resolveOrder(base.map((item) => item.id), patch.moves, patch.custom.map((item) => item.id));
  function changePatch(next: typeof patch) { onChange({ ...document, collections: { ...document.collections, [definition.key]: next } }); }
  function move(id: string, delta: number) {
    const order = [...ids]; const index = order.indexOf(id); const destination = index + delta;
    if (destination < 0 || destination >= order.length) return;
    order.splice(index, 1); order.splice(destination, 0, id);
    changePatch({ ...patch, moves: order.map((item, i) => ({ id: item, after: order[i - 1] ?? null })) });
  }
  function add() {
    const template = base.find((item) => item.fields && Object.keys(item.fields).length)?.fields;
    const fields: Record<string, FieldValue> = template ? structuredClone(template) : { label: "Documentation", href: "/docs" };
    changePatch({ ...patch, custom: [...patch.custom, { id: `custom-${crypto.randomUUID()}`, fields }] });
  }
  return <details className="cms-collection">
    <summary>{definition.label}<span>{ids.length} items</span></summary>
    <div className="cms-collection-body">
      {ids.map((id, index) => {
        const original = base.find((item) => item.id === id);
        const custom = patch.custom.find((item) => item.id === id);
        const fields = original?.fields ?? custom?.fields ?? {};
        const hidden = patch.hidden.includes(id);
        const labelField = ["title", "text", "label", "heading"].find((name) => typeof fields[name] === "string");
        const displayLabel = labelField ? String(document.fields[`${definition.key}.${id}.${labelField}`] ?? fields[labelField]) : original?.label ?? "New item";
        return <details key={id} className="cms-collection-item">
          <summary><span>{displayLabel}</span>{hidden && <small>Hidden</small>}</summary>
          <div className="cms-collection-fields">
            {Object.entries(fields).filter(([, value]) => ["string", "number", "boolean"].includes(typeof value)).map(([name, fallback]) => {
              const key = `${definition.key}.${id}.${name}`;
              const value = document.fields[key] ?? fallback;
              return <label key={name} className="cms-label">{name.replace(/([A-Z])/g, " $1")}
                {typeof fallback === "boolean" ? <input type="checkbox" checked={Boolean(value)} disabled={disabled} onChange={(event) => onChange(setFieldOverride(document, key, event.target.checked, fallback))} /> :
                  <input className={inputClass} value={String(value ?? "")} disabled={disabled} onChange={(event) => onChange(setFieldOverride(document, key, typeof fallback === "number" ? Number(event.target.value) : event.target.value, fallback))} />}
              </label>;
            })}
            <div className="cms-inline-controls">
              <button className="cms-small-button" disabled={disabled || index === 0} aria-label={`Move ${original?.label ?? "item"} up`} onClick={() => move(id, -1)}>↑</button>
              <button className="cms-small-button" disabled={disabled || index === ids.length - 1} aria-label={`Move ${original?.label ?? "item"} down`} onClick={() => move(id, 1)}>↓</button>
              <button className="cms-small-button" disabled={disabled} onClick={() => changePatch({ ...patch, hidden: hidden ? patch.hidden.filter((item) => item !== id) : [...patch.hidden, id] })}>{hidden ? "Show" : "Hide"}</button>
              {custom && <button className="cms-small-button" disabled={disabled} onClick={() => changePatch({ ...patch, custom: patch.custom.filter((item) => item.id !== id), moves: patch.moves.filter((item) => item.id !== id), hidden: patch.hidden.filter((item) => item !== id) })}>Remove</button>}
            </div>
          </div>
        </details>;
      })}
      <button className="cms-add-button" disabled={disabled} onClick={add}>+ Add item</button>
    </div>
  </details>;
}
