import { useEffect, useMemo, useState } from "react";
import { filterItems, type ItemFilter } from "./filterItems";
import { formatAbsoluteTime, formatRelativeTime, reactionSymbol } from "./format";
import type { NotificationPreferences, Reaction, Snapshot, SnapshotEvent, WorkItem } from "./types";
import {
  filterByActivity,
  filterByPullRequestAuthor,
  groupByRepository,
  isInactive,
  workItemStatus,
} from "./workItems";

type ConnectionState = "connecting" | "connected" | "disconnected";

const SHOW_INACTIVE_STORAGE_KEY = "gh-workbench:show-inactive:v1";
const LABEL_COLOR_PATTERN = /^#?([0-9a-f]{6})$/i;
const activityVerbs: Readonly<Record<string, string>> = {
  comment: "commented",
  review_comment: "left a review comment",
  review_approved: "approved",
  review_changes_requested: "requested changes",
  review_commented: "reviewed",
  review_dismissed: "dismissed a review",
  labeled: "labeled",
  unlabeled: "removed label",
  reopened: "reopened",
  review_requested: "requested review",
  review_request_removed: "removed review request",
  ready_for_review: "marked ready for review",
  converted_to_draft: "converted to draft",
};

const filterLabels: Readonly<Record<ItemFilter, string>> = {
  all: "All",
  pull_request: "Pull requests",
  issue: "Issues",
};

async function fetchSnapshot(signal?: AbortSignal): Promise<Snapshot> {
  const response = await fetch("/api/bootstrap", {
    credentials: "same-origin",
    headers: { Accept: "application/json" },
    signal,
  });

  if (!response.ok) {
    throw new Error(`Bootstrap failed with HTTP ${response.status}`);
  }

  return (await response.json()) as Snapshot;
}

function websocketURL(): string {
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/api/events`;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Unexpected request error";
}

function activityVerb(kind: string): string {
  return activityVerbs[kind] ?? "updated";
}

function groupedReactions(reactions: readonly Reaction[]) {
  const groups = new Map<string, { count: number; users: string[] }>();

  for (const reaction of reactions) {
    const current = groups.get(reaction.content);
    if (current) {
      current.count += 1;
      current.users.push(reaction.user);
    } else {
      groups.set(reaction.content, { count: 1, users: [reaction.user] });
    }
  }

  return Array.from(groups, ([content, group]) => ({ content, ...group }));
}

function linearColorChannel(color: string, offset: number) {
  const channel = Number.parseInt(color.slice(offset, offset + 2), 16) / 255;
  return channel <= 0.04045 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4;
}

function issueLabelStyle(rawColor: string) {
  const color = LABEL_COLOR_PATTERN.exec(rawColor)?.[1]?.toLowerCase() ?? "afb8c1";
  const red = linearColorChannel(color, 0);
  const green = linearColorChannel(color, 2);
  const blue = linearColorChannel(color, 4);
  const luminance = 0.2126 * red + 0.7152 * green + 0.0722 * blue;

  return {
    backgroundColor: `#${color}`,
    color: luminance > 0.179 ? "#000000" : "#ffffff",
  };
}

function WorkItemIcon({ kind }: { kind: WorkItem["kind"] }) {
  if (kind === "issue") {
    return (
      <svg aria-hidden="true" viewBox="0 0 16 16">
        <path d="M8 9.5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Z" />
        <path d="M8 0a8 8 0 1 0 0 16A8 8 0 0 0 8 0ZM1.5 8a6.5 6.5 0 1 1 13 0 6.5 6.5 0 0 1-13 0Z" />
      </svg>
    );
  }

  return (
    <svg aria-hidden="true" viewBox="0 0 16 16">
      <path d="M1.5 3.25a2.25 2.25 0 1 1 3 2.122v5.256a2.251 2.251 0 1 1-1.5 0V5.372A2.25 2.25 0 0 1 1.5 3.25Zm9.677-.177a.75.75 0 0 1 .646.18l2.75 2.5a.75.75 0 0 1 0 1.11l-2.75 2.5a.75.75 0 1 1-1.008-1.11L12.132 7.06h-1.88a2.5 2.5 0 0 0-2.5 2.5v1.068a2.251 2.251 0 1 1-1.5 0V9.56a4 4 0 0 1 4-4h1.88l-1.317-1.197a.75.75 0 0 1 .362-1.29ZM3 3.25a.75.75 0 1 0-1.5 0 .75.75 0 0 0 1.5 0ZM3 12.75a.75.75 0 1 0-1.5 0 .75.75 0 0 0 1.5 0Zm4.75 0a.75.75 0 1 0-1.5 0 .75.75 0 0 0 1.5 0Z" />
    </svg>
  );
}

export function WorkItemRow({ item, now }: { item: WorkItem; now: number }) {
  const reactions = groupedReactions(item.reactions);
  const inactive = isInactive(item.updatedAt, now);
  const isPullRequest = item.kind === "pull_request";
  const status = isPullRequest ? workItemStatus(item) : null;

  return (
    <a
      className={inactive ? "work-item work-item-inactive" : "work-item"}
      href={item.url}
      target="_blank"
      rel="noopener noreferrer"
    >
      <span className={`item-icon item-icon-${item.kind}`} data-status={status ?? undefined}>
        <WorkItemIcon kind={item.kind} />
      </span>
      <div className="item-body">
        <div className="item-heading">
          <div className="item-main">
            <span className="item-title">{item.title}</span>
            {isPullRequest ? (
              <div className="item-summary">
                <span className="status-badge" data-status={status}>
                  {status}
                </span>
                <span className="diff-stats" title="Pull request diff">
                  <span className="additions">+{item.additions}</span>
                  <span className="deletions">-{item.deletions}</span>
                </span>
              </div>
            ) : item.labels.length > 0 ? (
              <ul className="issue-labels" aria-label="Labels">
                {item.labels.map((label) => (
                  <li className="issue-label" key={label.name} style={issueLabelStyle(label.color)}>
                    {label.name}
                  </li>
                ))}
              </ul>
            ) : null}
          </div>
        </div>

        <div className="item-details">
          <span>#{item.number}</span>
          <span aria-hidden="true">·</span>
          <span>
            opened by <strong>{item.author || "ghost"}</strong>
          </span>
          <span aria-hidden="true">·</span>
          <time dateTime={item.updatedAt} title={formatAbsoluteTime(item.updatedAt)}>
            updated {formatRelativeTime(item.updatedAt)}
          </time>
          {item.latestActivity ? (
            <span className="latest-activity">
              <span aria-hidden="true">·</span>
              <span
                className="latest-activity-text"
                title={formatAbsoluteTime(item.latestActivity.occurredAt)}
              >
                {item.latestActivity.actor || "ghost"} {activityVerb(item.latestActivity.kind)}
                {item.latestActivity.bodyText ? `: ${item.latestActivity.bodyText}` : ""}
              </span>
            </span>
          ) : null}
        </div>

        {reactions.length > 0 ? (
          <ul className="reactions" aria-label="Reactions">
            {reactions.map((reaction) => (
              <li className="reaction" key={reaction.content} title={reaction.users.join(", ")}>
                <span aria-hidden="true">{reactionSymbol(reaction.content)}</span>
                <span>{reaction.count}</span>
              </li>
            ))}
          </ul>
        ) : null}

        {item.poll.error ? <p className="item-error">Poll error: {item.poll.error}</p> : null}
      </div>
    </a>
  );
}

function App() {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [filter, setFilter] = useState<ItemFilter>("all");
  const [showInactive, setShowInactive] = useState(() => {
    try {
      return window.localStorage.getItem(SHOW_INACTIVE_STORAGE_KEY) === "true";
    } catch {
      return false;
    }
  });
  const [connection, setConnection] = useState<ConnectionState>("connecting");
  const [requestingSync, setRequestingSync] = useState(false);
  const [savingNotifications, setSavingNotifications] = useState(false);
  const [transportError, setTransportError] = useState<string | null>(null);
  const notificationsSupported = snapshot?.notifications.supported ?? false;
  const notificationsEnabled = snapshot?.notifications.enabled ?? false;
  const onlyMyPullRequests = snapshot?.notifications.onlyMyPullRequests ?? true;

  useEffect(() => {
    const controller = new AbortController();
    let disposed = false;
    let retryAttempt = 0;
    let retryTimer: number | undefined;
    let socket: WebSocket | undefined;

    const scheduleReconnect = () => {
      if (disposed) {
        return;
      }

      setConnection("disconnected");
      const delay = Math.min(1_000 * 2 ** retryAttempt, 30_000);
      retryAttempt += 1;
      retryTimer = window.setTimeout(() => {
        void bootstrapAndConnect();
      }, delay);
    };

    const connect = () => {
      if (disposed) {
        return;
      }

      setConnection("connecting");
      socket = new WebSocket(websocketURL());

      socket.addEventListener("open", () => {
        retryAttempt = 0;
        setConnection("connected");
      });

      socket.addEventListener("message", (event) => {
        try {
          const message = JSON.parse(String(event.data)) as SnapshotEvent;
          if (message.type === "snapshot.updated") {
            setSnapshot(message.snapshot);
            setTransportError(null);
          }
        } catch {
          setTransportError("Received an invalid update from the local service");
        }
      });

      socket.addEventListener("close", scheduleReconnect);
      socket.addEventListener("error", () => {
        setTransportError("Live updates disconnected");
      });
    };

    async function bootstrapAndConnect() {
      setConnection("connecting");
      try {
        const nextSnapshot = await fetchSnapshot(controller.signal);
        if (disposed) {
          return;
        }
        setSnapshot(nextSnapshot);
        setTransportError(null);
      } catch (error) {
        if (controller.signal.aborted) {
          return;
        }
        setTransportError(errorMessage(error));
      }

      connect();
    }

    void bootstrapAndConnect();

    return () => {
      disposed = true;
      controller.abort();
      if (retryTimer !== undefined) {
        window.clearTimeout(retryTimer);
      }
      socket?.close(1000, "Page closed");
    };
  }, []);

  const referenceTime = useMemo(() => {
    const generatedAt = Date.parse(snapshot?.generatedAt ?? "");
    return Number.isFinite(generatedAt) ? generatedAt : Date.now();
  }, [snapshot?.generatedAt]);

  const accountScopedItems = useMemo(
    () =>
      filterByPullRequestAuthor(snapshot?.items ?? [], snapshot?.viewer ?? "", onlyMyPullRequests),
    [snapshot?.items, snapshot?.viewer, onlyMyPullRequests],
  );

  const activityItems = useMemo(
    () => filterByActivity(accountScopedItems, showInactive, referenceTime),
    [accountScopedItems, showInactive, referenceTime],
  );

  const selectedItems = useMemo(
    () => filterItems(accountScopedItems, filter),
    [accountScopedItems, filter],
  );
  const visibleItems = useMemo(
    () => filterByActivity(selectedItems, showInactive, referenceTime),
    [selectedItems, showInactive, referenceTime],
  );
  const repositoryGroups = useMemo(() => groupByRepository(visibleItems), [visibleItems]);
  const hiddenInactiveCount = selectedItems.length - visibleItems.length;

  const counts = useMemo(() => {
    let pullRequests = 0;
    let issues = 0;

    for (const item of activityItems) {
      if (item.kind === "pull_request") {
        pullRequests += 1;
      } else {
        issues += 1;
      }
    }

    return { all: activityItems.length, pull_request: pullRequests, issue: issues };
  }, [activityItems]);

  const updateShowInactive = (nextValue: boolean) => {
    setShowInactive(nextValue);
    try {
      window.localStorage.setItem(SHOW_INACTIVE_STORAGE_KEY, String(nextValue));
    } catch {
      // Browser storage can be unavailable in restricted contexts.
    }
  };

  const saveNotificationPreferences = async (
    update: Partial<Pick<NotificationPreferences, "enabled" | "onlyMyPullRequests">>,
  ) => {
    setSavingNotifications(true);
    setTransportError(null);
    try {
      const response = await fetch("/api/notifications", {
        method: "PATCH",
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify(update),
      });
      if (!response.ok) {
        throw new Error(`Notification settings failed with HTTP ${response.status}`);
      }
    } catch (error) {
      setTransportError(errorMessage(error));
    } finally {
      setSavingNotifications(false);
    }
  };

  const updateOnlyMyPullRequests = (nextValue: boolean) =>
    saveNotificationPreferences({
      onlyMyPullRequests: nextValue,
    });

  const toggleNotifications = () =>
    saveNotificationPreferences({
      enabled: !notificationsEnabled,
    });

  const syncNow = async () => {
    setRequestingSync(true);
    setTransportError(null);

    try {
      const response = await fetch("/api/sync", {
        method: "POST",
        credentials: "same-origin",
        headers: { Accept: "application/json" },
      });

      if (!response.ok) {
        throw new Error(`Sync request failed with HTTP ${response.status}`);
      }
    } catch (error) {
      setTransportError(errorMessage(error));
    } finally {
      setRequestingSync(false);
    }
  };

  const isSyncing = requestingSync || snapshot?.sync.running === true;
  const displayedError = transportError ?? snapshot?.sync.error ?? null;

  return (
    <main className="app-shell">
      <header className="app-header">
        <div>
          <h1>GitHub Workbench</h1>
          <div className="status-line">
            {snapshot ? (
              <>
                <span className="account-context">
                  {snapshot.viewer}@{snapshot.host}
                </span>
                <span aria-hidden="true">·</span>
                <span>
                  {snapshot.repositoryCount}{" "}
                  {snapshot.repositoryCount === 1 ? "repository" : "repositories"}
                </span>
                <span aria-hidden="true">·</span>
              </>
            ) : (
              <>
                <span>Loading account…</span>
                <span aria-hidden="true">·</span>
              </>
            )}
            <span className={`connection connection-${connection}`}>
              <span className="connection-dot" />
              {connection === "connected"
                ? "Live"
                : connection === "connecting"
                  ? "Connecting"
                  : "Reconnecting"}
            </span>
          </div>
        </div>

        <div className="header-actions">
          <button
            className="sync-button notification-button"
            type="button"
            onClick={() => void toggleNotifications()}
            disabled={!snapshot || !notificationsSupported || savingNotifications}
            aria-pressed={notificationsSupported && notificationsEnabled}
          >
            {savingNotifications
              ? "Saving notifications"
              : !notificationsSupported
                ? "Notifications unavailable"
                : notificationsEnabled
                  ? "Notifications on"
                  : "Enable notifications"}
          </button>
          <button className="sync-button" type="button" onClick={syncNow} disabled={isSyncing}>
            <span className={isSyncing ? "sync-icon spinning" : "sync-icon"} aria-hidden="true">
              ↻
            </span>
            {isSyncing ? "Syncing" : "Sync now"}
          </button>
        </div>
      </header>

      {displayedError ? (
        <div className="error-banner" role="alert">
          <span aria-hidden="true">!</span>
          <p>{displayedError}</p>
        </div>
      ) : null}

      <div className="list-controls">
        <nav className="filter-bar" aria-label="Work item filters">
          {(Object.keys(filterLabels) as ItemFilter[]).map((itemFilter) => (
            <button
              className={filter === itemFilter ? "filter-button active" : "filter-button"}
              type="button"
              key={itemFilter}
              onClick={() => setFilter(itemFilter)}
              aria-pressed={filter === itemFilter}
            >
              {filterLabels[itemFilter]}
              <span>{counts[itemFilter]}</span>
            </button>
          ))}
        </nav>
        <div className="view-options">
          <label className="list-toggle">
            <input
              type="checkbox"
              checked={onlyMyPullRequests}
              onChange={(event) => {
                void updateOnlyMyPullRequests(event.currentTarget.checked);
              }}
              disabled={!snapshot || savingNotifications}
            />
            <span>Only my PRs</span>
          </label>
          <label className="list-toggle">
            <input
              type="checkbox"
              checked={showInactive}
              onChange={(event) => updateShowInactive(event.currentTarget.checked)}
            />
            <span>Show inactive</span>
          </label>
        </div>
      </div>

      <div className="repository-results" aria-live="polite" aria-busy={!snapshot}>
        {snapshot && repositoryGroups.length > 0 ? (
          <div className="repository-groups">
            {repositoryGroups.map((group) => (
              <section
                className="repository-card"
                key={group.repository}
                aria-label={group.repository}
              >
                <header className="repository-header">
                  <h2>{group.repository}</h2>
                  <span>
                    {group.items.length} {group.items.length === 1 ? "item" : "items"}
                  </span>
                </header>
                <div className="work-list">
                  {group.items.map((item) => (
                    <WorkItemRow item={item} now={referenceTime} key={item.url} />
                  ))}
                </div>
              </section>
            ))}
          </div>
        ) : (
          <div className="empty-panel">
            <div className="empty-state">
              <span className="empty-mark" aria-hidden="true">
                {snapshot ? "✓" : "…"}
              </span>
              <h2>
                {snapshot
                  ? `No ${filterLabels[filter].toLowerCase()} to show`
                  : "Loading workbench"}
              </h2>
              <p>
                {snapshot
                  ? hiddenInactiveCount > 0
                    ? `${hiddenInactiveCount} inactive ${hiddenInactiveCount === 1 ? "item is" : "items are"} hidden.`
                    : "The local cache is up to date for this view."
                  : "Reading the initial snapshot from the local service."}
              </p>
            </div>
          </div>
        )}
      </div>
    </main>
  );
}

export default App;
