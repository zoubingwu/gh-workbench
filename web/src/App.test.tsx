import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { WorkItemRow } from "./App";
import type { WorkItem } from "./types";

const item: WorkItem = {
  repository: "acme/web",
  number: 42,
  kind: "pull_request",
  title: "Use a native link for the whole row",
  url: "https://github.com/acme/web/pull/42",
  state: "OPEN",
  author: "octocat",
  createdAt: "2026-07-27T00:00:00Z",
  updatedAt: "2026-07-28T00:00:00Z",
  isDraft: false,
  reviewDecision: "REVIEW_REQUIRED",
  mergeState: "CLEAN",
  needsReview: true,
  additions: 12,
  deletions: 3,
  reactions: [],
  poll: {
    intervalSeconds: 60,
    nextPollAt: "2026-07-28T00:01:00Z",
    lastPollAt: "2026-07-28T00:00:00Z",
    lastChangedAt: "2026-07-28T00:00:00Z",
    unchangedCount: 0,
  },
};

describe("WorkItemRow", () => {
  it("renders the whole row as one native GitHub link", () => {
    const markup = renderToStaticMarkup(
      <WorkItemRow item={item} now={Date.parse("2026-07-28T00:00:00Z")} />,
    );

    expect(markup).toContain(`href="${item.url}"`);
    expect(markup).toContain('target="_blank"');
    expect(markup).toContain('rel="noopener noreferrer"');
    expect(markup.match(/<a\b/g)).toHaveLength(1);
  });
});
