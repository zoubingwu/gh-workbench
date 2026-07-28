export type WorkItemKind = "issue" | "pull_request";

export type Reaction = {
  id: number;
  content: string;
  user: string;
  createdAt: string;
};

export type Label = {
  name: string;
  color: string;
};

export type Activity = {
  kind: string;
  actor: string;
  bodyText: string;
  occurredAt: string;
  url: string;
};

export type PollState = {
  intervalSeconds: number;
  nextPollAt: string;
  lastPollAt: string | null;
  lastChangedAt: string | null;
  unchangedCount: number;
  error?: string | null;
};

export type LocalAgentActivity = {
  state: "working" | "needs_input";
  providers: string[];
  sessionCount: number;
  confidence: "supported" | "heuristic";
};

export type WorkItem = {
  repository: string;
  number: number;
  kind: WorkItemKind;
  title: string;
  url: string;
  state: string;
  author: string;
  createdAt: string;
  updatedAt: string;
  isDraft: boolean;
  reviewDecision: string;
  mergeState: string;
  needsReview: boolean;
  additions: number;
  deletions: number;
  labels: Label[];
  latestActivity: Activity | null;
  reactions: Reaction[];
  localAgentActivity?: LocalAgentActivity | null;
  poll: PollState;
};

export type SyncState = {
  running: boolean;
  lastSuccess: string | null;
  error?: string | null;
};

export type NotificationPreferences = {
  supported: boolean;
  enabled: boolean;
  onlyMyPullRequests: boolean;
};

export type Snapshot = {
  host: string;
  viewer: string;
  repositoryCount: number;
  generatedAt: string;
  sync: SyncState;
  notifications: NotificationPreferences;
  items: WorkItem[];
};

export type SnapshotEvent = {
  type: "snapshot.updated";
  snapshot: Snapshot;
};
