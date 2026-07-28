# GitHub pull request review data

This note records the GitHub API signals needed to show comment activity and
unresolved review conversations in GitHub Workbench.

## Data concepts

GitHub exposes three related objects with different meanings:

| Signal | GitHub object | Useful fields |
| --- | --- | --- |
| Conversation-tab comment | `IssueComment` | `PullRequest.comments.totalCount` |
| Submitted review | `PullRequestReview` | `state`, optional body, review comments |
| Diff conversation | `PullRequestReviewThread` | `isResolved`, `isOutdated`, comments |

A review has one of `APPROVED`, `CHANGES_REQUESTED`, `COMMENTED`, `DISMISSED`,
or `PENDING`. The pull request-level `reviewDecision` summarizes merge review
status as `APPROVED`, `CHANGES_REQUESTED`, or `REVIEW_REQUIRED`.

Resolution belongs to a review thread. A review comment's own `state` only
distinguishes `PENDING` from `SUBMITTED`.

Sources:

- [GitHub GraphQL pull request reference](https://docs.github.com/en/graphql/reference/pulls)
- [GitHub pull request review REST reference](https://docs.github.com/en/rest/pulls/reviews)
- [GitHub review comment REST reference](https://docs.github.com/en/rest/pulls/comments)
- [GitHub conversation resolution guide](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/reviewing-changes-in-pull-requests/commenting-on-a-pull-request)

## Comment count

`PullRequest.totalCommentsCount` is the closest API field to the aggregate
comment count shown by GitHub. It can be added as one scalar in the existing
account-wide search fragment.

Two live API samples confirmed this observed composition:

- one submitted review comment plus one non-empty review body produced a total
  of two;
- four submitted review comments plus one non-empty review body produced a
  total of five.

Conversation comments also contribute to the total. The GraphQL schema
describes the field as the number of comments received by the pull request.
The sample composition remains an observed behavior; the schema guarantees the
aggregate definition.

## Review thread status

`PullRequest.reviewThreads` returns every diff conversation. Each node exposes:

- `isResolved`
- `isOutdated`
- `comments.totalCount`
- `resolvedBy`

The connection has `totalCount`, cursor pagination, and pagination arguments.
The client fetches the nodes and counts `isResolved == false`. An outdated
thread retains an independent resolution state; the actionable count should
therefore include every unresolved thread.

The current API permits `first` values up to 100. Pull requests with more than
100 review threads require cursor pagination.

## Minimal query

```graphql
query PullRequestReviewActivity(
  $owner: String!
  $name: String!
  $number: Int!
  $after: String
) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      totalCommentsCount
      reviewDecision
      reviewThreads(first: 100, after: $after) {
        totalCount
        nodes {
          id
          isResolved
          isOutdated
        }
        pageInfo {
          hasNextPage
          endCursor
        }
      }
    }
  }
  rateLimit {
    cost
    remaining
    resetAt
  }
}
```

A live call of this minimal per-pull-request query reported a GraphQL cost of
one point. GitHub calculates query cost from connections, enforces a maximum
of 100 nodes per connection page, and caps one call at 500,000 requested
nodes. Actual rate-limit headers and `rateLimit.cost` remain authoritative.

Source:

- [GitHub GraphQL rate and query limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api)

## Current implementation gap

The account-wide search already fetches `reviewDecision`, which lets the UI
show explicit approval and change-request states. Comment count and review
thread fields are the next additions.

Each relevant pull request already owns an adaptive reaction polling resource.
The same scheduling policy starts at ten seconds after a change and backs off
according to item age.

## Recommended implementation

1. Add `totalCommentsCount` to the existing pull request search fragment and
   persist it as `comment_count`.
2. Add one GraphQL review-activity polling resource per relevant pull request.
   Fetch and aggregate review threads with cursor pagination.
3. Persist only aggregate list data:
   - `comment_count`
   - `review_thread_count`
   - `unresolved_review_thread_count`
4. Reuse the current adaptive polling policy and reset the resource to the hot
   interval when any aggregate changes.
5. Keep the reaction REST resource independent so its ETag and rate-limit
   behavior remain intact.

The UI can render a GitHub-style comment indicator with these states:

| Data | Display |
| --- | --- |
| `comment_count > 0`, `unresolved_review_thread_count == 0` | muted comment icon and count |
| `unresolved_review_thread_count > 0` | orange comment indicator with `N unresolved` |
| review threads exist and all are resolved | comment count with a resolved tooltip |

This preserves the existing PR status badge for `reviewDecision` and adds
informational-review and inline-conversation signals.
