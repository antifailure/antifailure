"use client";

import { Badge } from "@/components/ui";
import {
  transportFor,
  statusChipTone,
  statusChipLabel,
  type AgentView,
} from "@/lib/live";
import { ScanlineFrame } from "./ScanlineFrame";

/** The persona and workflow, the label a watcher recognises a pane by. */
export function PersonaBadge({ agent }: { agent: AgentView }) {
  return (
    <span className="min-w-0 truncate">
      <span className="font-semibold text-[rgb(46,45,39)]">{agent.persona ?? agent.id}</span>
      {agent.workflow ? <span className="text-[rgb(90,88,78)]"> {"·"} {agent.workflow}</span> : null}
    </span>
  );
}

/** The static status chip. A word and a colour, never a pulse. */
export function StatusChip({ agent }: { agent: AgentView }) {
  return (
    <Badge tone={statusChipTone(agent.state, agent.verdict)}>
      {statusChipLabel(agent.state, agent.verdict)}
    </Badge>
  );
}

/** A monospace timestamp, static. The frame's own time when there is one, so a
 *  stalled pane shows an old time rather than looking current. */
export function FrameClock({ agent }: { agent: AgentView }) {
  const at = agent.lastFrame?.at;
  if (!at) return null;
  const d = new Date(at);
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  return (
    <span className="tabular-nums">
      {hh}:{mm}:{ss} {"·"} {agent.lastFrame!.w}x{agent.lastFrame!.h}
    </span>
  );
}

function FrameStream({ agent }: { agent: AgentView }) {
  const frame = agent.lastFrame!;
  return (
    <img
      src={`data:image/jpeg;base64,${frame.b64}`}
      alt={`Latest frame from ${agent.persona ?? agent.id}`}
      width={frame.w}
      height={frame.h}
      className="absolute inset-0 h-full w-full object-contain"
    />
  );
}

/** A terminal agent's cast: its output is the picture, so the text is shown as
 *  the screen rather than a frame that does not exist. */
function Cast({ agent }: { agent: AgentView }) {
  const lines = agent.steps.slice(-14);
  return (
    <div className="watch-cast absolute inset-0 overflow-auto p-3 text-[12px] leading-[1.5]">
      {lines.length === 0 ? (
        <div className="text-[rgb(140,139,128)]">No output yet.</div>
      ) : (
        lines.map((s) => (
          <div key={s.seq} className="whitespace-pre-wrap break-words">
            {s.text}
          </div>
        ))
      )}
    </div>
  );
}

/** What a pane shows before it has anything to show. Content shaped and honest,
 *  never a spinner: it says which of the two states it is in. */
function Connecting({ agent }: { agent: AgentView }) {
  const message =
    agent.state === "pending"
      ? "Waiting to start."
      : agent.surface === "terminal"
        ? "Starting the process."
        : "Opening the browser.";
  return (
    <div className="absolute inset-0 flex items-center justify-center px-4 text-center">
      <span className="watch-caption text-[12px] text-[rgb(180,177,166)]">{message}</span>
    </div>
  );
}

/** The ended view: the last frame it left, dimmed, with the verdict over it, or
 *  the final cast for a terminal agent. An agent that produced no frame says so
 *  rather than showing an empty screen. */
function Ended({ agent }: { agent: AgentView }) {
  if (agent.surface === "terminal") return <Cast agent={agent} />;
  return (
    <div className="absolute inset-0">
      {agent.lastFrame ? (
        <img
          src={`data:image/jpeg;base64,${agent.lastFrame.b64}`}
          alt={`Final frame from ${agent.persona ?? agent.id}`}
          className="absolute inset-0 h-full w-full object-contain opacity-70"
        />
      ) : (
        <div className="absolute inset-0 flex items-center justify-center px-4 text-center">
          <span className="watch-caption text-[12px] text-[rgb(180,177,166)]">
            No frame was captured.
          </span>
        </div>
      )}
      <div className="absolute inset-x-0 bottom-0 flex items-center justify-between gap-2 bg-[rgba(20,19,15,0.72)] px-3 py-2">
        <span className="watch-caption text-[11px] text-[rgb(214,211,201)]">Run ended</span>
        <StatusChip agent={agent} />
      </div>
    </div>
  );
}

/** LivePlayer picks the right surface for the agent and renders it. */
export function LivePlayer({ agent }: { agent: AgentView }) {
  const transport = transportFor(agent.surface, agent.state, !!agent.lastFrame);
  switch (transport) {
    case "frames":
      return <FrameStream agent={agent} />;
    case "cast":
      return <Cast agent={agent} />;
    case "ended":
      return <Ended agent={agent} />;
    default:
      return <Connecting agent={agent} />;
  }
}

/** StepTicker is the current step under a pane, the sentence the agent is on. */
export function StepTicker({ agent }: { agent: AgentView }) {
  const last = agent.steps.at(-1);
  if (!last) return <span className="text-[rgb(120,118,108)]">No steps yet</span>;
  return (
    <span className="min-w-0">
      <span className="text-[rgb(46,45,39)]">{last.text}</span>
      {last.url ? <span className="text-[rgb(120,118,108)]"> {"·"} {last.url}</span> : null}
    </span>
  );
}

/** One agent's pane: caption, the live surface, and a footer with the step and
 *  the frame clock. The whole thing wears the film skin. */
export function AgentPane({ agent, big = false }: { agent: AgentView; big?: boolean }) {
  return (
    <div className={big ? "" : "min-w-0"}>
      <ScanlineFrame
        caption={<PersonaBadge agent={agent} />}
        chip={<StatusChip agent={agent} />}
        footer={
          <div className="flex flex-col gap-1">
            <StepTicker agent={agent} />
            <div className="flex items-center justify-between gap-2 text-[rgb(120,118,108)]">
              <span>{agent.surface} surface</span>
              <FrameClock agent={agent} />
            </div>
          </div>
        }
      >
        <LivePlayer agent={agent} />
      </ScanlineFrame>
    </div>
  );
}

/** Filmstrip is the durable scrubber shown after a run, sourced from the webm
 *  reference the runner registered. Video playback rides that reference and is
 *  the next step; until it is wired, this shows the last frame and says plainly
 *  that the recording is served from the runner edge, not this portal. */
export function Filmstrip({ agent, webmRef }: { agent: AgentView; webmRef?: string }) {
  return (
    <div className="watch-mat overflow-hidden rounded-lg border border-rule p-3">
      <div className="watch-caption mb-2 text-[11px] text-[rgb(70,68,60)]">
        Recording {webmRef ? "available" : "reference not yet attached"}
      </div>
      {agent.lastFrame ? (
        <img
          src={`data:image/jpeg;base64,${agent.lastFrame.b64}`}
          alt={`Recording poster for ${agent.persona ?? agent.id}`}
          className="w-full rounded-sm border border-[rgba(46,45,39,0.18)]"
        />
      ) : null}
      <div className="watch-caption mt-2 text-[11px] text-[rgb(120,118,108)]">
        The recording streams from the runner edge. This portal holds a reference and a hash, never the bytes.
      </div>
    </div>
  );
}
