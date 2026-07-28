import { describe, expect, it } from "vitest";
import { filterItems } from "./filterItems";

const items = [
  { repository: "acme/api", kind: "issue" as const, number: 12, title: "Track API errors" },
  {
    repository: "acme/api",
    kind: "pull_request" as const,
    number: 18,
    title: "Add polling backoff",
  },
  {
    repository: "acme/web",
    kind: "issue" as const,
    number: 12,
    title: "Document extension setup",
  },
];

describe("filterItems", () => {
  it("returns all work items without changing their order", () => {
    expect(filterItems(items, "all")).toEqual(items);
  });

  it("keeps only pull requests for the pull request filter", () => {
    expect(filterItems(items, "pull_request")).toEqual([items[1]]);
  });

  it("keeps matching issue numbers from different repositories", () => {
    expect(filterItems(items, "issue")).toEqual([items[0], items[2]]);
  });
});
