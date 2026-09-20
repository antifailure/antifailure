// What a driven surface is, once the browser is not the only one.
//
// Four methods, and every one of them is a sentence a person would say: look
// at the screen, type this into the thing called that, choose that, press
// that. It is the subset of runner/src/login.ts's Page that a workflow loop
// actually uses, minus everything that is a browser: no goto, because a
// desktop application has no address bar, and no HTTP status, because nothing
// here answers with one.
//
// An interface rather than a class for the reason runner/src/explore.ts gives
// for its own Surface: the loop that drives this is then the loop a test
// drives, so a test proves the shipped loop rather than proving a copy of it.

import type { Snapshot } from '../workflow.ts';

export interface AxSurface {
  /** snapshot describes the screen in the terms a decision is made in: the
   *  same Snapshot a browser produces, so the planner does not know or care
   *  which surface it is reading. */
  snapshot(): Promise<Snapshot>;
  /** fill types into the field whose accessible name matches. */
  fill(field: RegExp, value: string): Promise<void>;
  /** check chooses the checkbox, radio or switch whose accessible name
   *  matches. Separate from fill for the reason the browser keeps them
   *  separate: typing into a checkbox is refused, and agreeing to a term is
   *  not the same act as answering a question. */
  check(field: RegExp): Promise<void>;
  /** click presses the control whose accessible name matches. */
  click(control: RegExp): Promise<void>;
  /** close releases whatever this surface opened. Idempotent. */
  close(): Promise<void>;
}
