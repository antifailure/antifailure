# changed

The console, website and docs take Next.js 16.3.8, the Tiptap editor 3.31.4,
Starlight 0.42.5 and sharp 0.35.5, and the Go modules take patch releases of
the Docker client, OpenTelemetry 1.47.0 and modernc SQLite 1.60.1. None of
these changes behaviour anybody can see. They are recorded because three of
the four automatic update requests that carried them could not merge on their
own: two changed licensed dependencies without regenerating
THIRD_PARTY_NOTICES.md, and two edited the same lines of one workspace
manifest.
