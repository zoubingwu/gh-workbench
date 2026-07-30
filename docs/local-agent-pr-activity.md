# Local coding-agent activity on pull requests

Research date: 2026-07-28

## Recommendation

Build this as a local-only, read-only signal with two confidence levels.

- Claude Code can provide a supported zero-configuration signal through
  `claude agents --json`. Background sessions expose explicit
  `working`, `blocked`, and terminal states.
- Codex can provide exact state through the supported app-server event stream
  when gh-workbench owns or shares that transport. A separately launched
  observer cannot attach to the Desktop app's default stdio transport.
- Codex Desktop can still support a conservative experimental signal by
  reading its state database and lifecycle-only JSONL records. That adapter
  must be version-gated and fail closed when the schema changes.

The first slice should ship Claude Code as the supported provider and Codex as
an explicitly heuristic provider. A solid spinner can represent supported
`working` state; a softer pulse with a tooltip such as `Codex activity observed
locally` can represent heuristic state.

This feature requires no hooks, plugins, settings edits, shell wrappers, or
changes to how either agent is launched.

## Capability matrix

| Product and signal | Setup | Coverage | Confidence |
| --- | --- | --- | --- |
| Claude Code `claude agents --json` | None after installing Claude Code v2.1.145+ | Every live session, plus working or blocked background sessions whose process exited | Supported API; high for documented background `state` |
| Codex app-server `thread/status/changed` and `turn/*` | A shared app-server transport | Exact loaded-thread and turn lifecycle | Supported API; high |
| Codex `state_5.sqlite` plus rollout lifecycle records | None | Current local CLI/Desktop sessions persisted in the default Codex data directory | Implementation detail; heuristic |
| Process enumeration alone | None | Product process presence | Too coarse for a work indicator |
| Claude or Codex transcript contents | None | Rich activity details | High privacy cost and unnecessary for this feature |

## Claude Code

Claude Code Agent View already implements the product idea internally: its
rows animate while a session is working and can display a linked pull request.
The documented read-only shell interface is:

```text
claude agents --json
```

The command prints a JSON array and exits. Each entry always contains `cwd`,
`kind`, and `startedAt`. Background entries contain `state`, whose documented
values are `working`, `blocked`, `done`, `failed`, and `stopped`. A live
process also includes `pid` and `status`; `waitingFor` explains a documented
`waiting` status. Agent View itself defines Working as active tool execution
or response generation.

Recommended mapping:

| Claude field | gh-workbench state |
| --- | --- |
| background `state == "working"` | `working`, supported confidence |
| background `state == "blocked"` | `needs_input`, supported confidence |
| background terminal state | Remove the live indicator |
| interactive `status == "waiting"` | `needs_input`, supported confidence |
| other interactive `status` values | Parse through a versioned compatibility layer and enable after fixtures from an installed release confirm their semantics |

`claude agents --json` arrived in Claude Code 2.1.145. Agent View remains a
research-preview feature, so the adapter should version-check the binary,
accept additive JSON fields, and treat malformed or unsupported output as
temporarily unavailable.

Claude documents that opening Agent View can start its per-user supervisor.
The live smoke test should record whether the `--json` form also initializes
that supervisor on a fresh install. This would be a transient runtime side
effect rather than a hook, plugin, settings edit, or launch wrapper.

For a strictly passive mode, Claude also documents `~/.claude/sessions/` as
one small file per running session, created for concurrent-session and crash
detection. That directory can provide a liveness guard without launching a
command. Its file schema and working-versus-idle semantics are undocumented,
so it is insufficient by itself for a high-confidence loading indicator. Until
the live smoke test closes the supervisor question, the conservative policy is
to invoke `agents --json` only when the supervisor is already running and keep
other Claude observations heuristic or unavailable.

Agent View's own pull-request link is richer than the documented JSON schema.
It observes `gh` output, `gh pr checkout`, or a pushed branch followed by
`gh pr view`. The JSON field table currently exposes no pull-request identity,
so gh-workbench should perform its own repository-and-branch mapping.

Reading `~/.claude/jobs/<id>/state.json` would duplicate an internal storage
contract. Reading `~/.claude/projects/.../*.jsonl` would also cross a privacy
boundary: Anthropic documents those files as full plaintext transcripts
containing messages, tool calls, tool results, and potentially credentials.
The CLI JSON command supplies the needed state without transcript access and
also respects a custom `CLAUDE_CONFIG_DIR`.

## Codex

### Supported integration

Codex app-server exposes the exact lifecycle needed for this feature:

- `thread/status/changed` reports `active`, `idle`, `systemError`, and
  `notLoaded`.
- `turn/started` begins a unit of work.
- `turn/completed` ends it as completed, interrupted, or failed.
- thread metadata includes `cwd` and persisted `gitInfo`.

This interface works when gh-workbench is connected to the same app-server
transport as the active client. The default app-server transport is stdio, so
the parent client owns both ends of the stream. Starting another app-server
process can list persisted threads, while their runtime status belongs to the
new process and starts as `notLoaded`. Exact observation of an already-running
Codex Desktop stdio process therefore requires cooperation from that client or
a shared socket configured at launch.

Those integration shapes conflict with the zero-setup requirement. They remain
the preferred long-term path if Codex later exposes a documented read-only
local status endpoint.

### Zero-setup heuristic

Current Codex builds maintain:

- `~/.codex/state_5.sqlite`, whose thread rows include `cwd`, rollout path, Git
  branch, Git origin URL, commit SHA, timestamps, source, and CLI version;
- rollout JSONL files containing session metadata and lifecycle events such as
  `task_started`, `task_complete`, and `turn_aborted`.

The state database contains no live turn-status column. A conservative adapter
can combine its metadata with lifecycle-only records:

1. Open the database read-only and select only thread ID, rollout path, cwd,
   Git metadata, timestamps, and CLI version.
2. Tail only records whose top-level type is session metadata or lifecycle
   `event_msg`.
3. Track active turn IDs after `task_started`.
4. Clear each turn after its matching `task_complete` or `turn_aborted`.
5. Require a known schema version and a live Codex process or recent lifecycle
   progress before publishing heuristic activity.
6. Drop the observation on parse errors, permission errors, stale data, or an
   unknown version.

The parser should use a strict field allowlist and avoid decoding, logging, or
retaining prompts, reasoning, assistant output, tool arguments, and tool
results. A set of active turn IDs handles concurrent subagents and overlapping
turns more accurately than one boolean per thread.

Crash termination can leave a final `task_started` without a terminal record.
Long model and tool calls can also leave the JSONL unchanged for a while.
Process existence and file freshness improve the estimate, while neither
provides proof of active work. The UI should carry the heuristic confidence
through to its icon and tooltip.

## Mapping a local session to a pull request

Use Git identity as the join key. Directory names alone are ambiguous across
clones, worktrees, forks, and GitHub Enterprise hosts.

For each observed `cwd`:

1. Resolve the worktree root with `git -C <cwd> rev-parse --show-toplevel`.
2. Resolve the checked-out branch and HEAD SHA.
3. Resolve the branch's upstream remote when present, then normalize SSH and
   HTTPS remote URLs into `(host, owner, repository)`.
4. Match open pull requests by head repository plus `headRefName`.
5. Use `headRefOid` as a fallback for detached HEAD and renamed local branches.
6. Recompute identity when the observer reports a new cwd/state or Git HEAD
   changes.

Fork pull requests need the head repository, since the base repository can
differ from the local branch's upstream remote. Exact
`head repository + head branch` matches are safe to aggregate. Ambiguous
matches should produce no indicator.

The current `model.WorkItem` and `relevantItemsQuery` store the base repository
and PR number but omit `headRefName`, `headRefOid`, and head repository.
Implementation therefore needs to add those fields to GitHub discovery,
SQLite, and the snapshot contract. This is a narrow data-contract addition;
it avoids one GitHub request per locally active session.

Multiple sessions can map to one PR. Aggregate them into one state:

- show a spinner while any supported session is working;
- show a count in the tooltip when two or more sessions are attached;
- show needs-input state when none are working and at least one is blocked;
- show heuristic activity separately from supported activity;
- clear the indicator as soon as every observation reaches a terminal or stale
  state.

The indicator belongs only in the loopback browser and TUI snapshot. It should
never update the pull request, post a comment, or send local agent metadata to
GitHub.

## Minimal implementation boundary

Add one local observer package behind a narrow interface:

```go
type Observation struct {
    Provider   string
    SessionKey string
    CWD        string
    State      string
    Confidence string
    ObservedAt time.Time
}
```

`SessionKey` should be opaque in memory and absent from browser payloads.
Prompt text, titles, summaries, tool names, and result text have no place in
this contract.

Recommended components:

1. A Claude adapter executes the resolved `claude` binary with
   `agents --json` every 2–5 seconds under a short timeout and parses a field
   allowlist.
2. A Codex adapter opens known local storage read-only, tails lifecycle records,
   and emits heuristic observations.
3. A Git resolver caches cwd-to-head identity and invalidates it when HEAD or
   upstream changes.
4. An aggregator joins observations to PR head metadata and publishes only the
   derived provider, state, confidence, and count.
5. Browser and TUI render the same snapshot fields.

Use direct subprocess arguments without a shell. A missing binary, unsupported
version, timeout, malformed JSON, inaccessible file, or unknown schema yields
an unavailable provider and clears its stale indicator.

## Local validation

Metadata-only inspection on this machine found:

- Codex CLI 0.145.0 and a Codex Desktop app-server build identifying as
  0.146.0-alpha.3.1.
- The Desktop app-server runs with the default stdio transport.
- `state_5.sqlite` has cwd, Git, rollout-path, and version metadata and has no
  live status column.
- Current rollout files contain the expected start, completion, and abort
  lifecycle records alongside sensitive conversation and tool content.
- Long-lived Codex processes and open rollout file descriptors outlive
  individual turns, confirming that process/file-open checks are presence
  signals.
- Claude Code is absent from the current executable path, so the Claude JSON
  adapter still needs fixture capture, subprocess-cost measurement, supervisor
  behavior verification, and a live smoke test on Claude Code 2.1.145 or
  newer.

No prompt, assistant output, reasoning text, tool argument, or tool result was
used for this inspection.

## Primary sources

- OpenAI:
  [Codex app-server overview and transports](https://learn.chatgpt.com/docs/app-server),
  [thread status and stored Git metadata](https://learn.chatgpt.com/docs/app-server#track-thread-status-changes),
  [turn lifecycle events](https://learn.chatgpt.com/docs/app-server#events),
  [Codex protocol lifecycle definitions](https://github.com/openai/codex/blob/main/codex-rs/protocol/src/protocol.rs)
- Anthropic:
  [Agent View states, PR linking, and `claude agents --json`](https://code.claude.com/docs/en/agent-view),
  [running-session markers](https://code.claude.com/docs/en/claude-directory#application-data),
  [Claude Code application data and plaintext transcript boundary](https://code.claude.com/docs/en/claude-directory#application-data),
  [`claude agents --json` 2.1.145 release note](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md)
- GitHub:
  [GraphQL `PullRequest` fields](https://docs.github.com/en/graphql/reference/objects#pullrequest)
