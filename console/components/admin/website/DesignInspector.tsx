"use client";

import { useEffect, useId, useState, type ReactNode } from "react";
import {
  resetStyleOverride,
  setStyleOverride,
  STYLE_NUMBER_BOUNDS,
  type ResponsiveStyle,
  type StyleValues,
  type WebsiteDocument,
  type WebsiteManifest,
} from "@antifailure/website";

export interface DesignInspectorProps {
  document: WebsiteDocument;
  target: string;
  device: keyof ResponsiveStyle;
  manifest: WebsiteManifest;
  onChange: (document: WebsiteDocument) => void;
  disabled?: boolean;
  fontAssets?: Array<{ id: string; name: string }>;
}

const controlClass = "h-11 min-w-0 w-full rounded-md border border-rule bg-card px-3 text-base text-ink outline-none focus-visible:border-ink focus-visible:ring-1 focus-visible:ring-ink disabled:cursor-not-allowed disabled:opacity-60 sm:text-[13px]";
const quietButtonClass = "inline-flex min-h-11 items-center justify-center rounded-md px-2 text-[12px] font-medium text-muted hover:bg-paper hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink disabled:cursor-not-allowed disabled:opacity-40";

function Reset({ label, active, disabled, onReset }: { label: string; active: boolean; disabled: boolean; onReset: () => void }) {
  return (
    <button type="button" className={quietButtonClass} disabled={disabled || !active} onClick={onReset} aria-label={`Reset ${label.toLowerCase()} to the site default`} title="Use site default">
      <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M3 6.5A5 5 0 1 1 3.5 11M3 2.5v4h4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg>
    </button>
  );
}

function Control({ label, active, disabled, onReset, children }: { label: string; active: boolean; disabled: boolean; onReset: () => void; children: (id: string) => ReactNode }) {
  const id = useId();
  return (
    <div className="min-w-0">
      <div className="flex min-h-8 items-center justify-between gap-1">
        <label htmlFor={id} className="text-[12px] font-medium text-muted">{label}</label>
        <Reset label={label} active={active} disabled={disabled} onReset={onReset} />
      </div>
      {children(id)}
    </div>
  );
}

function NumericControl({ label, property, value, disabled, onChange, step = 1, unit = "px" }: {
  label: string; property: keyof StyleValues; value: number | undefined; disabled: boolean;
  onChange: (value: number | undefined) => void; step?: number; unit?: string;
}) {
  const [draft, setDraft] = useState(value === undefined ? "" : String(value));
  const [error, setError] = useState("");
  const errorId = useId();
  const [min, max] = STYLE_NUMBER_BOUNDS[property];
  useEffect(() => { setDraft(value === undefined ? "" : String(value)); setError(""); }, [value]);
  const update = (raw: string) => {
    setDraft(raw);
    setError("");
    if (raw === "") { onChange(undefined); return; }
    const parsed = Number(raw);
    if (Number.isFinite(parsed) && parsed >= min && parsed <= max && (property !== "zIndex" || Number.isSafeInteger(parsed))) onChange(parsed);
  };
  const validate = () => {
    if (draft === "") return;
    const parsed = Number(draft);
    if (!Number.isFinite(parsed) || parsed < min || parsed > max || (property === "zIndex" && !Number.isSafeInteger(parsed))) setError(`Use ${property === "zIndex" ? "a whole number from " : ""}${min} to ${max}${unit ? ` ${unit}` : ""}.`);
  };
  return (
    <Control label={label} active={value !== undefined} disabled={disabled} onReset={() => { setDraft(""); setError(""); onChange(undefined); }}>
      {(id) => <>
        <div className="relative">
          <input id={id} type="number" inputMode="decimal" step={step} min={min} max={max} value={draft} placeholder="Auto" onChange={(event) => update(event.target.value)} onBlur={validate} disabled={disabled} className={`${controlClass} ${unit ? "pr-9" : ""}`} aria-invalid={!!error} aria-describedby={error ? errorId : undefined} />
          {unit ? <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-[11px] text-dim" aria-hidden="true">{unit}</span> : null}
        </div>
        {error ? <p id={errorId} role="alert" className="mt-1 text-[12px] text-fail">{error}</p> : null}
      </>}
    </Control>
  );
}

function ColorControl({ label, value, disabled, onChange }: { label: string; value: string | undefined; disabled: boolean; onChange: (value: string | undefined) => void }) {
  const [draft, setDraft] = useState(value ?? "");
  const [error, setError] = useState("");
  const errorId = useId();
  useEffect(() => { setDraft(value ?? ""); setError(""); }, [value]);
  const valid = (color: string) => /^#(?:[\da-f]{3}|[\da-f]{4}|[\da-f]{6}|[\da-f]{8})$/iu.test(color);
  let swatch = value ?? "#101010";
  if (swatch.length === 4 || swatch.length === 5) swatch = `#${swatch.slice(1, 4).split("").map((part) => part + part).join("")}`;
  else swatch = swatch.slice(0, 7);
  return (
    <Control label={label} active={value !== undefined} disabled={disabled} onReset={() => { setDraft(""); setError(""); onChange(undefined); }}>
      {(id) => <>
        <div className="flex items-center gap-2">
          <input type="color" value={swatch} aria-label={`Choose ${label.toLowerCase()}`} disabled={disabled} onChange={(event) => onChange(event.target.value)} className="h-11 w-11 shrink-0 cursor-pointer rounded-md border border-rule bg-card p-1 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink disabled:cursor-not-allowed disabled:opacity-60" />
          <input id={id} value={draft} spellCheck={false} placeholder="Site default" disabled={disabled} className={`${controlClass} font-mono`} onChange={(event) => { const next = event.target.value; setDraft(next); setError(""); if (!next || valid(next)) onChange(next || undefined); }} onBlur={() => setError(draft && !valid(draft) ? "Enter a hex color, such as #101010." : "")} aria-invalid={!!error} aria-describedby={error ? errorId : undefined} />
        </div>
        {error ? <p id={errorId} role="alert" className="mt-1 text-[12px] text-fail">{error}</p> : null}
      </>}
    </Control>
  );
}

function Group({ title, open, count, children }: { title: string; open?: boolean; count: number; children: ReactNode }) {
  return (
    <details open={open} className="group border-t border-rule">
      <summary className="flex min-h-12 cursor-pointer list-none items-center justify-between gap-3 py-3 text-[13px] font-semibold text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ink [&::-webkit-details-marker]:hidden">
        <span>{title}</span>
        <span className="flex items-center gap-2">{count ? <span className="text-[11px] font-normal text-muted">{count} edited</span> : null}<svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true" className="group-open:rotate-180"><path d="m4 6 4 4 4-4" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" /></svg></span>
      </summary>
      <div className="space-y-2 pb-5">{children}</div>
    </details>
  );
}

/** Device overrides deliberately do not inherit from the other two devices. */
export function DesignInspector(props: DesignInspectorProps) {
  return <DesignControls key={`${props.target}:${props.device}`} {...props} />;
}

function DesignControls({ document, target, device, manifest, onChange, disabled = false, fontAssets = [] }: DesignInspectorProps) {
  const styles = document.styles[target]?.[device] ?? {};
  const update = <K extends keyof StyleValues>(property: K, value: StyleValues[K]) => onChange(setStyleOverride(document, target, device, property, value));
  const count = (...keys: Array<keyof StyleValues>) => keys.filter((key) => styles[key] !== undefined).length;
  const number = (property: keyof StyleValues, label: string, step = 1, unit = "px") => (
    <NumericControl label={label} property={property} value={typeof styles[property] === "number" ? styles[property] as number : undefined} disabled={disabled} onChange={(value) => update(property, value)} step={step} unit={unit} />
  );
  const select = (property: keyof StyleValues, label: string, options: Array<{ value: string; label: string }>) => (
    <Control label={label} active={styles[property] !== undefined} disabled={disabled} onReset={() => update(property, undefined)}>
      {(id) => <select id={id} value={styles[property] ?? ""} disabled={disabled} onChange={(event) => update(property, event.target.value || undefined)} className={controlClass}><option value="">Site default</option>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select>}
    </Control>
  );
  const deviceName = device[0].toUpperCase() + device.slice(1);
  return (
    <div className="min-w-0">
      <div className="mb-4 flex items-start justify-between gap-3">
        <p className="max-w-[25ch] text-[12px] leading-5 text-muted">Editing {device} styles. Blank values follow the site design.</p>
        <button type="button" className={quietButtonClass} disabled={disabled || !Object.keys(styles).length} onClick={() => onChange(resetStyleOverride(document, target, device))} aria-label={`Reset all ${device} styles for this selection`}>Reset all</button>
      </div>
      <span className="sr-only">{deviceName} design settings</span>
      <Group title="Typography" open count={count("fontFamily", "fontSize", "fontWeight", "lineHeight", "letterSpacing", "textAlign")}>
        {select("fontFamily", "Font", [...manifest.fonts.map((font) => ({ value: font.key, label: font.label })), ...fontAssets.map((font) => ({ value: `asset:${font.id}`, label: `${font.name} · uploaded` }))])}
        <div className="grid grid-cols-2 gap-x-3">{number("fontSize", "Size")}{number("fontWeight", "Weight", 50, "")}{number("lineHeight", "Line height", 0.05, "")}{number("letterSpacing", "Letter spacing", 0.1)}</div>
        {select("textAlign", "Alignment", ["left", "center", "right", "justify"].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1) })))}
      </Group>
      <Group title="Color" open count={count("color", "backgroundColor")}>
        <ColorControl label="Text color" value={styles.color} disabled={disabled} onChange={(value) => update("color", value)} />
        <ColorControl label="Background" value={styles.backgroundColor} disabled={disabled} onChange={(value) => update("backgroundColor", value)} />
      </Group>
      <Group title="Spacing" count={count("paddingTop", "paddingRight", "paddingBottom", "paddingLeft", "marginTop", "marginBottom", "gap")}>
        <p className="text-[12px] leading-5 text-muted">Padding adds room inside. Margins add room above and below.</p>
        <div className="grid grid-cols-2 gap-x-3">{number("paddingTop", "Top padding")}{number("paddingBottom", "Bottom padding")}{number("paddingLeft", "Left padding")}{number("paddingRight", "Right padding")}{number("marginTop", "Top margin")}{number("marginBottom", "Bottom margin")}{number("gap", "Item gap")}</div>
      </Group>
      <Group title="Layout & size" count={count("layout", "position", "zIndex", "maxWidth", "width", "minHeight", "borderRadius", "opacity")}>
        {select("layout", "Arrangement", [{ value: "default", label: "Original arrangement" }, { value: "stack", label: "Stack vertically" }, { value: "media-left", label: "Media on the left" }, { value: "media-right", label: "Media on the right" }, { value: "center", label: "Centered" }])}
        {select("position", "Position", [{ value: "relative", label: "In page" }, { value: "absolute", label: "Overlay" }, { value: "fixed", label: "Fixed on screen" }])}
        <div className="grid grid-cols-2 gap-x-3">{number("zIndex", "Layer", 1, "")}{number("maxWidth", "Maximum width")}{number("width", "Width")}{number("minHeight", "Minimum height")}{number("borderRadius", "Corner radius")}{number("opacity", "Opacity", 0.05, "")}</div>
      </Group>
      <Group title="Image framing" count={count("imageFit", "focalX", "focalY")}>
        {select("imageFit", "Fit", [{ value: "cover", label: "Fill the frame" }, { value: "contain", label: "Show the whole image" }])}
        <div className="grid grid-cols-2 gap-x-3">{number("focalX", "Focal point X", 1, "%")}{number("focalY", "Focal point Y", 1, "%")}</div>
      </Group>
      <Group title="Position" count={count("x", "y")}>
        <p className="text-[12px] leading-5 text-muted">Fine-tune placement within the existing layout.</p>
        <div className="grid grid-cols-2 gap-x-3">{number("x", "Horizontal offset")}{number("y", "Vertical offset")}</div>
      </Group>
    </div>
  );
}
