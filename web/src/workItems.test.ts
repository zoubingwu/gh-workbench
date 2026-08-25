import { describe, expect, it } from "vitest";
import {
  filterByActivity,
  filterByViewerAuthor,
  groupByRepository,
  isInactive,
  workItemStatus,
} from "./workItems";

describe("isInactive", () => {
  it("marks items older than 30 days as inactive", () => {
    const now = Date.parse("2026-07-28T00:00:00Z");

    expect(isInactive("2026-06-27T23:59:59Z", now)).toBe(true);
    expect(isInactive("2026-06-28T00:00:00Z", now)).toBe(false);
    expect(isInactive("2026-07-20T00:00:00Z", now)).toBe(false);
  });
});

describe("filterByActivity", () => {
  it("hides inactive items until they are requested", () => {
    const now = Date.parse("2026-07-28T00:00:00Z");
    const items = [
      { id: "active", updatedAt: "2026-07-20T00:00:00Z" },
      { id: "inactive", updatedAt: "2026-05-01T00:00:00Z" },
    ];

    expect(filterByActivity(items, false, now)).toEqual([items[0]]);
    expect(filterByActivity(items, true, now)).toBe(items);
  });
});

describe("filterByViewerAuthor", () => {
  const items = [
    { id: "issue", kind: "issue" as const, author: "hubot" },
    { id: "mine", kind: "pull_request" as const, author: "OctoCat" },
    { id: "theirs", kind: "pull_request" as const, author: "hubot" },
  ];

  it("keeps only issues and pull requests authored by the viewer", () => {
    expect(filterByViewerAuthor(items, "octocat", true)).toEqual([items[1]]);
  });

  it("restores the full related item collection when disabled", () => {
    expect(filterByViewerAuthor(items, "octocat", false)).toBe(items);
  });
});

describe("groupByRepository", () => {
  it("preserves first-seen repository and item order", () => {
    const items = [
      { repository: "acme/api", id: 1 },
      { repository: "acme/web", id: 2 },
      { repository: "acme/api", id: 3 },
    ];

    expect(groupByRepository(items)).toEqual([
      { repository: "acme/api", items: [items[0], items[2]] },
      { repository: "acme/web", items: [items[1]] },
    ]);
  });
});

describe("workItemStatus", () => {
  it("uses the requested pull request priority and keeps issues open", () => {
    expect(
      workItemStatus({
        kind: "pull_request",
        isDraft: true,
        needsReview: true,
        reviewDecision: "CHANGES_REQUESTED",
      }),
    ).toBe("Draft");
    expect(
      workItemStatus({
        kind: "pull_request",
        isDraft: false,
        needsReview: true,
        reviewDecision: "CHANGES_REQUESTED",
      }),
    ).toBe("Review requested");
    expect(
      workItemStatus({
        kind: "pull_request",
        isDraft: false,
        needsReview: false,
        reviewDecision: "CHANGES_REQUESTED",
      }),
    ).toBe("Changes requested");
    expect(
      workItemStatus({
        kind: "pull_request",
        isDraft: false,
        needsReview: false,
        reviewDecision: "APPROVED",
      }),
    ).toBe("Approved");
    expect(
      workItemStatus({
        kind: "pull_request",
        isDraft: false,
        needsReview: false,
        reviewDecision: "REVIEW_REQUIRED",
      }),
    ).toBe("Review required");
    expect(
      workItemStatus({
        kind: "pull_request",
        isDraft: false,
        needsReview: false,
        reviewDecision: "",
      }),
    ).toBe("Open");
    expect(
      workItemStatus({
        kind: "issue",
        isDraft: true,
        needsReview: true,
        reviewDecision: "APPROVED",
      }),
    ).toBe("Open");
  });
});
