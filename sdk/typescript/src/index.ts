import { AsyncLocalStorage } from 'node:async_hooks';
import { createHash, createHmac, randomBytes, randomUUID } from 'node:crypto';
import { mkdir, open, link, unlink } from 'node:fs/promises';
import { join } from 'node:path';

export type JSONValue = null | boolean | number | string | JSONValue[] | { [key: string]: JSONValue };
export type BoundaryKind = 'model' | 'tool' | 'http' | 'database' | 'effect';
export interface RequestIdentity {
  kind: BoundaryKind;
  name: string;
  version: string;
  input: JSONValue;
  provider?: string;
  model?: string;
  instructions?: JSONValue;
  tools?: JSONValue;
  settings?: JSONValue;
}
export interface Exchange {
  seq: number;
  parent: number | null;
  request: RequestIdentity | null;
  key: string;
  kind: BoundaryKind;
  name: string;
  version: string;
  response?: JSONValue;
  error?: string;
  capturedAt: string;
  durationMs: number;
  provenance: 'recorded';
  usage?: Usage;
}
export interface Usage { inputTokens: number; outputTokens: number; costUSD: number }
export interface Incident {
  schemaVersion: 1;
  runId: string;
  traceId: string;
  project: string;
  service: string;
  commit: string;
  observedAt: string;
  policyVersion: string;
  input?: JSONValue;
  output?: JSONValue;
  inputHash: string;
  status: 'complete' | 'incomplete';
  issues: string[];
  exchanges: Exchange[];
  clock: 'sdk';
  identity: 'synthetic' | 'unmapped';
  golden: string;
  durationMs: number;
}
export interface CaptureOptions {
  project: string;
  service: string;
  commit: string;
  directory: string;
  policyVersion: string;
  /** Explicit names, such as input, output, model:answer, or tool:search. */
  content?: readonly string[];
  redact?: (value: JSONValue) => JSONValue;
  /** A key stays in the application, never in artifacts. Defaults to an ephemeral key. */
  hashKey?: Uint8Array;
  maxBytes?: number;
  maxExchanges?: number;
  maxConcurrentCaptures?: number;
  flushTimeoutMs?: number;
  onDiagnostic?: (reason: string) => void;
  failClosed?: boolean;
  now?: () => Date;
}
export interface RunOptions {
  runId?: string;
  traceId?: string;
  golden?: string;
  identity?: 'synthetic' | 'unmapped';
}
export interface ReplayRequest {
  schemaVersion: 1;
  input: JSONValue;
  clock: string;
  exchanges: Exchange[];
  commit?: string;
}
export interface ReplayResponse {
  schemaVersion: 1;
  output?: JSONValue;
  issues: string[];
  operations: { seq: number; kind: BoundaryKind; name: string; key: string; source: string }[];
  effects: { name: string; request: JSONValue }[];
  hits: number;
}
interface Context {
  owner: AgentReplay;
  incident?: Incident;
  replay?: ReplayRequest;
  result: ReplayResponse;
  cursor: number;
  bytes: number;
  pending: Set<Promise<unknown>>;
  closed: boolean;
}
const context = new AsyncLocalStorage<Context>();
const parentContext = new AsyncLocalStorage<{root: Context; seq: number}>();
const safeName = /^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$/;
const credential = /(?:(?:Bearer|Basic)\s+\S+|-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}|(?:sk_live_|sk-proj-|sk-ant-|ghp_|gho_|AKIA)[A-Za-z0-9_-]+|postgres(?:ql)?:\/\/[^\s]+|https?:\/\/[^\s/@]+:[^\s/@]+@[^\s]+)/gi;
const providerCredential = /\b(?:(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{10,247}|whsec_[A-Za-z0-9]{16,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}|(?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16}|xox[abposr]-[A-Za-z0-9-]{10,}|SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}|sk-(?:proj-|svcacct-|ant-)?[A-Za-z0-9_-]{20,}|AIza[0-9A-Za-z_-]{35}|AC[0-9a-fA-F]{32}|sbp_[0-9a-f]{40,}|napi_[a-z0-9]{20,}|npm_[A-Za-z0-9]{36}|dp\.(?:pt|st|sa|ct)\.[A-Za-z0-9]{20,})\b/g;
function deniedField(name: string): boolean {
  return /(?:authorization|cookie|password(?:hash)?|passwd|secret(?:accesskey)?|token|apikey|privatekey|connectionstring|credentials)$/.test(name.toLowerCase().replace(/[-_.]/g, ''));
}

/** Stable JSON is part of the wire contract, not JSON.stringify's insertion order. */
export function canonical(value: unknown): string {
  let nodes = 0;
  let bytes = 0;
  function encode(value: unknown, depth: number): string {
  if (++nodes > 100000 || depth > 100) throw new Error('JSON exceeds its structural limit.');
  if (typeof value === 'string') bytes += Buffer.byteLength(value);
  else bytes += 8;
  if (bytes > 4 * 1048576) throw new Error('JSON exceeds its byte limit.');
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return JSON.stringify(value);
  if (typeof value === 'number' && Number.isFinite(value)) return JSON.stringify(value);
  if (Array.isArray(value)) return '[' + value.map(v => encode(v, depth + 1)).join(',') + ']';
  if (typeof value === 'object' && value !== null && Object.getPrototypeOf(value) === Object.prototype) {
    return '{' + Object.keys(value).sort().map(k => encode(k, depth + 1) + ':' + encode((value as Record<string, unknown>)[k], depth + 1)).join(',') + '}';
  }
  throw new Error('Capture needs finite JSON values. Convert unsupported values explicitly.');
  }
  return encode(value, 0);
}
export function requestKey(request: RequestIdentity): string {
  return createHash('sha256').update(canonical(request)).digest('hex');
}
function scrub(value: JSONValue, depth = 0): JSONValue {
  if (depth > 64) return '[redacted]';
  if (typeof value === 'string') {
    let clean = value.replace(providerCredential, '[redacted]').replace(credential, '[redacted]').replace(/https?:\/\/[^\s"'<>]+/g, text => {
    try {
      const url = new URL(text); let changed = false;
      for (const key of [...url.searchParams.keys()]) {
        if (deniedField(key) || /^(?:sig|signature|x-amz-signature)$/i.test(key)) { url.searchParams.set(key, '[redacted]'); changed = true; }
      }
      return changed ? url.href : text;
    } catch { return text; }
    });
    clean = clean.replace(/"((?:\\.|[^"\\])*)"\s*:\s*"((?:\\.|[^"\\])*)"/g, (pair, key: string) => {
      try { const decoded = JSON.parse('"' + key + '"') as string; return deniedField(decoded) ? JSON.stringify(decoded) + ':"[redacted]"' : pair; } catch { return pair; }
    });
    if (/^\s*[\[{"]/.test(clean)) {
      let nested: JSONValue;
      try { nested = JSON.parse(clean) as JSONValue; } catch { return clean; }
      const protectedValue = scrub(nested, depth + 1);
      try { if (canonical(protectedValue) !== canonical(nested)) return JSON.stringify(protectedValue); } catch { return '[redacted]'; }
    }
    return clean;
  }
  if (Array.isArray(value)) return value.map(item => scrub(item, depth + 1));
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([k, v]) => [scrub(k, depth + 1) as string, deniedField(k) ? '[redacted]' : scrub(v, depth + 1)]));
  return value;
}
function issue(c: Context, reason: string): void {
  if (!c.result.issues.includes(reason)) c.result.issues.push(reason);
  if (c.incident && !c.incident.issues.includes(reason)) c.incident.issues.push(reason);
}
function validIdentity(r: RequestIdentity): void {
  if (!['model','tool','http','database','effect'].includes(r.kind) || !safeName.test(r.name) || !safeName.test(r.version)) throw new Error('Name every supported boundary and its version.');
  if (r.kind === 'model' && (!r.provider || !r.model || r.instructions === undefined || r.settings === undefined || r.tools === undefined)) throw new Error('Model capture requires provider, model, instructions, settings and tools.');
  canonical(r);
}

/** Capture is opt-in and never substitutes an application's return value. */
export class AgentReplay {
  readonly #options: CaptureOptions;
  readonly #key: Uint8Array;
  #active = 0;
  readonly #writes = new Set<Promise<void>>();
  constructor(options: CaptureOptions) {
    for (const value of [options.project, options.service, options.policyVersion]) if (!safeName.test(value)) throw new Error('Use a bounded name for project, service and capture policy.');
    if (!/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(options.commit)) throw new Error('Pin capture to a full Git commit.');
    for (const limit of [options.maxBytes, options.maxExchanges, options.maxConcurrentCaptures, options.flushTimeoutMs]) if (limit !== undefined && (!Number.isSafeInteger(limit) || limit < 1)) throw new Error('Capture limits must be positive integers.');
    this.#options = options;
    this.#key = options.hashKey ?? randomBytes(32);
  }
  #diagnose(reason: string): void {
    try {
      if (this.#options.onDiagnostic) this.#options.onDiagnostic(reason);
      else process.emitWarning(reason, {code:'AF_CAPTURE_INCOMPLETE'});
    } catch { /* A diagnostic must not change host behavior. */ }
  }
  #content(c: Context, name: string, value: unknown): JSONValue | undefined {
    if (!this.#options.content?.includes(name)) return undefined;
    try {
      const encoded = canonical(value);
      if (Buffer.byteLength(encoded) > Math.min(this.#options.maxBytes ?? 262144, 1048576)) throw new Error('size');
      const clean = scrub(this.#options.redact ? this.#options.redact(JSON.parse(encoded) as JSONValue) : JSON.parse(encoded) as JSONValue);
      const retainedBytes = Buffer.byteLength(canonical(clean));
      if (c.bytes + retainedBytes > 3 * 1048576) { issue(c, 'capture_byte_budget_exhausted'); return undefined; }
      c.bytes += retainedBytes;
      if (canonical(clean) !== encoded) issue(c, `identity_transformed:${name}`);
      return clean;
    } catch {
      issue(c, `content_unavailable:${name}`);
      this.#diagnose(`content_unavailable:${name}`);
      return undefined;
    }
  }
  /** Uses the declared application clock. It does not intercept Date.now(). */
  now(): Date {
    const c = context.getStore();
    if (c && c.owner !== this) { issue(c, 'sdk_instance_mismatch'); if (c.replay) throw new Error('Replay clock belongs to another SDK instance.'); }
    return c?.replay ? new Date(c.replay.clock) : (this.#options.now?.() ?? new Date());
  }
  /** Attach a checkpoint that became available after the run began. */
  checkpoint(golden: string): void {
    const c = context.getStore();
    if (!c?.incident || c.owner !== this || c.closed || !safeName.test(golden)) { this.#diagnose('checkpoint_unavailable'); return; }
    if (c.incident.golden && c.incident.golden !== golden) { issue(c, 'checkpoint_changed'); return; }
    c.incident.golden = golden;
  }
  async run<T extends JSONValue>(input: JSONValue, agent: () => Promise<T>, opts: RunOptions = {}): Promise<T> {
    const active = context.getStore();
    if (active?.replay) {
      if (active.owner !== this || canonical(input) !== canonical(active.replay.input)) {
        issue(active, 'nested_replay_identity_mismatch');
        throw new Error('A replay entry point cannot replace its capture context.');
      }
      return agent();
    }
    if (active?.incident) issue(active, 'nested_agent_run');
    const concurrencyLimit = Math.min(this.#options.maxConcurrentCaptures ?? 32, 64);
    if (this.#active >= concurrencyLimit || this.#writes.size >= concurrencyLimit) {
      this.#diagnose('capture_concurrency_limit');
      if (this.#options.failClosed) throw new Error('Protected capture exceeded its concurrency limit.');
      return context.exit(agent);
    }
    const validRunId = opts.runId === undefined || (safeName.test(opts.runId) && !opts.runId.includes(':') && scrub(opts.runId) === opts.runId);
    const validTraceId = opts.traceId === undefined || (/^[a-f0-9]{32}$/.test(opts.traceId) && opts.traceId !== '0'.repeat(32));
    const runId = validRunId ? (opts.runId ?? randomUUID()) : randomUUID();
    const traceId = validTraceId ? (opts.traceId ?? randomBytes(16).toString('hex')) : randomBytes(16).toString('hex');
    const started = performance.now();
    const incident: Incident = {
      schemaVersion: 1, runId, traceId, project: this.#options.project, service: this.#options.service,
      commit: this.#options.commit, observedAt: this.now().toISOString(), policyVersion: this.#options.policyVersion,
      inputHash: '', status: 'complete', issues: [], exchanges: [], clock: 'sdk', identity: opts.identity ?? 'unmapped', golden: opts.golden ?? '', durationMs: 0,
    };
    const c: Context = { owner:this, incident, result: {schemaVersion:1, issues: [], operations: [], effects: [], hits:0}, cursor:0, bytes:0, pending:new Set(), closed:false };
    if (!validRunId) { issue(c, 'run_id_invalid'); this.#diagnose('run_id_invalid'); }
    if (!validTraceId) { issue(c, 'trace_id_invalid'); this.#diagnose('trace_id_invalid'); }
    try { incident.inputHash = createHmac('sha256', this.#key).update(canonical(input)).digest('hex'); } catch { issue(c, 'input_unhashable'); }
    const retained = this.#content(c, 'input', input);
    if (retained !== undefined) incident.input = retained;
    this.#active++;
    return context.run(c, async () => {
      try {
        const result = await agent();
        const output = this.#content(c, 'output', result);
        if (output !== undefined) incident.output = output;
        return result;
      } catch (error) {
        issue(c, 'agent_error');
        throw error;
      } finally {
        c.closed = true;
        if (c.pending.size) issue(c, 'unfinished_operations');
        incident.durationMs = performance.now() - started;
        if (incident.issues.length) incident.status = 'incomplete';
        try { await this.#persist(incident); } catch {
          this.#diagnose(`capture_write_failed:${runId}`);
          if (this.#options.failClosed) throw new Error('Protected capture could not be persisted.');
        } finally { this.#active--; }
      }
    });
  }
  async #persist(incident: Incident): Promise<void> {
    const pending = this.#write(incident);
    this.#writes.add(pending);
    void pending.finally(() => this.#writes.delete(pending)).catch(() => {});
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      await Promise.race([pending, new Promise<never>((_resolve, reject) => {
        timer = setTimeout(() => reject(new Error('Capture flush exceeded its budget.')), Math.min(this.#options.flushTimeoutMs ?? 1000, 5000));
      })]);
    } finally { if (timer) clearTimeout(timer); }
  }
  async #write(incident: Incident): Promise<void> {
    const original = canonical(incident);
    const safe = scrub(JSON.parse(original) as JSONValue) as unknown as Incident;
    if (canonical(safe) !== original) { safe.status = 'incomplete'; safe.issues.push('writer_redacted_content'); }
    const body = canonical(safe) + '\n';
    if (Buffer.byteLength(body) > 4 * 1048576) throw new Error('Capture exceeded its total byte limit.');
    await mkdir(this.#options.directory, {recursive:true, mode:0o700});
    const target = join(this.#options.directory, incident.runId + '.json');
    const temporary = target + '.' + randomUUID() + '.tmp';
    const file = await open(temporary, 'wx', 0o600);
    try { await file.writeFile(body); await file.sync(); } finally { await file.close(); }
    try {
      await link(temporary, target);
      const directory = await open(this.#options.directory, 'r');
      try { await directory.sync(); } finally { await directory.close(); }
    } finally { await unlink(temporary); }
  }
  /** Database callbacks run against the isolated branch; other callbacks never run during replay. */
  async boundary<T extends JSONValue>(request: RequestIdentity, execute: () => Promise<T>, usage?: (response: T) => Usage): Promise<T> {
    const c = context.getStore();
    if (!c) return execute();
    if (c.owner !== this) {
      issue(c, 'sdk_instance_mismatch');
      if (c.replay) throw new Error('Replay boundary belongs to another SDK instance.');
      return execute();
    }
    if (c.closed) {
      if (c.replay) throw new Error('The replay is closed.');
      this.#diagnose('operation_after_run');
      return execute();
    }
    let key: string;
    try { validIdentity(request); key = requestKey(request); } catch {
      issue(c, 'invalid_request_identity');
      if (c.replay) throw new Error('The boundary identity cannot be replayed.');
      return execute();
    }
    if (c.replay) {
      if (c.pending.size) { issue(c, 'concurrent_boundaries'); throw new Error('Replay requires sequential boundaries.'); }
      const seq = c.cursor++;
      const recorded = c.replay.exchanges[seq];
      if (!recorded || recorded.key !== key || recorded.kind !== request.kind || recorded.version !== request.version) {
        issue(c, `cassette_miss:${seq}:${request.name}`);
        throw new Error(`Replay stopped at ${request.name}: no matching recorded request.`);
      }
      c.result.operations.push({seq, kind:request.kind, name:request.name, key, source:request.kind === 'database' ? 'isolated_database' : 'recorded'});
      if (request.kind === 'database') return this.#track(c, execute);
      c.result.hits++;
      if (request.kind === 'effect') c.result.effects.push({name:request.name, request:request.input});
      if (recorded.error !== undefined) throw new Error(recorded.error);
      if (recorded.response === undefined) { issue(c, `missing_response:${seq}`); throw new Error('The recorded response is absent.'); }
      return this.#track(c, async () => structuredClone(recorded.response) as T);
    }
    const incident = c.incident!;
    if (incident.exchanges.length >= Math.min(this.#options.maxExchanges ?? 1000, 10000)) { issue(c, 'exchange_limit'); return execute(); }
    const seq = incident.exchanges.length;
    const clean = this.#content(c, `${request.kind}:${request.name}`, request);
    const parent = parentContext.getStore();
    const exchange: Exchange = {seq, parent:parent?.root === c ? parent.seq : null, request:clean as RequestIdentity | undefined ?? null,
      key:clean === undefined ? createHmac('sha256', this.#key).update(canonical(request)).digest('hex') : requestKey(clean as unknown as RequestIdentity),
      kind:request.kind, name:request.name, version:request.version, capturedAt:this.now().toISOString(), durationMs:0, provenance:'recorded'};
    incident.exchanges.push(exchange);
    if (c.pending.size) issue(c, 'concurrent_boundaries');
    const started = performance.now();
    try {
      const result = await this.#track(c, execute, seq);
      if (usage) {
        try {
          const supplied = usage(result);
          if (!Number.isSafeInteger(supplied.inputTokens) || !Number.isSafeInteger(supplied.outputTokens) || supplied.inputTokens < 0 || supplied.outputTokens < 0 || !Number.isFinite(supplied.costUSD) || supplied.costUSD < 0) throw new Error('Invalid usage.');
          exchange.usage = {inputTokens:supplied.inputTokens, outputTokens:supplied.outputTokens, costUSD:supplied.costUSD};
        } catch { issue(c, 'usage_metadata_unavailable'); }
      }
      const response = this.#content(c, `${request.kind}:${request.name}`, result);
      if (response !== undefined) exchange.response = response;
      return result;
    } catch (error) {
      exchange.error = 'Recorded operation failed.';
      throw error;
    } finally { exchange.durationMs = performance.now() - started; }
  }
  async #track<T>(c: Context, execute: () => Promise<T>, seq = -1): Promise<T> {
    const pending = Promise.resolve().then(() => parentContext.run({root:c, seq}, execute));
    c.pending.add(pending);
    try { return await pending; } finally { c.pending.delete(pending); }
  }
  /** Call only from a test endpoint enabled explicitly in the isolated application. */
  async replay(request: ReplayRequest, agent: (input: JSONValue) => Promise<JSONValue>): Promise<ReplayResponse> {
    if (request.schemaVersion !== 1 || !Array.isArray(request.exchanges) || request.exchanges.length > 10000 || !Number.isFinite(Date.parse(request.clock))) throw new Error('Unsupported replay request.');
    if (Buffer.byteLength(canonical(request)) > 4 * 1048576) throw new Error('Replay request exceeds its byte limit.');
    const c: Context = {owner:this, replay:structuredClone(request), result:{schemaVersion:1, issues:[], operations:[], effects:[], hits:0}, cursor:0, bytes:0, pending:new Set(), closed:false};
    return context.run(c, async () => {
      try { c.result.output = await agent(structuredClone(request.input)); } catch { issue(c, 'agent_error'); }
      c.closed = true;
      if (c.pending.size) issue(c, 'unfinished_operations');
      if (c.cursor !== request.exchanges.length) issue(c, 'unconsumed_exchanges');
      return c.result;
    });
  }
}

/** Explicit HTTP observation, with status, selected headers, text and retrieval time. */
export async function captureHTTP(sdk: AgentReplay, name: string, url: string, init: RequestInit = {}): Promise<JSONValue> {
  const headers = Object.fromEntries(new Headers(init.headers).entries());
  const request: RequestIdentity = {kind:'http', name, version:'1', input:{url, method:init.method ?? 'GET', headers, body:typeof init.body === 'string' ? init.body : null}};
  if (init.body !== undefined && typeof init.body !== 'string') throw new Error('The HTTP capture adapter accepts a string body.');
  return sdk.boundary(request, async () => {
    const response = await fetch(url, {...init, redirect:'error', signal:init.signal ?? AbortSignal.timeout(30000)});
    const reader = response.body?.getReader();
    const chunks: Uint8Array[] = [];
    let bytes = 0;
    if (reader) {
      try {
        for (;;) {
          const part = await reader.read();
          if (part.done) break;
          bytes += part.value.byteLength;
          if (bytes > 262144) throw new Error('HTTP response exceeded the capture limit.');
          chunks.push(part.value);
        }
      } finally { await reader.cancel(); }
    }
    return {status:response.status, headers:{'content-type':response.headers.get('content-type')}, body:Buffer.concat(chunks).toString('utf8'), retrievedAt:sdk.now().toISOString()};
  });
}
