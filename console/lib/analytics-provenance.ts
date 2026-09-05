/**
 * Which sentence the analytics page owes the reader about recording.
 *
 * Three outcomes rather than a boolean, because "not recording" covers two
 * situations that look identical on the page and mean opposite things. A
 * control plane that never switched analytics on is running as intended:
 * staging does it, and so does any self-hosted installation whose operator does
 * not want the numbers. A control plane that recorded until some point and does
 * not now has lost something, and the numbers on screen are real but frozen,
 * which is the more convincing kind of wrong.
 *
 * The server decides which of the two it is, because it is the half that can
 * see the database, and `recordingStopped` carries the answer. This function
 * exists so that the choice is one named thing with a test rather than a
 * nested conditional inside a component, and so the page and the API cannot
 * drift into disagreeing about what the same two booleans mean.
 */
export type RecordingState = "recording" | "never-recorded" | "stopped";

export function recordingState(p: {
  recording: boolean;
  recordingStopped: boolean;
}): RecordingState {
  if (p.recording) return "recording";
  return p.recordingStopped ? "stopped" : "never-recorded";
}
