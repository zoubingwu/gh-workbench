# Terminal UI mode research

Research date: 2026-07-28

## Recommendation

Use Bubble Tea v2.0.8 for the first TUI implementation, with
`model.Snapshot` as the shared browser/TUI data contract. The existing
`store` and `syncer` should feed the TUI in process; the loopback HTTP server,
session exchange, and WebSocket hub remain browser-mode infrastructure.

Bubble Tea's command/message/update/view flow maps directly to the workbench's
current snapshot callback. It also owns raw terminal setup,
alternate-screen rendering, resize events, panic cleanup, and terminal
restoration. Its pure model is straightforward to test.

Direct tcell/v2 is the strongest lightweight alternative. It adds less
dependency and notice inventory, and its `SimulationScreen` is excellent for
tests. It also makes gh-workbench responsible for list layout, scrolling,
event routing, redraw scheduling, and every terminal lifecycle edge.

## CLI contract

Use the terminal interface by default and select browser behavior with boolean
options:

```text
gh workbench
gh workbench --browser
gh workbench --browser --no-open
```

- The default command starts the account store, GitHub client, and sync runner,
  then occupies the current terminal with the TUI.
- `--browser` starts the loopback HTTP server and browser interface.
- `--browser --no-open` prints the authenticated local URL for manual opening.
- Using `--no-open` without `--browser` returns a usage error.
- TUI mode requires interactive stdin and stdout. Terminal validation should
  happen before account synchronization starts.

Two boolean flags keep the two-mode contract direct and make the terminal
interface the zero-value path.

## Minimal useful TUI

The first version should stay read-focused and use the data already present in
`model.Snapshot`.

### Screen

- Header: `viewer@host`, repository count, and sync state.
- Filters: all, pull requests, issues, active-account pull requests, and
  inactive-item visibility.
- Repository groups: title, item count, and a two-line work-item summary.
- Work items: author, labels, review state, change size, latest activity,
  reactions, polling error, and updated time.
- Footer: key help plus the latest action result.
- Automatic redraw when a new snapshot arrives and on terminal resize.

### Keys

| Key | Behavior |
| --- | --- |
| `j`, `down` | Select next item |
| `k`, `up` | Select previous item |
| `pgdown`, `pgup` | Move by one visible page |
| `1`, `2`, `3` | Show all items, pull requests, or issues |
| `m` | Toggle items authored by the active account |
| `i` | Toggle inactive items |
| `r` | Call `syncer.Runner.Trigger()` |
| `enter`, `o` | Open the selected GitHub URL with the existing browser launcher |
| `q`, `ctrl+c` | Exit and restore the terminal |

Mouse support, configurable keys, notifications, and broader browser/TUI visual
parity belong to later slices.

## Integration boundary

The current process already has the required in-process seams:

- [`internal/store.Store.Snapshot`](../internal/store/store.go) produces
  `model.Snapshot`.
- [`internal/syncer.Runner`](../internal/syncer/runner.go) exposes `Trigger`,
  `Running`, and an `onUpdate` callback.
- [`internal/app.Run`](../internal/app/app.go) already coalesces runner updates
  through a capacity-one channel before publishing browser snapshots.

Recommended flow:

```text
GitHub -> syncer.Runner -> store.Store
                  |
                  v onUpdate
          coalesced update channel
                  |
                  v
        store.Store.Snapshot(...)
                  |
                  v
      Bubble Tea snapshot command
                  |
                  v
              Update -> View
```

`internal/app` should keep ownership of authentication, store lifetime,
runner lifetime, signals, and cancellation. A new `internal/tui` package
should receive snapshots and a narrow refresh callback. It should depend on
`internal/model`, with concrete `store` and `syncer` wiring staying in
`internal/app`.

The browser path continues to create the listener and `server.Server`. The TUI
path can skip the listener, embedded web assets, session token, bootstrap
request, cookie exchange, WebSocket client, and reconnection logic.

For shutdown, run the TUI and sync runner under one derived context. TUI exit
cancels the runner; runner failure closes the TUI with a visible error.
Bubble Tea should receive the context and retain its default panic cleanup.
`ctrl+c` is also handled as a TUI key so raw-mode input exits cleanly.

### Local WebSocket boundary

The current WebSocket endpoint requires the process-specific session cookie
and same-origin checks. An in-process TUI client would need to reproduce the
session redirect, cookie jar, Origin header, bootstrap request, sync POST, and
reconnect loop. That transport adds a second protocol client inside the same
process while `store.Snapshot` already provides the typed data.

## Option comparison

| Option | Go and license | Net new module paths versus current `go.mod` | Async and terminal model | Testability | Fit |
| --- | --- | ---: | --- | --- | --- |
| Bubble Tea v2.0.8 | Go 1.25.0, MIT | 15: Bubble Tea plus 14 new requirements | Elm-style messages and commands; `Program.Send` accepts runner updates; default quit, signal, panic, and alternate-screen cleanup | Direct `Update`/`View` tests; injectable input, output, window size, renderer, and signals | Recommended balance of lifecycle safety and maintainable UI code |
| tcell/v2 v2.13.10 | Go 1.24.0, Apache-2.0 | 4 in the module graph | Application owns `PollEvent`, redraw scheduling, `Fini`, and context wakeups | `SimulationScreen` supports event injection, resize, and screen inspection | Best dependency-conscious alternative for a small read-only list |
| tview v0.42.0 | Go 1.18, MIT | 4 | Mutable widget tree; background updates enter the event loop through `QueueUpdateDraw`; `Stop` calls `Screen.Fini` | Accepts a tcell `SimulationScreen` | Fast widget-heavy development; stable tag remains v0 and pins tcell/v2 v2.8.1 |
| Existing termenv + x/term | Go 1.17 and current project Go baseline; MIT/BSD | 0 | Application implements raw input decoding, resize signals, screen diffing, alternate screen, and restoration | Rendering helpers accept writers; terminal behavior needs custom fakes or a PTY | Smallest dependency graph and largest terminal-maintenance surface |

The dependency counts compare module paths from each candidate's official
`go.mod` with the paths already present in this repository. Existing shared
modules such as `go-colorful`, `uniseg`, `x/sys`, `x/term`, and `x/text` are
counted once.

Bubble Tea core is sufficient for this MVP. Adding Bubbles or Lip Gloss would
expand the graph and should follow a concrete need for list, viewport, or
styling components.

## `THIRD_PARTY_NOTICES.md` cost

The release script ships
[`THIRD_PARTY_NOTICES.md`](../THIRD_PARTY_NOTICES.md) with every release, so a
TUI dependency change includes a notice update:

- Bubble Tea has the highest inventory cost: add Bubble Tea and review the 14
  newly introduced requirement paths. The final component list should come
  from the built release binary; upstream test-only requirements stay outside
  that inventory.
- tcell/v2 and tview each add four module paths before final binary pruning.
  Their license texts are Apache-2.0 and MIT respectively; transitive
  components still need individual component/version entries.
- termenv and `golang.org/x/term` already appear in the current dependency and
  notice inventory.

Use `go version -m <release-binary>` for each release target as the linked
module inventory, then preserve every linked component's license or notice
text. This keeps the notice file aligned with the executable described by its
introduction.

## Implementation validation

- Unit-test CLI parsing, defaults, replaced flags, and the requirement that
  `--no-open` accompanies `--browser`.
- Unit-test selection, paging, resize, snapshot replacement, sync state,
  error display, and key actions by calling the Bubble Tea model directly.
- Assert that selection remains stable by repository, number, and kind across
  reordered snapshots.
- Verify `q`, `ctrl+c`, context cancellation, and terminal control-sequence
  sanitization. Bubble Tea's upstream suite covers its panic-cleanup behavior.
- Run `make check` and `make build`, then keep the existing release target
  matrix as the cross-platform compile check.

## Primary sources

- Bubble Tea
  [v2.0.8 release](https://github.com/charmbracelet/bubbletea/releases/tag/v2.0.8),
  [`go.mod`](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/go.mod),
  [runtime and terminal lifecycle](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/tea.go),
  [program options](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/options.go),
  [commands](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/commands.go),
  [MIT license](https://github.com/charmbracelet/bubbletea/blob/v2.0.8/LICENSE)
- tcell/v2
  [v2.13.10 release](https://github.com/gdamore/tcell/releases/tag/v2.13.10),
  [`go.mod`](https://github.com/gdamore/tcell/blob/v2.13.10/go.mod),
  [`Screen` and `SimulationScreen` API](https://pkg.go.dev/github.com/gdamore/tcell/v2@v2.13.10),
  [Apache-2.0 license](https://github.com/gdamore/tcell/blob/v2.13.10/LICENSE)
- tview
  [v0.42.0 release](https://github.com/rivo/tview/releases/tag/v0.42.0),
  [`go.mod`](https://github.com/rivo/tview/blob/v0.42.0/go.mod),
  [`Application` event loop and cleanup](https://github.com/rivo/tview/blob/v0.42.0/application.go),
  [MIT license](https://github.com/rivo/tview/blob/v0.42.0/LICENSE.txt)
- termenv
  [v0.16.0 release](https://github.com/muesli/termenv/releases/tag/v0.16.0),
  [`go.mod`](https://github.com/muesli/termenv/blob/v0.16.0/go.mod),
  [output and styling API](https://github.com/muesli/termenv/blob/v0.16.0/output.go),
  [MIT license](https://github.com/muesli/termenv/blob/v0.16.0/LICENSE)
- `golang.org/x/term`
  [terminal API](https://pkg.go.dev/golang.org/x/term),
  [BSD-style license](https://github.com/golang/term/blob/master/LICENSE)
