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
  labels: [],
  latestActivity: null,
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

  it("renders the latest activity after the updated time", () => {
    const markup = renderToStaticMarkup(
      <WorkItemRow
        item={{
          ...item,
          latestActivity: {
            kind: "comment",
            actor: "alice",
            bodyText: "Please cover the retry case.",
            occurredAt: "2026-07-28T00:05:00Z",
            url: "https://github.com/acme/web/pull/42#issuecomment-1",
          },
        }}
        now={Date.parse("2026-07-28T00:05:00Z")}
      />,
    );

    expect(markup).toContain("alice commented: Please cover the retry case.");
    expect(markup.indexOf("updated")).toBeLessThan(markup.indexOf("alice commented"));
    expect(markup.match(/<a\b/g)).toHaveLength(1);
  });

  it("omits the redundant open status badge for issues", () => {
    const markup = renderToStaticMarkup(
      <WorkItemRow
        item={{
          ...item,
          kind: "issue",
          title: "Track API errors",
          url: "https://github.com/acme/web/issues/42",
        }}
        now={Date.parse("2026-07-28T00:00:00Z")}
      />,
    );

    expect(markup).not.toContain('class="status-badge"');
  });

  it("renders GitHub labels for issues", () => {
    const markup = renderToStaticMarkup(
      <WorkItemRow
        item={{
          ...item,
          kind: "issue",
          title: "Track API errors",
          url: "https://github.com/acme/web/issues/42",
          labels: [
            { name: "bug", color: "d73a4a" },
            { name: "good first issue", color: "7057ff" },
            { name: "help wanted", color: "fbca04" },
          ],
        }}
        now={Date.parse("2026-07-28T00:00:00Z")}
      />,
    );

    expect(markup).toContain('aria-label="Labels"');
    expect(markup).toContain(">bug</li>");
    expect(markup).toContain(">good first issue</li>");
    expect(markup).toContain(">help wanted</li>");
    expect(markup).toContain("background-color:#d73a4a");
    expect(markup).toContain("background-color:#7057ff;color:#ffffff");
    expect(markup).toContain("background-color:#fbca04;color:#000000");
  });

  it("keeps issue labels out of pull request rows", () => {
    const markup = renderToStaticMarkup(
      <WorkItemRow
        item={{
          ...item,
          labels: [{ name: "bug", color: "d73a4a" }],
        }}
        now={Date.parse("2026-07-28T00:00:00Z")}
      />,
    );

    expect(markup).not.toContain('aria-label="Labels"');
    expect(markup).not.toContain(">bug</li>");
  });
});
