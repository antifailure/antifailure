# added

How to connect a client to the MCP server.

`af mcp` was documented in full: the division of authority, every tool, the
verdicts, the errors, what `project_id` asserts and where output goes. The page
opened by saying the server is started by an MCP client rather than typed by a
person, and then never showed a client configuration. There was no `mcpServers`
block and no `claude mcp add` line anywhere in the repository, the docs or the
README, so a reader who wanted the thing the page described had nowhere to go.

There is now a configuration for Claude Code, Codex CLI, Gemini CLI, and for
the clients configured by JSON: Cursor, Windsurf, VS Code, Cline, Continue,
Claude Desktop, the JetBrains AI Assistant, and Zed, whose shape is different
enough to get its own block.

It also names the setting people get wrong. A client configured by JSON usually
has nowhere to set a working directory, so without `-C` the server binds
whatever directory the client launched from, which is the client's own
installation and never the project. That failure reads as a missing manifest
rather than as a missing setting.

And it says, in the page rather than only in the operator portal, that there is
no hosted MCP endpoint and why. claude.ai and chatgpt.com connect only to a
remote server on an HTTPS URL, and this one speaks standard input and output to
a process the client started. A gateway bridges it, and the page states the
cost in the same breath: the server starts containers, restores production
shaped data into them and reads the checkout, so a tunnel to it with nothing in
front is remote code execution offered to the internet. Self hosting is the
only shape there is, and `af mcp` is already it.
