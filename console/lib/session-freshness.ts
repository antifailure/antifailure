export type SessionRefreshTrigger = "focus" | "visibilitychange";

/**
 * Whether a browser event means the shared session may have changed.
 *
 * Focus always asks again. A visibility change asks only when the document is
 * visible, since refreshing while it is being hidden cannot update a control
 * anybody can see and the matching visible event follows when they return.
 */
export function shouldRefreshSession(
  trigger: SessionRefreshTrigger,
  visibilityState: DocumentVisibilityState,
): boolean {
  return trigger === "focus" || visibilityState === "visible";
}

/**
 * Only the newest session response may replace the shared answer.
 *
 * Focus and visibility restoration can happen close together, and member
 * changes can overlap either one. An older response arriving last must not put
 * the old role or a session that has since been revoked back on screen.
 */
export function isCurrentResponse(
  mounted: boolean,
  responseSequence: number,
  latestSequence: number,
): boolean {
  return mounted && responseSequence === latestSequence;
}
