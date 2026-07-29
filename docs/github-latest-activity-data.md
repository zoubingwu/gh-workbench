# GitHub latest activity data

This note defines the API shape for showing one concise latest activity after
each Issue or pull request's `updated … ago` text.

## Recommendation

Keep account-wide search as discovery, add each item's global `id`, deduplicate
the five search result sets, then enrich due items in batches with
`nodes(ids: $ids)`.

Use four candidates and select the greatest activity timestamp:

1. `comments(first: 1, orderBy: {field: UPDATED_AT, direction: DESC})` for an
   Issue comment or pull request conversation comment.
2. `timelineItems(last: 1, itemTypes: […])` for conversation comments, reviews,
   and selected state events such as label changes, reopen, review request,
   ready-for-review, and draft conversion, plus the latest pull request commit.
3. For pull requests, a dedicated
   `timelineItems(last: 1, itemTypes: [PULL_REQUEST_REVIEW])` connection so an
   update to the last submitted review remains comparable after a later commit
   enters the mixed timeline.
4. For pull requests, one conditional REST request to
   `GET /repos/{owner}/{repo}/pulls/{number}/comments?sort=updated&direction=desc&per_page=1`
   for the newest inline review comment or thread reply.

Persist one normalized value:

```text
kind, actor, bodyText, occurredAt, url
```

Persist the latest pull request commit candidate independently so its first
observed event clock remains stable while later activity becomes the rendered
latest value.

Collapse whitespace and truncate `bodyText` for display. Keep the complete
permalink. A useful rendering is:

```text
updated 21 minutes ago · alice commented 15 minutes ago: Please cover the retry case…
```

GitHub exposes `bodyText`, author, timestamps, and URLs on `IssueComment`,
`PullRequestReview`, and `PullRequestReviewComment`. Pull request conversation
comments use the same `IssueComment` type as Issue comments.
([Issues schema](https://docs.github.com/en/graphql/reference/issues),
[pull requests schema](https://docs.github.com/en/graphql/reference/pulls))

## Coverage

| Activity | Source | Useful fields |
| --- | --- | --- |
| Issue comment | `Issue.comments` | `author.login`, `bodyText`, `createdAt`, `updatedAt`, `url` |
| PR conversation comment | `PullRequest.comments` | same `IssueComment` fields |
| Submitted PR review | `PullRequest.timelineItems` → `PullRequestReview` | `author.login`, `bodyText`, `state`, `submittedAt`, `updatedAt`, `url` |
| Inline review comment or reply | REST pull review comments | `user.login`, `body_text`, `created_at`, `updated_at`, `html_url`, `pull_request_review_id` |
| Pull request commit | `PullRequest.timelineItems` → `PullRequestCommit` | `PullRequest.updatedAt`, `committer.user.login`, `author.user.login`, `abbreviatedOid`, `messageHeadline`, `committedDate`, `url` |
| Label change | timeline `LabeledEvent` / `UnlabeledEvent` | `actor.login`, `createdAt`, `label.name`, `label.color` |
| State/review workflow | selected timeline event | `actor.login`, `createdAt`, event-specific fields |

`Issue.comments` and `PullRequest.comments` accept
`IssueCommentOrder { field: UPDATED_AT, direction: DESC }`. This catches edits
to an older conversation comment as the latest comment candidate.
([Issue comment ordering](https://docs.github.com/en/graphql/reference/issues),
[PullRequest comments](https://docs.github.com/en/graphql/reference/pulls))

`timelineItems` accepts `last`, `itemTypes`, and `since`. Useful pull request
types include `PULL_REQUEST_REVIEW`, `LABELED_EVENT`, `UNLABELED_EVENT`,
`REOPENED_EVENT`, `REVIEW_REQUESTED_EVENT`,
`REVIEW_REQUEST_REMOVED_EVENT`, `READY_FOR_REVIEW_EVENT`, and
`CONVERT_TO_DRAFT_EVENT`, plus `PULL_REQUEST_COMMIT`. The Issue union includes
Issue comments and its state and label events.
([Issue timeline](https://docs.github.com/en/graphql/reference/issues),
[pull request timeline](https://docs.github.com/en/graphql/reference/pulls))

The current `PullRequestTimelineItems` union contains
`PullRequestReviewThread`, while an inline `PullRequestReviewComment` is
available through each thread's comments connection. Finding the newest
comment across every thread therefore requires scanning and paginating all
threads. The REST review-comment endpoint provides `sort=updated`,
`direction=desc`, and `per_page=1`, which gives the exact candidate in one
request.
([PullRequest review threads](https://docs.github.com/en/graphql/reference/pulls),
[REST review comments](https://docs.github.com/en/rest/pulls/comments))

## Batch query shape

Use a conservative batch size such as 50. GitHub documents `nodes(ids:)` as a
root lookup for a list of global node IDs; the schema publishes no separate
cardinality limit for the ID list. Treat IDs as opaque strings.
([GraphQL `nodes`](https://docs.github.com/en/graphql/reference/meta),
[global node IDs](https://docs.github.com/en/graphql/guides/using-global-node-ids))

```graphql
query LatestActivity($ids: [ID!]!) {
  nodes(ids: $ids) {
    __typename
    ... on Issue {
      id
      updatedAt
      comments(
        first: 1
        orderBy: {field: UPDATED_AT, direction: DESC}
      ) {
        nodes {
          author { login }
          bodyText
          createdAt
          updatedAt
          url
        }
      }
      timelineItems(last: 1, itemTypes: [
        LABELED_EVENT
        UNLABELED_EVENT
        REOPENED_EVENT
      ]) {
        nodes { __typename }
      }
    }
    ... on PullRequest {
      id
      updatedAt
      comments(
        first: 1
        orderBy: {field: UPDATED_AT, direction: DESC}
      ) {
        nodes {
          author { login }
          bodyText
          createdAt
          updatedAt
          url
        }
      }
      timelineItems(last: 1, itemTypes: [
        ISSUE_COMMENT
        PULL_REQUEST_REVIEW
        LABELED_EVENT
        UNLABELED_EVENT
        REOPENED_EVENT
        REVIEW_REQUESTED_EVENT
        REVIEW_REQUEST_REMOVED_EVENT
        READY_FOR_REVIEW_EVENT
        CONVERT_TO_DRAFT_EVENT
        PULL_REQUEST_COMMIT
      ]) {
        nodes {
          __typename
          ... on PullRequestReview {
            author { login }
            bodyText
            state
            submittedAt
            updatedAt
            url
          }
          ... on IssueComment {
            author { login }
            bodyText
            createdAt
            updatedAt
            url
          }
          ... on PullRequestCommit {
            url
            commit {
              abbreviatedOid
              committedDate
              messageHeadline
              author { user { login } }
              committer { user { login } }
            }
          }
        }
      }
      latestCommit: timelineItems(
        last: 1
        itemTypes: [PULL_REQUEST_COMMIT]
      ) {
        nodes {
          __typename
          ... on PullRequestCommit {
            url
            commit {
              abbreviatedOid
              committedDate
              messageHeadline
              author { user { login } }
              committer { user { login } }
            }
          }
        }
      }
      latestReview: timelineItems(
        last: 1
        itemTypes: [PULL_REQUEST_REVIEW]
      ) {
        nodes {
          __typename
          ... on PullRequestReview {
            author { login }
            bodyText
            state
            submittedAt
            updatedAt
            url
          }
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

The implementation needs inline fragments for every rendered timeline event;
the abbreviated query above shows the connection layout.

## Strategy comparison

| Strategy | Requests and cost | Behavior |
| --- | --- | --- |
| Add activity to all five search fragments | Zero extra HTTP round trips. Each 100-result page adds one child connection per matching result and repeats work for items present in several searches. Refresh cadence remains account-wide. |
| Deduplicate, then batch `nodes(ids:)` | Roughly one GraphQL request per 50 due items. It supports item-specific adaptive schedules while coalescing due work. This is the recommended base. |
| Poll each item separately | One timeline request per Issue and at least two requests per PR when inline comments are included. It has the simplest ETag model and the highest initial latency and request count. |

GitHub estimates GraphQL primary cost from the number of connection requests,
divided by 100 and rounded, with a minimum cost of one. A 50-node enrichment
with one comment connection and one timeline connection per item is roughly
one point. Pull requests add connections for the independently cached latest
commit and the latest review, making a 50-PR batch roughly two points.
`rateLimit.cost` is authoritative. Adding a scan of 100 review threads plus
one comment connection per thread for 50 PRs is roughly 51 points and requests
about 10,000 nodes, so that query shape should stay out of the hot polling
path.

GraphQL user authentication normally has 5,000 points per hour. Connections
require `first` or `last` values from 1 through 100, and a call may request at
most 500,000 nodes.
([GraphQL rate and node limits](https://docs.github.com/en/graphql/overview/rate-limits-and-query-limits-for-the-graphql-api))

Authenticated REST requests normally have a 5,000-request hourly limit.
Correctly authorized conditional requests that return `304 Not Modified` do
not consume the primary limit. Save the ETag for the stable inline-comment
URL and reuse the existing adaptive polling policy.
([REST rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api),
[conditional request guidance](https://docs.github.com/en/rest/using-the-rest-api/best-practices-for-using-the-rest-api))

The REST timeline endpoint covers conversation comments, submitted reviews,
and many state events. It exposes page-based pagination with up to 100 results
and provides no reverse-sort parameter, so locating the newest event requires
tracking the final page. This makes it a weaker primary source for the
account-wide list.
([REST timeline endpoint](https://docs.github.com/en/rest/issues/timeline),
[issue event types](https://docs.github.com/en/rest/using-the-rest-api/issue-event-types))

## Accuracy boundaries

- Review-thread resolution exposes current `isResolved` and `resolvedBy`.
  The read schema provides no resolution timestamp, so it cannot produce an
  exact “alice resolved this thread at …” latest activity.
- `latestReview(last: 1)` follows timeline submission order. An update to an
  earlier review after another review was submitted sits outside this
  single-candidate window.
- `PullRequestCommit` carries `committedDate` and has no timeline-event
  timestamp. GitHub has retired `Commit.pushedDate`. The first observation
  uses `PullRequest.updatedAt` as the available event clock when the commit is
  the newest selected timeline activity and falls back to `committedDate`; the
  persisted commit identity then keeps that clock stable. An initial hydration
  after a later untracked metadata update can overestimate the push time.
- Reactions are separate connections/resources and carry no timeline event.
  Keep the existing reaction poller independent.
- Deleted comments may appear as `CommentDeletedEvent`; their body is gone.
- Check-run changes and several background GitHub updates are current state
  rather than Issue/PR timeline events. When `item.updatedAt` is newer than all
  covered candidates, omit the activity snippet or show a neutral
  `metadata updated` fallback.
- `timelineItems` offers cursor order plus `first`/`last`, with no explicit
  `orderBy` field. Keep the selected event timestamp in the normalized value
  and monitor live samples when the query is introduced.

## Implementation order

1. Add and persist GraphQL global `id` during discovery.
2. Add a per-item activity polling resource and coalesce up to 50 due IDs into
   one enrichment request.
3. Persist the normalized latest activity and push it in the existing
   snapshot.
4. Add the conditional inline-review-comment REST candidate for PRs.
5. Measure and log `rateLimit.cost`, response size, and candidate timestamps
   during rollout.
