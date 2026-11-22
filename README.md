# browser-ctl

Drive the user's own Chrome (their signed-in sites, their profile) from **any editor or agent —
even ones without MCP support** — by shelling out to a small, statically linked CLI.

`browser-ctl` is a Go CLI that speaks the [Browser MCP](https://browsermcp.io) extension's relay
protocol (WebSocket on `127.0.0.1`) directly. No MCP server, no Node runtime in the hot path: the
agent runs the binary as a subprocess and reads stdout.

## Layout

| Path | What it is |
|---|---|
| `cmd/browser-ctl` | The CLI entrypoint (Go). |
| `internal/relay` | The relay: HTTP/WS server, the `hello`/`welcome` handshake, ECDSA P-256 signing, the identity key. |
| `internal/cdp` | A thin CDP client over the extension's relay. |
| `internal/cmd` | The command engine (`goto`, `click`, `fill`, `snapshot`, …). |
| `apps/extension` | The Chrome extension (WXT), built from source. |
| `packages/extension-core` | The extension's browser control (a thin `chrome.debugger` relay). |
| `packages/protocol` | The shared wire protocol (frames, pairing, ports). |
| `skill/` | The **skill** for editors/agents without MCP: `SKILL.md` + helper scripts. |

## Build

**CLI** (static, no CGO):

```sh
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/browser-ctl ./cmd/browser-ctl
```

**Extension** (needs Node 24+ and pnpm):

```sh
pnpm install
pnpm build:extension
# -> apps/extension/.output/chrome-mv3
```

## Use

1. Load the extension in Chrome: `chrome://extensions` → Developer mode → **Load unpacked** →
   `apps/extension/.output/chrome-mv3` (or install the Chrome Web Store "Browser MCP").
2. Run commands:

   ```sh
   browser-ctl run "goto https://example.com
   snapshot
   click getByRole('link', {name:'More'})"
   ```

   First use: approve the relay in the extension's popup (**Allow**). The identity is kept in
   `~/.browser-ctl/identity.json`, so the approval survives restarts.

The relay listens on the first free port of `9010, 9011, 9012` (`--port N` to pin one).

## The skill

`skill/SKILL.md` is a drop-in skill for agents/editors that can't speak MCP: it documents the
CLI contract and ships `scripts/browserctl.{ps1,sh}` wrappers (with a `--json` mode) so any host
can drive the browser by running a subprocess.
