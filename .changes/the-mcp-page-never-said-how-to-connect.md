# added

How to connect a client to the MCP server.

`af mcp` was documented in full: the division of authority, every tool, the
verdicts, the errors, what `project_id` asserts and where output goes. The page
opened by saying the server is started by an MCP client rather than typed by a
person, and then never showed a client configuration. There was no `mcpServers`
block and no `claude mcp add` line anywhere in the repository, the docs or the
README, so a reader who wanted the thing the page described had nowhere to go.

There is now a verified configuration for Claude Code, Codex CLI, Gemini CLI,
Cursor, Windsurf, VS Code, Cline, Continue, Claude Desktop, the ChatGPT desktop
app, JetBrains AI Assistant and Zed. The page uses each client's current file,
schema and working directory support rather than presenting one JSON shape as
universal.

It also names the setting people get wrong. The server binds the directory the
client starts it in. The page uses `cwd` or a Working directory field where the
client supports one, and an absolute path passed with `-C` where it does not.
Without either, the failure reads as a missing manifest rather than as a
missing setting.

It states the current remote boundary without making it permanent: v1.1.1 has
no hosted Antifailure MCP endpoint yet. Browser clients cannot start the local
process. An authenticated Streamable HTTP bridge is possible, but it operates
containers, a checkout and production shaped data, so it needs deliberate
identity, network and tenant isolation. The Supergateway example now selects
Streamable HTTP explicitly and names its `/mcp` endpoint instead of silently
starting the default SSE transport.
