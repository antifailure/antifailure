"use client";

import { useState } from "react";
import {
  Badge,
  Button,
  Card,
  Confirm,
  Empty,
  Field,
  Loaded,
  TableSkeleton,
  When,
  inputClass,
  selectClass,
} from "@/components/ui";
import {
  AdminPage,
  DataTable,
  Drawer,
  EmptyList,
  Facts,
  StatusChip,
  type Column,
  type Fact,
} from "@/components/admin/primitives";
import { operatorMay, useAdminContext, useTenants } from "@/lib/admin";
import type { ApiError } from "@/lib/api";
import {
  killFlag,
  revokeOverride,
  setFlag,
  targetFlag,
  useFlags,
  useOrgEntitlements,
  type FeatureFlag,
  type FlagTarget,
  type OrgEntitlement,
} from "@/lib/admin-product";

/**
 * Feature flags, the targets on them, and the entitlement overrides beside
 * them. There are no experiments, and this page says so rather than drawing
 * one.
 *
 * THE THING THIS PAGE REFUSES TO BE. The section is called Experiments and
 * Feature Flags and only the second half exists. There is no experiment table
 * in this schema, no variant, no assignment, no exposure log and no results. A
 * rollout percent is a share of traffic, not an experiment: nothing records
 * which subject fell on which side, so nothing can compare the two afterwards.
 * A dashboard over that would have to invent every number on it, which is worse
 * than an empty page because an operator would read the invention as a
 * measurement. So the absence is stated at the top, in one card, with the four
 * things that would have to exist for the other half of the title to be true.
 *
 * WHAT IT IS INSTEAD is the whole of the flag surface: state, rollout, targets,
 * the internal-only bit, and the kill with its reason on it. The kill is the
 * reason this page matters at three in the morning, so it is one button on the
 * flag rather than a state hidden in a dropdown, and it demands a reason
 * because a kill switch whose reason nobody can see is not a kill switch.
 *
 * NOTHING HERE ANIMATES, including the chip on a flag that is live. A dot that
 * throbs while the reader is doing nothing says exactly as much as a still one.
 */
export default function ProductExperimentsPage() {
  const { me } = useAdminContext();
  const mayWriteFlags = operatorMay(me, "admin.flags.write");
  const mayReadEntitlements = operatorMay(me, "admin.entitlements.read");

  const [creating, setCreating] = useState(false);
  const state = useFlags();

  return (
    <AdminPage
      href="/admin/product/experiments"
      actions={
        mayWriteFlags ? (
          <Button variant="primary" onClick={() => setCreating(true)}>
            Add a flag
          </Button>
        ) : undefined
      }
    >
      <div className="space-y-6">
        <NoExperiments />

        <Card
          title="Feature flags"
          note="Killed beats off, a deny beats an allow, and a rollout is consulted last."
        >
          <Loaded state={state} skeleton={<TableSkeleton rows={5} cols={6} />}>
            {(answer) => (
              <FlagTable
                flags={answer.flags}
                targets={answer.targets}
                mayWrite={mayWriteFlags}
                onChanged={state.reload}
              />
            )}
          </Loaded>
        </Card>

        {mayReadEntitlements ? <Overrides mayWrite={operatorMay(me, "admin.entitlements.write")} /> : null}
      </div>

      {creating ? (
        <FlagForm
          title="Add a flag"
          onClose={() => setCreating(false)}
          onSaved={() => {
            setCreating(false);
            state.reload();
          }}
        />
      ) : null}
    </AdminPage>
  );
}

/* -------------------------------------------------------------------------
 * The absence, named
 * ---------------------------------------------------------------------- */

function NoExperiments() {
  return (
    <Card title="There are no experiments here">
      <div className="space-y-3 px-4 py-4 text-[13px] leading-6 text-muted">
        <p>
          This section is named for two things and one of them is not built. Feature flags below are
          complete: state, rollout, targets and a kill switch, all backed by real tables and all
          consulted by the request path. Experiment assignment and results are not wired at all, and
          no filter on this page will reveal them.
        </p>
        <p>
          A rollout percent is not an experiment. It turns a feature on for a stable share of
          subjects, hashed per flag, and nothing records which subject fell on which side. So there
          is no exposure to count, no arm to compare against, and no result to read.
        </p>
        <p>
          Four things would have to exist for the other half of this title to be true: an experiment
          with its hypothesis and its arms, a variant assignment written the first time a subject is
          bucketed, an exposure log that is separate from assignment because being bucketed is not
          being shown, and a metric definition to read a result against. Three of the four are
          tables; the fourth is a decision about what this product measures.
        </p>
      </div>
    </Card>
  );
}

/* -------------------------------------------------------------------------
 * Flags
 * ---------------------------------------------------------------------- */

function FlagTable({
  flags,
  targets,
  mayWrite,
  onChanged,
}: {
  flags: FeatureFlag[];
  targets: FlagTarget[];
  mayWrite: boolean;
  onChanged: () => void;
}) {
  const [open, setOpen] = useState<string | null>(null);
  const current = flags.find((f) => f.key === open) ?? null;
  const targetsFor = (key: string) => targets.filter((t) => t.flag_key === key);

  // SIX COLUMNS, and the count is the design. Seven pushed the Manage button
  // past the right edge of the card, where it sat inside a horizontal scroll
  // nobody would think to use for a control. The target count is a fact about
  // the state, so it reads under the state rather than as a column.
  const columns: Column<FeatureFlag>[] = [
    {
      key: "key",
      header: "Flag",
      cell: (f) => (
        <span className="block min-w-[22ch] max-w-[40ch]">
          <span className="block break-words font-mono text-[12px] font-medium text-ink">
            {f.key}
          </span>
          <span className="mt-0.5 block break-words text-[12px] leading-5 text-muted">
            {f.description}
          </span>
        </span>
      ),
    },
    {
      key: "state",
      header: "State",
      cell: (f) => {
        const targets = targetsFor(f.key).length;
        return (
          <span className="block min-w-[13ch]">
            <span className="flex flex-wrap items-center gap-1.5">
              <StatusChip
                value={f.state}
                tone={f.state === "on" ? "pass" : f.state === "off" ? "neutral" : "warn"}
              />
              {f.killed_at ? <Badge tone="fail">killed</Badge> : null}
              {f.internal_only ? <Badge tone="neutral">internal only</Badge> : null}
            </span>
            <span className="mt-1 block text-[12px] text-muted">
              {targets === 0
                ? "nobody targeted"
                : `${targets} ${targets === 1 ? "target" : "targets"}`}
            </span>
          </span>
        );
      },
    },
    {
      key: "rollout",
      header: "Rollout",
      numeric: true,
      cell: (f) =>
        f.state === "on" ? (
          // The rollout is only consulted when the flag is targeted, so a
          // percent beside an `on` flag would describe a rule nobody reaches.
          // The evaluation order is the semantics.
          <span className="whitespace-nowrap text-dim">not consulted</span>
        ) : (
          `${f.rollout_percent}%`
        ),
    },
    {
      // break-words rather than truncate on the call site: this is the path
      // somebody greps for to find out what the flag actually does, and half of
      // it is worth nothing.
      key: "known",
      header: "Read at",
      cell: (f) =>
        f.known ? (
          // Split at the colon rather than wrapped as one string. The value is
          // `path/from/src:symbol`, and a colon is not a break opportunity, so
          // a single span broke mid-identifier at "refuseWhenKille" and "d".
          // The file and the symbol are two facts anyway.
          <span className="block max-w-[30ch] font-mono text-[12px] text-muted">
            <span className="block break-words">{(f.checkedAt ?? "").split(":")[0]}</span>
            <span className="block break-words text-ink">
              {(f.checkedAt ?? "").split(":").slice(1).join(":")}
            </span>
          </span>
        ) : (
          // The most useful column on this table during an incident. A flag with
          // no call site is a switch that looks like a control and is not one,
          // and finding that out by flipping it is the worst way to learn.
          <span className="block min-w-[16ch] max-w-[26ch]">
            <Badge tone="warn">nothing reads it</Badge>
            <span className="mt-1 block text-[12px] leading-5 text-muted">
              flipping this changes nothing
            </span>
          </span>
        ),
    },
    {
      key: "updated",
      header: "Last changed",
      cell: (f) => (
        <span className="block min-w-0">
          <When value={f.updated_at} />
          {f.updated_by_label ? (
            <span className="block truncate text-[12px] text-muted">{f.updated_by_label}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: "manage",
      header: "Manage",
      cell: (f) => (
        // A real button rather than a clickable row: a row that opens a panel on
        // click has no keyboard equivalent and is never announced as opening
        // anything. The accessible name carries the flag, so twenty of these do
        // not all read "Manage".
        // The hidden half comes AFTER the visible word, so the accessible name
        // reads "Manage the flag billing.checkout" as a sentence. Split either
        // side of the label it read "Manage the flag Manage billing.checkout".
        <Button onClick={() => setOpen(f.key)}>
          {mayWrite ? "Manage" : "Look at"}
          <span className="sr-only"> the flag {f.key}</span>
        </Button>
      ),
    },
  ];

  return (
    <>
      <DataTable
        columns={columns}
        rows={flags}
        keyOf={(f) => f.key}
        empty={
          <EmptyList
            title="No feature flag exists"
            action={mayWrite ? undefined : undefined}
          >
            Nothing on this installation is behind a flag. A flag is created here rather than by a
            deploy, so the list stays empty until somebody adds one. Two are expected by the source
            and will appear as soon as they are created: the checkout switch and the switch that
            stops the administrative surface moving money.
          </EmptyList>
        }
      />
      {current ? (
        <FlagDrawer
          flag={current}
          targets={targets.filter((t) => t.flag_key === current.key)}
          mayWrite={mayWrite}
          onClose={() => setOpen(null)}
          onChanged={onChanged}
        />
      ) : null}
    </>
  );
}

function FlagDrawer({
  flag,
  targets,
  mayWrite,
  onClose,
  onChanged,
}: {
  flag: FeatureFlag;
  targets: FlagTarget[];
  mayWrite: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [targeting, setTargeting] = useState(false);
  const [killing, setKilling] = useState(false);

  const facts: Fact[] = [
    { label: "Key", value: flag.key, mono: true },
    { label: "What it does", value: flag.description },
    { label: "State", value: <StatusChip value={flag.state} /> },
    {
      label: "Rollout",
      value:
        flag.state === "on"
          ? "Not consulted while the flag is on for everybody"
          : `${flag.rollout_percent}% of subjects, hashed per flag`,
    },
    {
      label: "Internal only",
      value: flag.internal_only
        ? "Yes. Off for anybody outside the operator's own organizations."
        : "No",
    },
    {
      label: "Read at",
      value: flag.known ? (
        <span className="font-mono text-[12px]">{flag.checkedAt}</span>
      ) : (
        <span className="text-warn">
          Nothing in this build reads this flag, so changing it changes nothing.
        </span>
      ),
    },
    { label: "Killed", value: flag.killed_at ? <When value={flag.killed_at} /> : null },
    { label: "Killed by", value: flag.killed_by_label },
    { label: "Kill reason", value: flag.killed_reason },
    { label: "Last changed", value: flag.updated_at ? <When value={flag.updated_at} /> : null },
    { label: "Changed by", value: flag.updated_by_label },
  ];

  return (
    <>
      <Drawer
        open
        title={flag.key}
        onClose={onClose}
        actions={
          mayWrite ? (
            <>
              <Button onClick={() => setTargeting(true)}>Target somebody</Button>
              <Button onClick={() => setEditing(true)}>Change it</Button>
              <Button variant="danger" onClick={() => setKilling(true)}>
                Kill it
              </Button>
            </>
          ) : undefined
        }
      >
        <Facts facts={facts} />

        <div className="border-t border-rule px-4 py-3">
          <h3 className="text-[12px] font-medium uppercase tracking-[0.08em] text-dim">
            Who it is targeted at
          </h3>
          <p className="mt-1.5 text-[12.5px] leading-5 text-muted">
            A deny beats an allow and both beat the rollout, so one deny is how a single customer
            comes out of a rollout that is working for everybody else. A target cannot be deleted
            from this portal; setting the same subject the other way is the recorded change.
          </p>
        </div>
        {targets.length === 0 ? (
          <Empty title="Nobody is targeted">
            This flag applies by its state and its rollout alone. Add a target to turn it on or off
            for one organization, user, repository, plan or deployment.
          </Empty>
        ) : (
          <ul className="divide-y divide-rule border-t border-rule">
            {targets.map((t) => (
              <li key={t.id} className="px-4 py-3">
                <div className="flex flex-wrap items-center gap-2">
                  {t.allow ? <Badge tone="pass">allow</Badge> : <Badge tone="fail">deny</Badge>}
                  <span className="font-mono text-[12px] text-ink">
                    {t.kind}: {t.value}
                  </span>
                </div>
                {t.reason ? (
                  <p className="mt-1 break-words text-[12.5px] leading-5 text-muted">{t.reason}</p>
                ) : null}
                <p className="mt-1 text-[12px] text-dim">
                  {t.created_by_label ?? "unknown"}, <When value={t.created_at} />
                </p>
              </li>
            ))}
          </ul>
        )}
      </Drawer>

      {editing ? (
        <FlagForm
          title={`Change ${flag.key}`}
          flag={flag}
          onClose={() => setEditing(false)}
          onSaved={() => {
            setEditing(false);
            onChanged();
          }}
        />
      ) : null}

      {targeting ? (
        <TargetForm
          flagKey={flag.key}
          onClose={() => setTargeting(false)}
          onSaved={() => {
            setTargeting(false);
            onChanged();
          }}
        />
      ) : null}

      <KillConfirm
        flag={flag}
        open={killing}
        onCancel={() => setKilling(false)}
        onKilled={() => {
          setKilling(false);
          onChanged();
        }}
      />
    </>
  );
}

/* -------------------------------------------------------------------------
 * Writing a flag
 * ---------------------------------------------------------------------- */

const KEY_SHAPE = /^[a-z][a-z0-9]*([._-][a-z0-9]+)*$/;

/**
 * Create or change a flag.
 *
 * ONE FORM FOR BOTH, because the route is one upsert and a second form would be
 * a second place for the field rules to drift. The key is the only field that
 * differs: it is editable when creating and fixed afterwards, since changing it
 * would silently create a second flag and leave the first one live.
 *
 * TURNING A FLAG OFF HERE IS NOT A KILL and the copy says so. `set` records a
 * configuration change at notice severity; `kill` records an incident action at
 * high severity with a reason attached. Somebody reconstructing an incident six
 * months later is looking for the second, and a form that collapsed them would
 * make that line impossible to find.
 */
function FlagForm({
  title,
  flag,
  onClose,
  onSaved,
}: {
  title: string;
  flag?: FeatureFlag;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [key, setKey] = useState(flag?.key ?? "");
  const [description, setDescription] = useState(flag?.description ?? "");
  const [flagState, setFlagState] = useState<"off" | "on" | "targeted">(flag?.state ?? "off");
  const [rollout, setRollout] = useState(String(flag?.rollout_percent ?? 0));
  const [internalOnly, setInternalOnly] = useState(flag?.internal_only ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const keyError =
    flag || key === ""
      ? null
      : KEY_SHAPE.test(key)
        ? null
        : "Lower case letters and digits, separated by a dot, a dash or an underscore.";
  const descriptionError =
    description === "" || description.trim().length >= 8
      ? null
      : "At least eight characters. This is what somebody reads at three in the morning.";
  const rolloutNumber = Number(rollout);
  const rolloutError =
    Number.isInteger(rolloutNumber) && rolloutNumber >= 0 && rolloutNumber <= 100
      ? null
      : "A whole number between 0 and 100.";

  const ready =
    key !== "" &&
    keyError === null &&
    description.trim().length >= 8 &&
    rolloutError === null &&
    !busy;

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await setFlag({
        key,
        description: description.trim(),
        state: flagState,
        rolloutPercent: rolloutNumber,
        internalOnly,
      });
      onSaved();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer
      open
      title={title}
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" onClick={save} disabled={!ready} busy={busy}>
            {flag ? "Save the change" : "Create the flag"}
          </Button>
        </>
      }
    >
      <div className="space-y-4 px-4 py-4">
        <Field
          label="Key"
          hint={
            flag
              ? "Fixed. Changing a key would create a second flag and leave this one live."
              : "How the source asks for it. Lower case, dot separated by convention."
          }
          error={keyError}
        >
          <input
            className={inputClass}
            value={key}
            onChange={(e) => setKey(e.target.value)}
            disabled={flag !== undefined}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field
          label="What it does"
          hint="One or two sentences, in the words somebody reads during an incident."
          error={descriptionError}
        >
          <textarea
            className={`${inputClass} h-auto min-h-24 py-2`}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={3}
          />
        </Field>

        <Field
          label="State"
          hint="Off for everybody, on for everybody, or on for the targets and the rollout below."
        >
          <select
            className={`${selectClass} mt-1.5 w-full`}
            value={flagState}
            onChange={(e) => setFlagState(e.target.value as "off" | "on" | "targeted")}
          >
            <option value="off">Off for everybody</option>
            <option value="on">On for everybody</option>
            <option value="targeted">Targeted, plus the rollout</option>
          </select>
        </Field>

        <Field
          label="Rollout percent"
          hint="Consulted only when the state is targeted. A stable share, hashed per flag and per subject, so the same subject keeps the same answer."
          error={rolloutError}
        >
          <input
            className={inputClass}
            type="number"
            min={0}
            max={100}
            value={rollout}
            onChange={(e) => setRollout(e.target.value)}
            inputMode="numeric"
          />
        </Field>

        <label className="flex items-start gap-2.5">
          <input
            type="checkbox"
            className="mt-0.5 h-4 w-4 accent-ink"
            checked={internalOnly}
            onChange={(e) => setInternalOnly(e.target.checked)}
          />
          <span className="text-[13px] leading-5 text-ink">
            Internal only
            <span className="mt-0.5 block text-[12px] leading-5 text-dim">
              Off for anybody outside the operator&apos;s own organizations, whatever the rollout
              says.
            </span>
          </span>
        </label>

        {flagState === "off" && flag ? (
          <p className="rounded-md border border-rule bg-[rgba(138,90,0,0.12)] px-3 py-2.5 text-[12.5px] leading-5 text-ink">
            Turning it off here is recorded as a configuration change, not as a kill. If this is an
            incident, close this and use Kill it instead: the kill demands a reason and is the line
            somebody will be looking for when they reconstruct the timeline.
          </p>
        ) : null}

        {error ? (
          <p role="alert" className="text-[12.5px] leading-5 text-fail">
            {error}
          </p>
        ) : null}
      </div>
    </Drawer>
  );
}

function TargetForm({
  flagKey,
  onClose,
  onSaved,
}: {
  flagKey: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [kind, setKind] = useState<FlagTarget["kind"]>("organization");
  const [value, setValue] = useState("");
  const [allow, setAllow] = useState(true);
  const [orgId, setOrgId] = useState("");
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const reasonError =
    reason === "" || reason.trim().length >= 8
      ? null
      : "At least eight characters. A target with no reason is one nobody can undo confidently.";
  const ready = value.trim() !== "" && reason.trim().length >= 8 && !busy;

  async function save() {
    setBusy(true);
    setError(null);
    try {
      await targetFlag({
        flagKey,
        kind,
        value: value.trim(),
        allow,
        orgId: orgId.trim() === "" ? null : orgId.trim(),
        reason: reason.trim(),
      });
      onSaved();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer
      open
      title={`Target ${flagKey}`}
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button variant="primary" onClick={save} disabled={!ready} busy={busy}>
            {allow ? "Turn it on for them" : "Turn it off for them"}
          </Button>
        </>
      }
    >
      <div className="space-y-4 px-4 py-4">
        <p className="text-[12.5px] leading-5 text-muted">
          A deny is consulted before the state and before the rollout, so it takes one subject out
          of a rollout that is fine for everybody else. That is the usual incident action, and it is
          recorded at a higher severity than an allow for exactly that reason.
        </p>

        <Field label="Kind" hint="What the subject is.">
          <select
            className={`${selectClass} mt-1.5 w-full`}
            value={kind}
            onChange={(e) => setKind(e.target.value as FlagTarget["kind"])}
          >
            <option value="organization">Organization</option>
            <option value="user">User</option>
            <option value="project">Project</option>
            <option value="repository">Repository, as owner and name</option>
            <option value="plan">Plan</option>
            <option value="environment">Deployment, such as production or staging</option>
          </select>
        </Field>

        <Field
          label="Value"
          hint={
            kind === "repository"
              ? "owner/name, and owner/* matches a whole owner."
              : "The identifier this kind is matched on."
          }
        >
          <input
            className={inputClass}
            value={value}
            onChange={(e) => setValue(e.target.value)}
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field
          label="Organization this concerns"
          hint="Optional. Its own field because it decides whose audit trail this lands in, which is not always the subject: a repository target concerns the organization that owns it."
        >
          <input
            className={inputClass}
            value={orgId}
            onChange={(e) => setOrgId(e.target.value)}
            placeholder="Organization id, or leave empty"
            autoComplete="off"
            spellCheck={false}
          />
        </Field>

        <Field label="Reason" hint="Written into the audit chain beside your name." error={reasonError}>
          <textarea
            className={`${inputClass} h-auto min-h-20 py-2`}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            rows={2}
          />
        </Field>

        <fieldset>
          <legend className="text-[12px] font-medium text-muted">Direction</legend>
          <div className="mt-2 space-y-2">
            <label className="flex items-start gap-2.5">
              <input
                type="radio"
                name="direction"
                className="mt-0.5 h-4 w-4 accent-ink"
                checked={allow}
                onChange={() => setAllow(true)}
              />
              <span className="text-[13px] leading-5 text-ink">
                Allow
                <span className="mt-0.5 block text-[12px] leading-5 text-dim">
                  On for this subject even when the flag is targeted and the rollout would not have
                  reached them.
                </span>
              </span>
            </label>
            <label className="flex items-start gap-2.5">
              <input
                type="radio"
                name="direction"
                className="mt-0.5 h-4 w-4 accent-ink"
                checked={!allow}
                onChange={() => setAllow(false)}
              />
              <span className="text-[13px] leading-5 text-ink">
                Deny
                <span className="mt-0.5 block text-[12px] leading-5 text-dim">
                  Off for this subject even when the flag is on for everybody.
                </span>
              </span>
            </label>
          </div>
        </fieldset>

        {error ? (
          <p role="alert" className="text-[12.5px] leading-5 text-fail">
            {error}
          </p>
        ) : null}
      </div>
    </Drawer>
  );
}

/**
 * The kill, behind a confirmation that makes you type the flag's own key.
 *
 * `phrase` is the flag rather than the word "kill", for the reason `Confirm`
 * gives: typing "kill" proves you can read a label, and typing the key proves
 * you know which switch you are throwing. During an incident, with several
 * flags open, that is the mistake that actually happens.
 */
function KillConfirm({
  flag,
  open,
  onCancel,
  onKilled,
}: {
  flag: FeatureFlag;
  open: boolean;
  onCancel: () => void;
  onKilled: () => void;
}) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function confirm() {
    if (reason.trim().length < 8) {
      setError("A kill needs a reason of at least eight characters.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await killFlag(flag.key, reason.trim());
      setReason("");
      onKilled();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Confirm
      open={open}
      title={`Kill ${flag.key}`}
      phrase={flag.key}
      confirmLabel="Kill it now"
      busy={busy}
      error={error}
      onConfirm={confirm}
      onCancel={onCancel}
    >
      <p>
        This turns the flag off for everybody immediately and stamps who did it, when, and why. The
        kill is consulted before the state, before every target and before the rollout, so nothing
        can put it back except turning the flag on again.
      </p>
      {!flag.known ? (
        <p className="text-warn">
          Nothing in this build reads this flag. Killing it will be recorded and will change no
          behaviour, so if you are in an incident the lever you want is somewhere else.
        </p>
      ) : null}
      <Field label="Reason" hint="Written into the audit chain at high severity beside your name.">
        <textarea
          className={`${inputClass} h-auto min-h-20 py-2`}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={2}
        />
      </Field>
    </Confirm>
  );
}

/* -------------------------------------------------------------------------
 * Entitlement overrides
 * ---------------------------------------------------------------------- */

/**
 * The limits one organization holds, and which of them were granted rather than
 * set by its plan.
 *
 * ORGANIZATION BY ORGANIZATION, because that is the shape of the route and the
 * shape of the question. There is no route that lists every live override on
 * the installation, and this page does not fake one by paging every tenant and
 * asking about each: that would be one query per organization, on the operator
 * pool, to build a list nobody asked for.
 */
function Overrides({ mayWrite }: { mayWrite: boolean }) {
  const [search, setSearch] = useState("");
  const [picked, setPicked] = useState<{ id: string; slug: string } | null>(null);
  const tenants = useTenants(search);
  const state = useOrgEntitlements(picked?.id ?? null);
  const [revoking, setRevoking] = useState<OrgEntitlement | null>(null);

  if (!picked) {
    return (
      <Card
        title="Entitlement overrides"
        note="A limit granted to one organization rather than set by its plan."
      >
        <form
          role="search"
          className="flex flex-wrap items-end gap-2 border-b border-rule px-4 py-3"
          onSubmit={(e) => {
            e.preventDefault();
          }}
        >
          <div className="min-w-0 flex-1 basis-[240px]">
            <label htmlFor="override-org" className="block text-[12px] font-medium text-muted">
              Which organization
            </label>
            <input
              id="override-org"
              type="search"
              className={inputClass}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Name or slug"
              autoComplete="off"
              spellCheck={false}
            />
          </div>
        </form>
        <Loaded state={tenants} skeleton={<TableSkeleton rows={3} cols={2} />}>
          {(rows) =>
            rows.length === 0 ? (
              <Empty title={search ? "No tenant matches that" : "No organizations yet"}>
                {search
                  ? "Nothing on this installation has that name or slug."
                  : "Overrides are held per organization, so this needs one to look at."}
              </Empty>
            ) : (
              <ul className="divide-y divide-rule">
                {rows.slice(0, 12).map((t) => (
                  <li key={t.id}>
                    <button
                      type="button"
                      onClick={() => setPicked({ id: t.id, slug: t.slug })}
                      className="flex min-h-11 w-full flex-wrap items-center justify-between gap-2 px-4 py-3 text-left hover:bg-[rgba(16,16,16,0.035)]"
                    >
                      <span className="min-w-0">
                        <span className="block truncate text-[13px] font-medium text-ink">
                          {t.name}
                        </span>
                        <span className="block truncate font-mono text-[12px] text-muted">
                          {t.slug}
                        </span>
                      </span>
                      <span className="text-[12.5px] text-muted">{t.plan}</span>
                    </button>
                  </li>
                ))}
              </ul>
            )
          }
        </Loaded>
      </Card>
    );
  }

  return (
    <>
      <Card
        title={`Entitlements for ${picked.slug}`}
        note="The plan's own value beside the grant that moved it."
        actions={<Button onClick={() => setPicked(null)}>Choose another organization</Button>}
      >
        <Loaded state={state} skeleton={<TableSkeleton rows={5} cols={5} />}>
          {(answer) =>
            answer === null ? (
              <Empty title="Nothing to show">Pick an organization to see what it is entitled to.</Empty>
            ) : (
              <DataTable
                columns={entitlementColumns(mayWrite, setRevoking)}
                rows={answer.entitlements}
                keyOf={(e) => e.key}
                empty={
                  <EmptyList title="This build defines no entitlement">
                    Nothing is limited per organization, so there is nothing to override.
                  </EmptyList>
                }
              />
            )
          }
        </Loaded>
      </Card>

      {revoking && revoking.override ? (
        <RevokeConfirm
          entitlement={revoking}
          onCancel={() => setRevoking(null)}
          onRevoked={() => {
            setRevoking(null);
            state.reload();
          }}
        />
      ) : null}
    </>
  );
}

function entitlementColumns(
  mayWrite: boolean,
  onRevoke: (e: OrgEntitlement) => void,
): Column<OrgEntitlement>[] {
  return [
    {
      key: "key",
      header: "Entitlement",
      cell: (e) => (
        <span className="block min-w-0">
          <span className="block truncate font-mono text-[12px] font-medium text-ink">{e.key}</span>
          <span className="block max-w-[52ch] break-words text-[12px] text-muted">
            {e.description}
          </span>
        </span>
      ),
    },
    {
      key: "value",
      header: "In force",
      numeric: true,
      cell: (e) => (
        <span className="flex flex-col items-end">
          <span>{formatEntitlement(e.value, e.unit)}</span>
          {e.override ? (
            // Both numbers, always. A grant that reads like the plan's normal
            // behaviour is a grant nobody remembers to take back.
            <span className="text-[12px] text-muted">
              plan says {formatEntitlement(e.planValue, e.unit)}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: "source",
      header: "Decided by",
      cell: (e) =>
        e.override === null ? (
          <span className="text-muted">the plan</span>
        ) : (
          <span className="flex flex-wrap items-center gap-1.5">
            <Badge tone="warn">{e.override.scope} override</Badge>
            {e.override.ticket ? (
              <span className="font-mono text-[12px] text-muted">{e.override.ticket}</span>
            ) : null}
          </span>
        ),
    },
    {
      key: "expires",
      header: "Until",
      cell: (e) =>
        e.override === null ? (
          <span className="text-dim">--</span>
        ) : e.override.expiresAt === null ? (
          // Forever is a real answer and the one worth seeing. A grant with no
          // end is a plan change nobody wrote down.
          <span className="text-warn">no expiry, it never lapses</span>
        ) : (
          <When value={e.override.expiresAt} />
        ),
    },
    {
      key: "granted",
      header: "Granted",
      cell: (e) =>
        e.override === null ? (
          <span className="text-dim">--</span>
        ) : (
          <span className="block min-w-0">
            <span className="block truncate text-[12.5px]">{e.override.grantedBy}</span>
            <span className="block text-[12px] text-muted">
              <When value={e.override.grantedAt} />
            </span>
            <span className="mt-0.5 block max-w-[40ch] break-words text-[12px] text-muted">
              {e.override.reason}
            </span>
          </span>
        ),
    },
    {
      key: "enforced",
      header: "Enforced",
      cell: (e) =>
        e.enforced ? (
          <Badge tone="pass">yes</Badge>
        ) : (
          <span className="flex flex-col gap-1">
            <Badge tone="warn">not enforced</Badge>
            {e.notEnforcedBecause ? (
              <span className="max-w-[36ch] break-words text-[12px] text-muted">
                {e.notEnforcedBecause}
              </span>
            ) : null}
          </span>
        ),
    },
    {
      key: "revoke",
      header: "Revoke",
      cell: (e) =>
        e.override && mayWrite ? (
          <Button variant="danger" onClick={() => onRevoke(e)}>
            Revoke
            <span className="sr-only"> the override on {e.key}</span>
          </Button>
        ) : (
          <span className="text-dim">--</span>
        ),
    },
  ];
}

function formatEntitlement(value: number | boolean, unit: string | null): string {
  if (typeof value === "boolean") return value ? "yes" : "no";
  return unit ? `${value.toLocaleString()} ${unit}` : value.toLocaleString();
}

function RevokeConfirm({
  entitlement,
  onCancel,
  onRevoked,
}: {
  entitlement: OrgEntitlement;
  onCancel: () => void;
  onRevoked: () => void;
}) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function confirm() {
    if (reason.trim().length < 8) {
      setError("A revoke needs a reason of at least eight characters.");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await revokeOverride(entitlement.override!.id, reason.trim());
      onRevoked();
    } catch (err) {
      setError((err as ApiError).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Confirm
      open
      title={`Revoke the override on ${entitlement.key}`}
      phrase={entitlement.key}
      confirmLabel="Revoke it"
      busy={busy}
      error={error}
      onConfirm={confirm}
      onCancel={onCancel}
    >
      <p>
        The limit goes back to what the plan says, which is{" "}
        {formatEntitlement(entitlement.planValue, entitlement.unit)}. Capacity is money by another
        name, so this is recorded at the same severity as a refund.
      </p>
      <Field label="Reason" hint="Written into the audit chain beside your name.">
        <textarea
          className={`${inputClass} h-auto min-h-20 py-2`}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={2}
        />
      </Field>
    </Confirm>
  );
}
