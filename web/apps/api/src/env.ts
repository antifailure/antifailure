// The per-request values middleware puts on the context.
//
// Its own module because two files need the same type and neither should own
// it: server.ts creates the app, console/index.ts is handed the same app, and a
// Hono whose Variables differ from the one it was given is a type error that
// reads as though the console is at fault.
export type ApiEnv = { Variables: { requestId: string } }
