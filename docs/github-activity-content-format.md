# GitHub activity content format

This note defines the content representation for the one-line latest-activity
snippet.

## Finding

The GraphQL activity fields already return GitHub-rendered text. The REST
inline review-comment endpoint defaults to raw Markdown unless the request
selects its text representation.

| Activity source | Field | GitHub format |
| --- | --- | --- |
| GraphQL `IssueComment` | `bodyText` | Body rendered to text |
| GraphQL `PullRequestReview` | `bodyText` | Review body rendered as plain text |
| REST pull request review comment | `body` | Raw Markdown, the default representation |
| REST pull request review comment | `body_text` | Text-only representation, available with the text media type |

Sources:

- [GraphQL `IssueComment`](https://docs.github.com/en/graphql/reference/issues#issuecomment)
- [GraphQL `PullRequestReview`](https://docs.github.com/en/graphql/reference/pulls#pullrequestreview)
- [REST review-comment representations](https://docs.github.com/en/rest/pulls/comments?apiVersion=2022-11-28#list-review-comments-on-a-pull-request)

## Root cause and current contract

GitHub Workbench already queries GraphQL `bodyText` for Issue comments and
submitted reviews. Those paths receive rendered text.

The earlier inline review-comment request sent the generic
`application/vnd.github+json` media type and fell back from `body_text` to
`body`. The default REST representation supplied raw Markdown in `body`, so
markup such as `**`, `<sub>`, and `![badge](url)` reached the UI.

The current client requests the text media type and consumes `body_text`
exclusively.

## Minimal implementation

1. Request inline review comments with:

   ```http
   Accept: application/vnd.github-commitcomment.text+json
   ```

2. Use `body_text` as the activity body. An absent or empty value can remain an
   empty snippet; avoid displaying the raw `body` fallback.
3. Keep the existing whitespace collapse and 160-rune truncation after the
   representation is normalized.
4. Add a client test that verifies the text media type and proves raw badge
   Markdown never reaches `Activity.BodyText`.

## Cache compatibility

The persisted ETag is representation-specific. Prefix text-representation
ETags with `text-v1:` in SQLite and strip the prefix before sending
`If-None-Match`. An unversioned ETag belongs to the earlier raw representation,
so the first text request omits it and refreshes both the body and ETag. Later
polls resume conditional requests with the versioned cache.

GitHub guarantees a text representation, not a natural-language summary.
Code, image alternative text, table contents, and other meaningful symbols
may remain. Keep those in the first implementation. Add targeted cleanup only
for a repeated artifact confirmed in live samples; a generic Markdown regex
would risk removing valid code and prose.
