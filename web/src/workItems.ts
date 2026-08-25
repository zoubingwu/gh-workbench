import type { WorkItemKind } from "./types";

const INACTIVE_AFTER_MS = 30 * 24 * 60 * 60 * 1_000;

export function isInactive(updatedAt: string, now = Date.now()): boolean {
  const updatedAtTime = Date.parse(updatedAt);
  return Number.isFinite(updatedAtTime) && now - updatedAtTime > INACTIVE_AFTER_MS;
}

export function filterByActivity<T extends { updatedAt: string }>(
  items: readonly T[],
  showInactive: boolean,
  now = Date.now(),
): readonly T[] {
  if (showInactive) {
    return items;
  }

  return items.filter((item) => !isInactive(item.updatedAt, now));
}

export function filterByViewerAuthor<T extends { author: string }>(
  items: readonly T[],
  viewer: string,
  onlyMine: boolean,
): readonly T[] {
  if (!onlyMine) {
    return items;
  }

  const normalizedViewer = viewer.toLowerCase();
  return items.filter((item) => item.author.toLowerCase() === normalizedViewer);
}

export type RepositoryGroup<T> = {
  repository: string;
  items: T[];
};

export function groupByRepository<T extends { repository: string }>(
  items: readonly T[],
): RepositoryGroup<T>[] {
  const groups = new Map<string, T[]>();

  for (const item of items) {
    const group = groups.get(item.repository);
    if (group) {
      group.push(item);
    } else {
      groups.set(item.repository, [item]);
    }
  }

  return Array.from(groups, ([repository, groupItems]) => ({
    repository,
    items: groupItems,
  }));
}

export type WorkItemStatus =
  | "Draft"
  | "Review requested"
  | "Changes requested"
  | "Approved"
  | "Review required"
  | "Open";

type StatusItem = {
  kind: WorkItemKind;
  isDraft: boolean;
  reviewDecision: string;
  needsReview: boolean;
};

export function workItemStatus(item: StatusItem): WorkItemStatus {
  if (item.kind === "issue") {
    return "Open";
  }
  if (item.isDraft) {
    return "Draft";
  }
  if (item.needsReview) {
    return "Review requested";
  }

  switch (item.reviewDecision.toUpperCase()) {
    case "CHANGES_REQUESTED":
      return "Changes requested";
    case "APPROVED":
      return "Approved";
    case "REVIEW_REQUIRED":
      return "Review required";
    default:
      return "Open";
  }
}
