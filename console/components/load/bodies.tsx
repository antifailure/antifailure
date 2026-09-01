"use client";

import { useState, type ReactNode } from "react";
import { Empty, Field, inputClass } from "@/components/ui";
import { Fact, Facts, KindMark } from "@/components/load/primitives";
import {
  KIND_FACTS,
  KNOBS,
  count,
  seconds,
  type Body,
  type Kind,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * What a kind is
 * ---------------------------------------------------------------------- */

/**
 * What this workload is and what a result from it is worth.
 *
 * The reproducibility sentence is why this is a block rather than a subtitle.
 * It is the fact a reader needs before a single number underneath: a scenario
 * that replays request for request and a mix that replays only as a shape are
 * not equally strong evidence, and the console is the only place that
 * difference gets said out loud.
 */
export function KindHeader({ kind }: { kind: Kind }) {
  const fact = KIND_FACTS[kind];
  return (
    <div className="border-b border-rule px-4 py-4">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <KindMark kind={kind} />
        <code className="font-mono text-[12.5px] text-muted">{fact.command}</code>
      </div>
      <p className="mt-2 max-w-[74ch] text-[13px] leading-6 text-muted">{fact.what}</p>
      <p className="mt-2 max-w-[74ch] text-[12.5px] leading-6 text-dim">
        <span className="font-medium text-muted">Reproducible:</span> {fact.reproducible}
      </p>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * What a version says
 * ---------------------------------------------------------------------- */

function Selection({ names, noun }: { names: string[]; noun: string }) {
  if (names.length === 0) {
    return (
      <span className="text-muted">
        Nothing named, so every {noun} the manifest declares
      </span>
    );
  }
  return (
    <ul className="flex flex-wrap gap-x-2 gap-y-1">
      {names.map((n) => (
        <li key={n} className="break-all font-mono text-[12.5px] text-ink">
          {n}
        </li>
      ))}
    </ul>
  );
}

/**
 * A version, rendered by its kind.
 *
 * A switch, not a shared form with optional fields. The four branches share no
 * row of markup, which is the point: they are not four skins on one idea, and
 * the moment they share a table they begin to look like they are.
 *
 * A knob the version does not set is shown as the command's own default rather
 * than as a blank or a zero. An absent duration is not a duration of nothing.
 */
export function BodyView({ body }: { body: Body | null }) {
  if (body === null) {
    return (
      <Empty title="This version cannot be read">
        The stored definition is not in the shape this kind of workload uses.
        That is a gap in the record rather than a workload that does nothing.
      </Empty>
    );
  }
  const knobs = KNOBS[body.kind];

  if (body.kind === "observed_load") {
    return (
      <Facts>
        <Fact label="Duration">
          {body.durationSeconds === null ? (
            <span className="text-muted">Not set, so af load run sends for its own default</span>
          ) : (
            seconds(body.durationSeconds)
          )}
        </Fact>
        <Fact label="Scale">
          {body.scale === null ? (
            <span className="text-muted">Not set, so production's own rate</span>
          ) : (
            `${body.scale} times production's rate`
          )}
        </Fact>
      </Facts>
    );
  }

  if (body.kind === "http_scenario") {
    return (
      <Facts>
        <Fact label="Scenarios">
          <Selection names={body.select} noun={knobs.selects} />
        </Fact>
        <Fact label="Seed">
          {body.seed === null ? (
            <span className="text-muted">Not set, so the command's default of 1</span>
          ) : (
            <code className="font-mono text-[12.5px]">{body.seed}</code>
          )}
        </Fact>
        <Fact label="Concurrency">
          {body.concurrency === null ? (
            <span className="text-muted">Not set, so the command's default of 20</span>
          ) : (
            count(body.concurrency)
          )}
        </Fact>
      </Facts>
    );
  }

  if (body.kind === "exploration") {
    return (
      <Facts>
        <Fact label="Goals">
          <Selection names={body.select} noun={knobs.selects} />
        </Fact>
        <Fact label="Seed">
          {body.seed === null ? (
            <span className="text-muted">Not set, so the manifest's own seed</span>
          ) : (
            <code className="break-all font-mono text-[12.5px]">{body.seed}</code>
          )}
        </Fact>
      </Facts>
    );
  }

  return (
    <>
      <Facts>
        <Fact label="Workflows">
          <Selection names={body.select} noun={knobs.selects} />
        </Fact>
      </Facts>
      {body.manifestBlock ? <ManifestBlock block={body.manifestBlock} /> : null}
      {body.dropped.length > 0 ? <Dropped dropped={body.dropped} /> : null}
    </>
  );
}

/**
 * The manifest block a promoted workflow carries.
 *
 * Shown on the version rather than only at the moment of promotion, because
 * the version is where somebody looks a week later when the run comes back
 * unverified and they need to know what was never pasted in.
 */
export function ManifestBlock({ block, heading }: { block: string; heading?: string }) {
  return (
    <div className="border-t border-rule px-4 py-4">
      <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
        {heading ?? "For antifailure.yaml"}
      </p>
      <p className="mt-1 max-w-[74ch] text-[12.5px] leading-6 text-muted">
        This block has to be in the repository before anything can run this
        version. The control plane cannot put a file in your repository, and{" "}
        <code className="font-mono">af test --only</code> selects out of the
        manifest, so until this is committed the selection above names a
        workflow that does not exist.
      </p>
      {/* overflow-x-auto by hand rather than the shared .scroll-x helper, which
          turns itself off below 640px because the tables it was written for
          stack at that width. A YAML block does not stack, and borrowing the
          class clipped it dead at the card edge on a phone. */}
      <pre className="mt-3 overflow-x-auto rounded-md border border-rule bg-[rgba(16,16,16,0.03)] px-3 py-2.5 font-mono text-[12.5px] leading-6 text-ink">
        <code>{block}</code>
      </pre>
    </div>
  );
}

/**
 * What a compilation deliberately did not carry.
 *
 * Never summarised into a count. Each line is a sentence a person has to read
 * before deciding whether to keep the promotion, and "3 things were dropped"
 * is the shape of a notice nobody opens.
 */
export function Dropped({ dropped }: { dropped: string[] }) {
  return (
    <div className="border-t border-rule px-4 py-4">
      <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
        What the compilation did not carry
      </p>
      <ul className="mt-2 space-y-2">
        {dropped.map((d) => (
          <li key={d} className="max-w-[74ch] text-[12.5px] leading-6 text-muted">
            {d}
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * What a kind cannot set, and the command that is the reason.
 *
 * Said rather than hidden. A reader who finds no concurrency box and no
 * explanation assumes the console forgot; one who is told `af load run` has no
 * such flag has learned something about the product. The alternative, offering
 * every knob on every kind, is a control that exists to be refused.
 */
export function KnobNotes({ kind }: { kind: Kind }) {
  const knobs = KNOBS[kind];
  if (knobs.refused.length === 0) return null;
  return (
    <div className="mt-4 border-t border-rule pt-3">
      <p className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">
        Not settable on this kind
      </p>
      <ul className="mt-2 space-y-1.5">
        {knobs.refused.map((r) => (
          <li key={r.knob} className="max-w-[74ch] text-[12.5px] leading-6 text-muted">
            <span className="font-medium text-ink">{r.knob}.</span> {r.because}
          </li>
        ))}
      </ul>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Writing one
 * ---------------------------------------------------------------------- */

interface Draft {
  duration: string;
  scale: string;
  seed: string;
  concurrency: string;
  select: string;
}

const EMPTY: Draft = { duration: "", scale: "", seed: "", concurrency: "", select: "" };

function draftOf(body: Body | null): Draft {
  if (body === null) return EMPTY;
  switch (body.kind) {
    case "observed_load":
      return {
        ...EMPTY,
        duration: body.durationSeconds === null ? "" : String(body.durationSeconds),
        scale: body.scale === null ? "" : String(body.scale),
      };
    case "http_scenario":
      return {
        ...EMPTY,
        select: body.select.join(", "),
        seed: body.seed === null ? "" : String(body.seed),
        concurrency: body.concurrency === null ? "" : String(body.concurrency),
      };
    case "browser_workflow":
      return { ...EMPTY, select: body.select.join(", ") };
    case "exploration":
      return { ...EMPTY, select: body.select.join(", "), seed: body.seed ?? "" };
  }
}

function names(v: string): string[] {
  return v
    .split(",")
    .map((n) => n.trim())
    .filter(Boolean);
}

/**
 * A number from a field, or a sentence saying why it is not one.
 *
 * `Number.isFinite` rejects NaN, which matters more than it looks: the engine
 * found a real defect where NaN parsed as a float and every comparison against
 * it was false, so a range check let it through and the send rate became NaN
 * requests per second. Nothing here can send one.
 *
 * The bounds are the control plane's own, so a value it would refuse is
 * refused here first, next to the field, rather than coming back as a sentence
 * about the whole form after a round trip.
 */
function bounded(
  raw: string,
  what: string,
  low: number,
  high: number,
  whole: boolean,
): { value: number | null } | { error: string } {
  if (raw.trim() === "") return { value: null };
  const n = Number(raw);
  if (!Number.isFinite(n)) return { error: `${what} has to be a number.` };
  if (whole && !Number.isInteger(n)) return { error: `${what} has to be a whole number.` };
  if (n < low || n > high) return { error: `${what} has to be between ${low} and ${high}.` };
  return { value: n };
}

export interface BodyDraft {
  draft: Draft;
  set: (patch: Partial<Draft>) => void;
  /** The body to send, or null when a field is wrong. */
  body: Body | null;
  /** Keyed by field so the message sits under the control that is wrong. */
  errors: Partial<Record<keyof Draft, string>>;
  /** True when the draft is exactly what it started as, so a save would add a
   *  version identical to the head. The control plane answers that with "that
   *  is what the latest version already says" rather than a new version, and
   *  saying so before the round trip is friendlier than after it. */
  unchanged: boolean;
}

/**
 * The form state behind a version body, per kind.
 *
 * `manifestBlock` and `dropped` are carried through from the version being
 * edited rather than re-entered or dropped. They are written only by a
 * promotion, and a form that omitted them would silently delete the block a
 * person has to paste into their repository, in a save that looks like it only
 * changed a selection.
 */
export function useBodyDraft(kind: Kind, initial: Body | null): BodyDraft {
  const [draft, setDraft] = useState<Draft>(() => draftOf(initial));
  const start = draftOf(initial);
  const carried =
    initial !== null && initial.kind === "browser_workflow"
      ? { manifestBlock: initial.manifestBlock, dropped: initial.dropped }
      : { manifestBlock: null, dropped: [] as string[] };

  const errors: Partial<Record<keyof Draft, string>> = {};
  let body: Body | null = null;
  const selected = names(draft.select);
  const knobs = KNOBS[kind];

  if (knobs.select === "required" && selected.length === 0) {
    errors.select = `Name at least one ${knobs.selects}.`;
  }
  if (selected.length > 50) {
    errors.select = `Fifty ${knobs.selects}s is the most one version may name.`;
  }

  if (kind === "observed_load") {
    const d = bounded(draft.duration, "The duration", 1, 3600, true);
    const s = bounded(draft.scale, "The scale", 0.01, 100, false);
    if ("error" in d) errors.duration = d.error;
    if ("error" in s) errors.scale = s.error;
    if (!("error" in d) && !("error" in s)) {
      body = { kind, durationSeconds: d.value, scale: s.value };
    }
  } else if (kind === "http_scenario") {
    const seed = bounded(draft.seed, "The seed", 0, 2_147_483_647, true);
    const c = bounded(draft.concurrency, "The concurrency", 1, 500, true);
    if ("error" in seed) errors.seed = seed.error;
    if ("error" in c) errors.concurrency = c.error;
    if (!("error" in seed) && !("error" in c) && errors.select === undefined) {
      body = { kind, select: selected, seed: seed.value, concurrency: c.value };
    }
  } else if (kind === "browser_workflow") {
    if (errors.select === undefined) {
      body = { kind, select: selected, manifestBlock: carried.manifestBlock, dropped: carried.dropped };
    }
  } else {
    if (errors.select === undefined) {
      const seed = draft.seed.trim();
      if (seed.length > 200) errors.seed = "The seed has to be 200 characters or fewer.";
      else body = { kind, select: selected, seed: seed === "" ? null : seed };
    }
  }

  return {
    draft,
    set: (patch) => setDraft((d) => ({ ...d, ...patch })),
    body,
    errors,
    unchanged:
      draft.duration === start.duration &&
      draft.scale === start.scale &&
      draft.seed === start.seed &&
      draft.concurrency === start.concurrency &&
      names(draft.select).join(",") === names(start.select).join(","),
  };
}

/**
 * The fields a kind actually has.
 *
 * Rendered from the knob table rather than by hand per kind, so a kind cannot
 * grow a control the command has no flag for by somebody copying a block.
 */
export function BodyFields({ kind, draft }: { kind: Kind; draft: BodyDraft }): ReactNode {
  const knobs = KNOBS[kind];
  return (
    <>
      {knobs.select !== "no" ? (
        <Field
          label={`${knobs.selects.charAt(0).toUpperCase()}${knobs.selects.slice(1)}s`}
          hint={
            knobs.select === "required"
              ? `Comma separated names out of the manifest. Required: running everything it declares would change what this workload does the day somebody adds one.`
              : (knobs.emptyMeans ?? undefined)
          }
          error={draft.errors.select ?? null}
        >
          <input
            className={inputClass}
            value={draft.draft.select}
            onChange={(e) => draft.set({ select: e.target.value })}
            placeholder={knobs.selects === "goal" ? "upgrade-a-plan" : "checkout"}
          />
        </Field>
      ) : null}

      {knobs.duration ? (
        <Field label="Duration" hint="Seconds." error={draft.errors.duration ?? null}>
          <input
            className={inputClass}
            inputMode="numeric"
            value={draft.draft.duration}
            onChange={(e) => draft.set({ duration: e.target.value })}
            placeholder="60"
          />
        </Field>
      ) : null}

      {knobs.scale ? (
        <Field
          label="Scale"
          hint="A multiplier on production's rate."
          error={draft.errors.scale ?? null}
        >
          <input
            className={inputClass}
            inputMode="decimal"
            value={draft.draft.scale}
            onChange={(e) => draft.set({ scale: e.target.value })}
            placeholder="1"
          />
        </Field>
      ) : null}

      {knobs.concurrency ? (
        <Field
          label="Concurrency"
          hint="Ceiling on requests in flight."
          error={draft.errors.concurrency ?? null}
        >
          <input
            className={inputClass}
            inputMode="numeric"
            value={draft.draft.concurrency}
            onChange={(e) => draft.set({ concurrency: e.target.value })}
            placeholder="20"
          />
        </Field>
      ) : null}

      {knobs.seed !== "no" ? (
        <Field
          label="Seed"
          hint={
            knobs.seed === "number"
              ? "A whole number. The same seed plans the same schedule."
              : "Any string. The same seed walks the same way."
          }
          error={draft.errors.seed ?? null}
        >
          <input
            className={inputClass}
            inputMode={knobs.seed === "number" ? "numeric" : "text"}
            value={draft.draft.seed}
            onChange={(e) => draft.set({ seed: e.target.value })}
            placeholder={knobs.seed === "number" ? "1" : "a-quiet-tuesday"}
          />
        </Field>
      ) : null}
    </>
  );
}
