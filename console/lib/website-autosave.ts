import { assertWebsiteDocument, stableStringify, type WebsiteDocument } from "@antifailure/website";

export interface WebsiteSaveInput {
  document: WebsiteDocument;
  expectedRevision: number;
  requestId: string;
}

export interface WebsiteSaveResult {
  document: WebsiteDocument;
  draftRevision: number;
}

export type WebsiteAutosaveStatus = "saved" | "dirty" | "saving" | "error" | "conflict" | "suspended" | "disposed";

export interface WebsiteAutosaveSnapshot {
  /** Immutable. Pass a new document to edit(), including for undo and redo. */
  readonly document: WebsiteDocument;
  readonly revision: number;
  readonly status: WebsiteAutosaveStatus;
  readonly error: Error | null;
  readonly hasUnsavedChanges: boolean;
  readonly inFlight: boolean;
  readonly suspended: boolean;
}

export interface WebsiteAutosaveOptions {
  initialDocument: WebsiteDocument;
  initialRevision: number;
  save(input: WebsiteSaveInput): Promise<WebsiteSaveResult>;
  debounceMs?: number;
  onChange?(snapshot: WebsiteAutosaveSnapshot): void;
  /** Injected by tests. Production uses the browser's UUID and timers. */
  requestId?: () => string;
  scheduler?: {
    set(callback: () => void, delay: number): unknown;
    clear(handle: unknown): void;
  };
}

export interface WebsiteAutosave {
  /** Stable until state changes, suitable for useSyncExternalStore. */
  snapshot(): WebsiteAutosaveSnapshot;
  subscribe(listener: () => void): () => void;
  edit(document: WebsiteDocument): void;
  /** Waits until every edit has been acknowledged. Does not hide save errors. */
  flush(): Promise<WebsiteAutosaveSnapshot>;
  retry(): Promise<WebsiteAutosaveSnapshot>;
  /** Discards local edits in favor of a freshly fetched authoritative state. */
  reload(document: WebsiteDocument, revision: number): void;
  /** Explicitly keeps the local document against a freshly fetched revision. */
  reapply(serverDocument: WebsiteDocument, serverRevision: number): void;
  /** Freezes editing immediately. Explicit flush() remains available. */
  suspend(): void;
  resume(): void;
  /** Cancels future work, not a write that may already have committed. */
  dispose(): WebsiteAutosaveSnapshot;
}

function checkRevision(revision: number): void {
  if (!Number.isSafeInteger(revision) || revision < 0) throw new Error("The website revision is invalid.");
}

function immutable(document: WebsiteDocument): WebsiteDocument {
  const copy = assertWebsiteDocument(document);
  function freeze(value: unknown): void {
    if (!value || typeof value !== "object" || Object.isFrozen(value)) return;
    Object.values(value).forEach(freeze);
    Object.freeze(value);
  }
  freeze(copy);
  return copy;
}

function errorOf(cause: unknown): Error {
  return cause instanceof Error ? cause : new Error("Your changes could not be saved. Please retry.");
}

function refusal(cause: unknown): { conflict: boolean; definitive: boolean } {
  if (!cause || typeof cause !== "object") return { conflict: false, definitive: false };
  const { status, code } = cause as { status?: unknown; code?: unknown };
  const conflict = status === 409 || code === "CONFLICT";
  // These refusals did not commit. An interrupted request or a server error
  // might have, so its idempotency key and original payload must survive.
  const definitive = typeof status === "number" && status >= 400 && status < 500 && status !== 408 && status !== 429;
  return { conflict, definitive };
}

/** One writer per editor. The server revision is the cross-tab authority; this
 * queue never guesses that a rejected write succeeded or overwrites a newer
 * local edit with the response to an older one. */
export function createWebsiteAutosave(options: WebsiteAutosaveOptions): WebsiteAutosave {
  checkRevision(options.initialRevision);
  const debounceMs = options.debounceMs ?? 500;
  if (!Number.isFinite(debounceMs) || debounceMs < 0) throw new Error("The autosave delay is invalid.");
  const scheduler = options.scheduler ?? {
    set: (callback: () => void, delay: number) => setTimeout(callback, delay),
    clear: (handle: unknown) => clearTimeout(handle as ReturnType<typeof setTimeout>),
  };
  const requestId = options.requestId ?? (() => crypto.randomUUID());
  const listeners = new Set<() => void>();
  let document = immutable(options.initialDocument);
  let savedJSON = stableStringify(document);
  let localJSON = savedJSON;
  let revision = options.initialRevision;
  let error: Error | null = null;
  let conflict = false;
  let suspended = false;
  let disposed = false;
  let timer: unknown = null;
  let active: Promise<void> | null = null;
  let flushing = 0;
  let attempt: { input: WebsiteSaveInput; json: string } | null = null;
  let current: WebsiteAutosaveSnapshot;

  function dirty(): boolean {
    // An unacknowledged request may commit even if the user undid the edit.
    // It has to resolve before we can safely say the old value is saved.
    return localJSON !== savedJSON || attempt !== null;
  }

  function emit(): void {
    current = Object.freeze({
      document, revision, error, hasUnsavedChanges: dirty(), inFlight: active !== null, suspended,
      status: disposed ? "disposed" : conflict ? "conflict" : error ? "error" : active ? "saving" : suspended ? "suspended" : dirty() ? "dirty" : "saved",
    });
    if (!disposed) {
      options.onChange?.(current);
      listeners.forEach((listener) => listener());
    }
  }

  function cancelTimer(): void {
    if (timer !== null) scheduler.clear(timer);
    timer = null;
  }

  function requireOpen(): void {
    if (disposed) throw new Error("This website editor has closed.");
  }

  function schedule(): void {
    cancelTimer();
    if (disposed || suspended || conflict || error || active || flushing || !dirty()) return;
    timer = scheduler.set(() => {
      timer = null;
      // Scheduled saves report their error through the snapshot. Explicit
      // flush/retry calls still reject so Publish cannot run after a failure.
      void send().catch(() => {});
    }, debounceMs);
  }

  function send(): Promise<void> {
    if (active) return active;
    if (!dirty()) return Promise.resolve();
    if (!attempt) attempt = {
      input: Object.freeze({ document, expectedRevision: revision, requestId: requestId() }),
      json: localJSON,
    };
    const sent = attempt;
    active = Promise.resolve().then(() => options.save(sent.input)).then((result) => {
      checkRevision(result.draftRevision);
      if (result.draftRevision < sent.input.expectedRevision) throw new Error("The server returned an older website revision. Please retry.");
      const savedDocument = immutable(result.document);
      const acknowledgedJSON = stableStringify(savedDocument);
      revision = result.draftRevision;
      savedJSON = acknowledgedJSON;
      // Server normalization is useful only when it acknowledges the exact
      // state still displayed. Later edits must remain visible and queued.
      if (localJSON === sent.json) {
        document = savedDocument;
        localJSON = acknowledgedJSON;
      }
      attempt = null;
      error = null;
    }).catch((cause: unknown) => {
      const kind = refusal(cause);
      error = errorOf(cause);
      conflict = kind.conflict;
      if (kind.conflict || kind.definitive) attempt = null;
      throw error;
    }).finally(() => {
      active = null;
      // Even after unmount, retain the acknowledged revision and outstanding
      // edits. Aborting or ignoring this response would lose committed truth.
      emit();
      schedule();
    });
    emit();
    return active;
  }

  async function flush(): Promise<WebsiteAutosaveSnapshot> {
    requireOpen();
    cancelTimer();
    flushing++;
    try {
      while (active || dirty()) {
        requireOpen();
        if (conflict || error) throw error ?? new Error("The website changed in another editor. Reload it before saving.");
        await (active ?? send());
      }
      requireOpen();
      if (conflict || error) throw error ?? new Error("The website changed in another editor. Reload it before saving.");
      return current;
    } finally {
      flushing--;
      schedule();
    }
  }

  function acceptServer(serverDocument: WebsiteDocument, serverRevision: number, keepLocal: boolean): void {
    requireOpen();
    if (active) throw new Error("Wait for the current save before loading another version.");
    checkRevision(serverRevision);
    if (serverRevision < revision) throw new Error("This website version is older than the last saved version. Refresh and try again.");
    const accepted = immutable(serverDocument);
    cancelTimer();
    savedJSON = stableStringify(accepted);
    revision = serverRevision;
    if (!keepLocal) {
      document = accepted;
      localJSON = savedJSON;
    }
    attempt = null;
    error = null;
    conflict = false;
    emit();
    schedule();
  }

  emit();
  return {
    snapshot: () => current,
    subscribe(listener) {
      requireOpen();
      listeners.add(listener);
      return () => { listeners.delete(listener); };
    },
    edit(next) {
      requireOpen();
      if (suspended) throw new Error("Wait for publishing or restoring to finish before editing.");
      const nextDocument = immutable(next);
      const nextJSON = stableStringify(nextDocument);
      if (nextJSON === localJSON) return;
      document = nextDocument;
      localJSON = nextJSON;
      // A corrected validation error can save as a new request. Ambiguous
      // network failures keep their attempt until the user explicitly retries.
      if (!attempt && !conflict) error = null;
      emit();
      schedule();
    },
    flush,
    async retry() {
      requireOpen();
      if (conflict) throw error ?? new Error("Reload the website before retrying this conflict.");
      error = null;
      emit();
      return flush();
    },
    reload: (next, nextRevision) => acceptServer(next, nextRevision, false),
    reapply: (next, nextRevision) => acceptServer(next, nextRevision, true),
    suspend() {
      requireOpen();
      suspended = true;
      cancelTimer();
      emit();
    },
    resume() {
      requireOpen();
      suspended = false;
      emit();
      schedule();
    },
    dispose() {
      disposed = true;
      cancelTimer();
      listeners.clear();
      emit();
      return current;
    },
  };
}
