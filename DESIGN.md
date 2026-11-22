# browser-ctl v2 — from scratch, no Node, extension unchanged

Goal: a single statically-linked Go binary that does everything the JS
Browser MCP system does, for CLIs/editors/agents that do not speak MCP,
and that *also* speaks MCP itself (over stdio) so it can be shared as an
MCP server — without Node, without any JS runtime in the hot path.

The Chrome extension source is NOT changed. This binary speaks the exact
wire protocol the extension already speaks (see `internal/protocol`).

## Modes (one binary)

```
browser-ctl daemon [--port N] [--stderr]   long-lived daemon (owns the relay)
browser-ctl mcp [--caps cdp] [--port N]    MCP server over stdio (thin client)
browser-ctl run "goto https://..."         CLI: one batch through the daemon
browser-ctl run --commands-file cmds.txt
browser-ctl tabs                            CLI: list tabs through the daemon
browser-ctl status                          CLI: daemon identity / ports
browser-ctl serve [--port N] [--no-wait]    legacy: relay only, wait for browser
```

## Architecture

```
MCP app (Claude Code, Cursor, ...)     CLI / editor without MCP
  │ MCP over stdio                        │ daemon /client WS
  ▼                                       ▼
browser-ctl mcp  ──►  browser-ctl daemon (one process, in-memory sessions)
                        │  relay: HTTP+WS on 127.0.0.1:9010-9012
                        │  /browsermcp/status  /extension  /client  /shutdown
                        ▼
                   Chrome extension (unchanged)
                        ▼
                   user's Chrome (debugger/CDP)
```

Daemon state (in-memory only, like the JS daemon):
- `identity.json` (0600, ECDSA P-256, survives restarts) in `~/.browser-ctl`
  (JS used `~/.browsermcp`; v2 uses its own home so both can coexist).
- `daemon.json` (0600: port, pid, version, per-boot token) — how clients
  find/reuse the daemon. `daemon.starting.json` race guard.
- Sessions: `map[sessionID]*Session` — app, capabilities, current tab,
  opened tabs, pending dragged-in tabs, last call time. Idle exit 10 min
  after the last session leaves.
- Backend: tab registry with public tab IDs (Chrome ID unless colliding,
  then spare ≥100001, kept forever after close), one slot per browser
  profile (max 8), default = last-focused usable browser.

## Protocol (extension ↔ daemon) — unchanged from JS

- `GET /browsermcp/status` (no auth), `WS /extension` (Origin
  `chrome-extension://<32-char id>` in allow-list), loopback only,
  Host must be 127.0.0.1/localhost/[::1].
- hello/welcome with ECDSA P-256 signature over
  `browsermcp-pair-v1:<port>:<challenge>:<commitment>`, pairing
  commitment = SHA-256 of fresh daemon nonce, 6-digit code from
  `browsermcp-pair-code-v1:<pubkey>:<extNonce>:<daemonNonce>`.
- This fork's extension auto-pairs (`isPairedKey` always true), so the
  daemon must accept a hello whose state already says `paired` and must
  also handle `pairingNonce` if an extension ever sends one.
- Ping every 20 s, 10 s pong timeout. Close codes 4001/4002/4003.

## Engine (commands) — the single `browser` tool

One MCP tool `browser`, input `{commands: string}` (one command per line).
Pre-validate every line before running any; stop at first error.
Output: `✓ <echo> — <notes>` per step, read output inline,
`✗ step N: <echo> — <message>`, `Page: <url> — "<title>"` trailer.

Commands (JS parity target, implemented in milestones):
goto, back, forward, reload, click, hover, drag, scroll, fill, type,
press, select, check, uncheck, upload, wait, dialog, tab, browser,
snapshot, find, get, screenshot, pdf, download, eval, console,
requests, request, cdp, help.

Key semantics that the v1 Go port got wrong and v2 must keep:
- `goto` navigates the session's current tab; creates a tab only when
  the session has none. It never searches by URL. (A stateless CLI has
  no session, so it gets a short-lived session from the daemon instead
  of inventing URL matching.)
- `tab close` only closes tabs the session opened.
- `snapshot` uses the accessibility tree (`Accessibility.getFullAXTree`
  via CDP — the raw-CDP equivalent of Playwright's `ariaSnapshotJSON`),
  refs `e12` / `f2e12`, `-i` interactive-only, `--depth`, frame-prefix
  stripping so stale refs fail cleanly.
- Targets are parsed with a whitelist (getByRole/getByLabel/getByText/
  getByPlaceholder/getByTestId/locator/frameLocator/filter/first/last/
  nth), never evaluated as code.
- Hidden tabs: `Emulation.setFocusEmulationEnabled` on claim, off on
  release; `ensureVisible` before mouse/keyboard/screenshot.
- Dialogs block steps except `whileDialog` ones; answering resumes the
  interrupted step (≤10 s) in the same call.
- Output caps: reads 10k chars default / 75k max, whole response 80k,
  3 images per response, downloads ≤25 MB.
- CDP: two sessions per tab (engine-internal + agent `--caps cdp`),
  full domain RESETS cleanup on release, denied methods (cookies,
  `Target.*`, `Network.loadNetworkResource`, storage domains).

## Milestones

1. Daemon + sessions + daemon.json discovery + relay (reuse protocol).
2. MCP server over stdio + reverse file RPC (readFile/writeFile).
3. CLI client (`run`/`tabs`/`status`) through the daemon.
4. Snapshot (AX tree), targets whitelist, actions (actionability, settle).
5. Missing commands: dialog, console, requests, request, upload,
   download, pdf, cdp.

## Testing

- Unit tests for parser, targets, snapshot rendering, URL handling,
  pairing code, daemon.json.
- Fake-extension integration test: a WS server speaking the extension
  protocol (hello/state/cdpEvent) so the daemon + engine can be tested
  without Chrome.
- Live tests on the Dell (real Chrome + extension) for each milestone.
