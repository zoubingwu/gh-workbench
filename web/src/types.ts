export type WorkItemKind = "issue" | "pull_request";

export type Reaction = {
  id: number;
  content: string;
  user: string;
  createdAt: string;
};

export type PollState = {
  intervalSeconds: number;
  nextPollAt: string;
  lastPollAt: string | null;
  lastChangedAt: string | null;
  unchangedCount: number;
  error?: string | null;
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
  reactions: Reaction[];
  poll: PollState;
};

export type SyncState = {
  running: boolean;
  lastSuccess: string | null;
  error?: string | null;
};

export type Snapshot = {
  host: string;
  viewer: string;
  repositoryCount: number;
  generatedAt: string;
  sync: SyncState;
  items: WorkItem[];
};

export type SnapshotEvent = {
  type: "snapshot.updated";
  snapshot: Snapshot;
};
