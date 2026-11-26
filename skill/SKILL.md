---
name: browser-ctl
description: Drive the user's own Chrome (their signed-in sites, their profile) from any editor or agent that has no MCP support, by shelling out to the browser-ctl CLI. Use when the task needs the browser — navigate, click, fill forms, read the page, take screenshots — and the editor cannot talk MCP natively.
commands:
  - name: browser-ctl
    description: Drive the user's Chrome by bare name (run, tabs, status, fill-login). The binary lives in the bin/ of this skill's parent folder.
    vault: true
---

# browser-ctl

`browser-ctl` is a standalone, statically linked CLI that drives the user's Chrome through the
**Browser MCP** Chrome extension. It speaks the extension's own relay protocol (WebSocket on
`127.0.0.1`) directly — **no MCP server, no Node runtime**. Any editor, agent or script can drive
the browser by running the binary as a subprocess and reading its stdout.

## Invoking in Lilith: bare name, always

On a Lilith host, this skill's command is registered by name: invoke it
exactly as `browser-ctl ...`. Lilith resolves the bare name directly to
the binary in this skill's `bin/` and runs it — no path is needed or
wanted. This is a hard rule, not a style preference:

- NEVER invoke it by path (`./bin/browser-ctl`, `C:\...\browser-ctl.exe`).
- NEVER `cd` into the skill folder (or anywhere) to run it.
- NEVER probe for it first with `where`, `Get-Command`, `ls` or `test -f`.
- Do NOT use the `scripts/browserctl.*` wrappers on a Lilith host; they
  exist only for hosts without skill-command resolution.

If `browser-ctl` fails to resolve, do not hunt for the file: the usual
cause is that `bin/` has no binary for the current platform. Follow the
next section.

## If the binary is missing for this platform: compile it

Lilith indexes the executables in the `bin/` of the folder that contains
this `skill/` folder. If that `bin/` holds no binary for the current
platform — only the other platform's (e.g. just `browser-ctl.exe` while
you are on Linux), or none at all — build it from this repository's
sources and drop it in that same `bin/`, named for the platform:

- Windows → `browser-ctl.exe`
- Linux / macOS → `browser-ctl`

From the root of the checkout (the folder that contains `skill/`):

```sh
# Windows
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/browser-ctl.exe ./cmd/browser-ctl
# Linux / macOS
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/browser-ctl ./cmd/browser-ctl
```

Then retry the command by its bare name — Lilith picks the new binary up
from `bin/`. If Go is not installed or the repository sources are not at
hand, tell the user and stop: do not search the disk for a browser-ctl
binary, and do not download one from anywhere else.

## When to use

- The agent must operate a real browser (the user's Chrome: their logins, their profile).
- The host editor/agent has no MCP client, or MCP is disabled.
- You need deterministic, scriptable browser steps (`goto`, `click`, `fill`, `snapshot`, …).

Do **not** use it for headless scraping you can do with `curl`/HTTP, or when the editor already
has a working MCP `browser` tool (then prefer that).

## Prerequisites (one-time)

1. **Chrome/Edge/Brave** (Chromium; Firefox has no `debugger` API).
2. **The extension** loaded in Chrome:
   - `chrome://extensions` → enable **Developer mode** → **Load unpacked** → pick
     `apps/extension/.output/chrome-mv3` (build it first with `pnpm build:extension`), **or**
     install the Chrome Web Store "Browser MCP" extension.
3. **The CLI** binary, statically linked:
   ```sh
   CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/browser-ctl ./cmd/browser-ctl
   ```
   (On Windows: `bin\browser-ctl.exe`.)

## How a call works

Each `browser-ctl run` invocation is self-contained:

1. The CLI starts its relay on the first free port of `9010, 9011, 9012` (or `--port N`).
2. The extension (already running in Chrome) probes those ports and connects to the relay.
3. The CLI runs the commands against the browser and prints the result to **stdout**.
4. The process exits. The relay's identity key is persisted in `~/.browser-ctl/identity.json`,
   so the extension's **Allow** survives restarts.

First use: the extension's popup asks to **Allow** the relay. Approve it once.

## The contract (what the agent does)

Run the binary; one line per command; stop at the first error; read stdout.

```sh
browser-ctl run "goto https://example.com
snapshot"
```

Exit code `0` = success (stdout has the output), `1` = failure (stderr has the error).

### Commands

| Command | Syntax | Returns |
|---|---|---|
| `goto` | `goto <url>` | `→ <url>` |
| `back` / `forward` / `reload` | no args | note |
| `click` | `click <target> [--double] [--button left\|right] [--modifiers Shift,Control]` | ✓ |
| `hover` | `hover <target>` | ✓ |
| `scroll` | `scroll top\|bottom\|<dir> [<px>]` | position note |
| `fill` | `fill <target> "<text>" [--submit]` | ✓ |
| `type` | `type "<text>" [--submit]` | ✓ |
| `press` | `press <key>` | ✓ |
| `select` | `select <target> "<value>"…` | ✓ |
| `check` / `uncheck` | `check <target>` | ✓ |
| `wait` | `wait <ms> \| wait --text "<t>" \| wait --url <url>` | ✓ (10 s timeout) |
| `snapshot` | `snapshot` | element tree with `e#` refs |
| `find` | `find "<text>"` | matching lines |
| `get` | `get text\|value\|count <target> \| get url \| get title` | value |
| `screenshot` | `screenshot [--path <file>]` | ASCII preview or saved file |
| `eval` | `eval "<js>"` | JSON result |
| `tab` | `tab list\|new [url]\|select <id>\|close [id]` | tab list / id |
| `help` | `help [command]` | command list |

## Signing in with a credential vault (Lilith hosts)

`browser-ctl fill-login <domain>` fills the login form of the **active page**
with a credential it receives **only on stdin**, as JSON
`{"username","password"}`. On a Lilith host with the experimental web vault
enabled, the agent just runs the command by its bare name — no paths:

```sh
browser-ctl fill-login youtube.com
# => login filled for youtube.com
```

Lilith looks the domain up in its encrypted vault (asking the user to type
the credential in a local secure prompt if it is missing) and attaches the
payload to the process's stdin. The values never appear in arguments, in
the command output or in any log, and the agent never sees them. The
command refuses to fill when the active page's host is not the requested
domain or a subdomain of it, and fails explicitly when no payload arrives
on stdin. Do not pass credentials as arguments, ever.

### Targets

- `e12` — a ref from the last `snapshot` (preferred; stable within a page).
- `getByRole('button', {name:'Go'})`, `getByText('...')`, `getByLabel('...')` — locators.
- CSS selectors: `#id`, `.class`, `a[href]`.

### Speed rules

- Prefer `goto <url>` over click-through when the destination is known.
- Reuse the current tab; don't re-navigate if the page is already there.
- `snapshot` is the expensive step — take it once after a page change, reuse the refs.
- Use `wait` only when content is still loading or an interaction hasn't settled.
- Finish in the fewest browser actions that still preserve correctness.

## Helper scripts

`scripts/browserctl.ps1` (Windows) and `scripts/browserctl.sh` (Unix) wrap the binary with a
stable path and a `--json` flag that wraps the result as JSON for programmatic hosts:

```sh
scripts/browserctl.sh run "goto https://example.com
get title" --json
# => {"ok":true,"output":"..."}   or   {"ok":false,"error":"..."}
```

## Troubleshooting

- **"No browser is connected"** — the extension isn't installed/enabled, or Chrome is closed.
- **"not paired: click Allow…"** — open the extension's popup and click **Allow** once.
- **"the browser is paused"** — the user pressed **Stop** in the popup; ask them to **Resume**.
- **Port in use** — another relay is running; the CLI picks the next free port automatically.
