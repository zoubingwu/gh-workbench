package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestClientFetchesRelevantOpenItemsAcrossRepositories(t *testing.T) {
	t.Parallel()

	var queries []string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/graphql" {
			t.Fatalf("request path = %q, want /graphql", request.URL.Path)
		}
		if request.Method != http.MethodPost {
			t.Fatalf("request method = %q, want POST", request.Method)
		}

		var payload graphQLRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		if !strings.Contains(payload.Query, "issueCount") {
			t.Fatalf("GraphQL query = %q, want issueCount", payload.Query)
		}
		queries = append(queries, payload.Variables.Query)

		switch {
		case strings.Contains(payload.Variables.Query, "review-requested:"):
			return jsonResponse(http.StatusOK, graphQLPage(`[
				{
					"__typename": "PullRequest",
					"number": 7,
					"title": "Ship the rocket",
					"url": "https://github.com/acme/rocket/pull/7",
					"state": "OPEN",
					"author": {"login": "octocat"},
					"createdAt": "2026-07-26T10:00:00Z",
					"updatedAt": "2026-07-28T10:00:00Z",
					"repository": {"nameWithOwner": "acme/rocket"},
					"isDraft": false,
					"reviewDecision": "REVIEW_REQUIRED",
					"mergeStateStatus": "BLOCKED",
					"additions": 42,
					"deletions": 7
				}
			]`), nil), nil
		case strings.Contains(payload.Variables.Query, "reviewed-by:"):
			return jsonResponse(http.StatusOK, graphQLPage(`[
				{
					"__typename": "PullRequest",
					"number": 11,
					"title": "Keep the satellite online",
					"url": "https://github.com/octocat/satellite/pull/11",
					"state": "OPEN",
					"author": {"login": "hubot"},
					"createdAt": "2026-07-20T08:00:00Z",
					"updatedAt": "2026-07-27T08:00:00Z",
					"repository": {"nameWithOwner": "octocat/satellite"},
					"isDraft": true,
					"reviewDecision": null,
					"mergeStateStatus": "DRAFT",
					"additions": 12,
					"deletions": 3
				}
			]`), nil), nil
		case strings.Contains(payload.Variables.Query, "author:"):
			return jsonResponse(http.StatusOK, graphQLPage(`[
				{
					"__typename": "PullRequest",
					"number": 7,
					"title": "Ship the rocket",
					"url": "https://github.com/acme/rocket/pull/7",
					"state": "OPEN",
					"author": {"login": "octocat"},
					"createdAt": "2026-07-26T10:00:00Z",
					"updatedAt": "2026-07-28T10:00:00Z",
					"repository": {"nameWithOwner": "acme/rocket"},
					"isDraft": false,
					"reviewDecision": "REVIEW_REQUIRED",
					"mergeStateStatus": "BLOCKED",
					"additions": 42,
					"deletions": 7
				},
				{
					"__typename": "Issue",
					"number": 3,
					"title": "Track fuel",
					"url": "https://github.com/acme/rocket/issues/3",
					"state": "OPEN",
					"author": {"login": "hubot"},
					"createdAt": "2026-07-20T10:00:00Z",
					"updatedAt": "2026-07-27T10:00:00Z",
					"repository": {"nameWithOwner": "acme/rocket"}
				},
				{
					"__typename": "Issue",
					"number": 2,
					"title": "Already closed",
					"url": "https://github.com/acme/rocket/issues/2",
					"state": "CLOSED",
					"author": {"login": "hubot"},
					"createdAt": "2026-07-20T10:00:00Z",
					"updatedAt": "2026-07-28T11:00:00Z",
					"repository": {"nameWithOwner": "acme/rocket"}
				}
			]`), nil), nil
		case strings.Contains(payload.Variables.Query, "assignee:"):
			return jsonResponse(http.StatusOK, graphQLPage(`[
				{
					"__typename": "Issue",
					"number": 3,
					"title": "Track fuel",
					"url": "https://github.com/acme/rocket/issues/3",
					"state": "OPEN",
					"author": {"login": "hubot"},
					"createdAt": "2026-07-20T10:00:00Z",
					"updatedAt": "2026-07-27T10:00:00Z",
					"repository": {"nameWithOwner": "acme/rocket"}
				}
			]`), nil), nil
		case strings.Contains(payload.Variables.Query, "mentions:"):
			return jsonResponse(http.StatusOK, graphQLPage(`[
				{
					"__typename": "PullRequest",
					"number": 11,
					"title": "Keep the satellite online",
					"url": "https://github.com/octocat/satellite/pull/11",
					"state": "OPEN",
					"author": {"login": "hubot"},
					"createdAt": "2026-07-20T08:00:00Z",
					"updatedAt": "2026-07-27T08:00:00Z",
					"repository": {"nameWithOwner": "octocat/satellite"},
					"isDraft": true,
					"reviewDecision": null,
					"mergeStateStatus": "DRAFT",
					"additions": 12,
					"deletions": 3
				}
			]`), nil), nil
		default:
			t.Fatalf("unexpected search query %q", payload.Variables.Query)
			return nil, nil
		}
	})

	client, err := NewWithBaseURL(
		&http.Client{Transport: transport},
		"https://api.example.test",
	)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	result, err := client.FetchRelevantOpenItems(
		context.Background(),
		"github.com",
		"octocat",
	)
	if err != nil {
		t.Fatalf("FetchRelevantOpenItems() error = %v", err)
	}
	if len(queries) != 5 {
		t.Fatalf("GraphQL search request count = %d, want 5", len(queries))
	}
	for _, query := range queries {
		if !strings.Contains(query, "is:open") ||
			!strings.Contains(query, "archived:false") {
			t.Fatalf("search query = %q, want open non-archived scope", query)
		}
		if strings.Contains(query, "involves:") ||
			strings.Contains(query, "commenter:") {
			t.Fatalf("search query = %q, want direct relationship scope", query)
		}
	}
	for _, qualifier := range []string{
		"author:octocat",
		"assignee:octocat",
		"mentions:octocat",
		"reviewed-by:octocat",
		"review-requested:octocat",
	} {
		if !slices.ContainsFunc(queries, func(query string) bool {
			return strings.Contains(query, qualifier)
		}) {
			t.Fatalf("search queries = %q, want qualifier %q", queries, qualifier)
		}
	}
	if len(result.Items) != 3 {
		t.Fatalf("len(FetchRelevantOpenItems().Items) = %d, want 3", len(result.Items))
	}

	byURL := make(map[string]model.WorkItem, len(result.Items))
	for _, item := range result.Items {
		byURL[item.URL] = item
		if item.State != "open" {
			t.Fatalf("item state = %q, want open", item.State)
		}
	}
	pullRequest := byURL["https://github.com/acme/rocket/pull/7"]
	if !pullRequest.NeedsReview {
		t.Fatal("pull request NeedsReview = false, want true")
	}
	if pullRequest.Additions != 42 || pullRequest.Deletions != 7 {
		t.Fatalf(
			"pull request diff = +%d -%d, want +42 -7",
			pullRequest.Additions,
			pullRequest.Deletions,
		)
	}
	if pullRequest.ReviewDecision != "review_required" {
		t.Fatalf(
			"pull request review decision = %q, want review_required",
			pullRequest.ReviewDecision,
		)
	}
	if got := byURL["https://github.com/octocat/satellite/pull/11"]; !got.IsDraft {
		t.Fatal("reviewed pull request IsDraft = false, want true")
	}
	if got := byURL["https://github.com/acme/rocket/issues/3"]; got.Kind != model.ItemKindIssue {
		t.Fatalf("issue kind = %q, want issue", got.Kind)
	}
}

func TestClientSearchOpenItemsPaginatesCompleteResults(t *testing.T) {
	t.Parallel()

	var afterValues []*string
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var payload graphQLRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		afterValues = append(afterValues, payload.Variables.After)

		if payload.Variables.After == nil {
			return jsonResponse(http.StatusOK, graphQLPageWithInfo(`[
				{
					"__typename": "Issue",
					"number": 1,
					"title": "First",
					"url": "https://github.com/acme/rocket/issues/1",
					"state": "OPEN",
					"repository": {"nameWithOwner": "acme/rocket"}
				}
			]`, 2, true, "page-2"), nil), nil
		}
		if *payload.Variables.After != "page-2" {
			t.Fatalf("after = %q, want page-2", *payload.Variables.After)
		}
		return jsonResponse(http.StatusOK, graphQLPageWithInfo(`[
			{
				"__typename": "PullRequest",
				"number": 2,
				"title": "Second",
				"url": "https://github.com/acme/rocket/pull/2",
				"state": "OPEN",
				"repository": {"nameWithOwner": "acme/rocket"}
			}
		]`, 2, false, ""), nil), nil
	})

	client, err := NewWithBaseURL(
		&http.Client{Transport: transport},
		"https://api.example.test",
	)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	items, err := client.searchOpenItems(
		context.Background(),
		"github.com",
		"is:open author:octocat",
		false,
	)
	if err != nil {
		t.Fatalf("searchOpenItems() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(searchOpenItems()) = %d, want 2", len(items))
	}
	if len(afterValues) != 2 || afterValues[0] != nil ||
		afterValues[1] == nil || *afterValues[1] != "page-2" {
		t.Fatalf("pagination cursors = %#v, want [nil page-2]", afterValues)
	}
}

func TestClientSearchOpenItemsRejectsTruncatedResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		response  string
		wantError string
	}{
		{
			name: "search limit",
			response: graphQLPageWithInfo(
				`[]`,
				maxSearchPages*pageSize+1,
				true,
				"page-2",
			),
			wantError: "exceeding the accessible limit of 1000",
		},
		{
			name: "incomplete final page",
			response: graphQLPageWithInfo(`[
				{
					"__typename": "Issue",
					"number": 1,
					"title": "First",
					"url": "https://github.com/acme/rocket/issues/1",
					"state": "OPEN",
					"repository": {"nameWithOwner": "acme/rocket"}
				}
			]`, 2, false, ""),
			wantError: "reported 2 items but returned 1",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, test.response, nil), nil
			})
			client, err := NewWithBaseURL(
				&http.Client{Transport: transport},
				"https://api.example.test",
			)
			if err != nil {
				t.Fatalf("NewWithBaseURL() error = %v", err)
			}
			client.gate.interval = 0

			_, err = client.searchOpenItems(
				context.Background(),
				"github.com",
				"is:open author:octocat",
				false,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("searchOpenItems() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestClientUsesGitHubAPIHostConventions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host        string
		wantREST    string
		wantGraphQL string
	}{
		{
			host:        "github.com",
			wantREST:    "https://api.github.com",
			wantGraphQL: "https://api.github.com/graphql",
		},
		{
			host:        "github.localhost",
			wantREST:    "http://api.github.localhost",
			wantGraphQL: "http://api.github.localhost/graphql",
		},
		{
			host:        "garage.github.com",
			wantREST:    "https://garage.github.com/api/v3",
			wantGraphQL: "https://garage.github.com/api/graphql",
		},
		{
			host:        "github.example.com",
			wantREST:    "https://github.example.com/api/v3",
			wantGraphQL: "https://github.example.com/api/graphql",
		},
		{
			host:        "tenant.ghe.com",
			wantREST:    "https://api.tenant.ghe.com",
			wantGraphQL: "https://api.tenant.ghe.com/graphql",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.host, func(t *testing.T) {
			t.Parallel()

			client, err := New(&http.Client{}, test.host)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if got := client.baseURL.String(); got != test.wantREST {
				t.Fatalf("REST URL = %q, want %q", got, test.wantREST)
			}
			if got := client.graphqlURL.String(); got != test.wantGraphQL {
				t.Fatalf("GraphQL URL = %q, want %q", got, test.wantGraphQL)
			}
		})
	}
}

func TestClientFetchesViewer(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/user" {
			t.Fatalf("request path = %q, want /user", request.URL.Path)
		}
		return jsonResponse(http.StatusOK, `{"login":"octocat"}`, nil), nil
	})
	client, err := NewWithBaseURL(
		&http.Client{Transport: transport},
		"https://api.example.test",
	)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}

	viewer, err := client.FetchViewer(context.Background())
	if err != nil {
		t.Fatalf("FetchViewer() error = %v", err)
	}
	if viewer != "octocat" {
		t.Fatalf("FetchViewer() = %q, want octocat", viewer)
	}
}

func TestClientGatesConcurrentRequests(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		requests []time.Time
	)
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, time.Now())
		mu.Unlock()
		return jsonResponse(http.StatusOK, `{"login":"octocat"}`, nil), nil
	})
	client, err := NewWithBaseURL(
		&http.Client{Transport: transport},
		"https://api.example.test",
	)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 20 * time.Millisecond

	start := make(chan struct{})
	errors := make(chan error, 3)
	for range 3 {
		go func() {
			<-start
			_, err := client.FetchViewer(context.Background())
			errors <- err
		}()
	}
	close(start)
	for range 3 {
		if err := <-errors; err != nil {
			t.Fatalf("FetchViewer() error = %v", err)
		}
	}

	mu.Lock()
	slices.SortFunc(requests, func(a, b time.Time) int {
		return a.Compare(b)
	})
	gotRequests := slices.Clone(requests)
	mu.Unlock()
	for index := 1; index < len(gotRequests); index++ {
		gap := gotRequests[index].Sub(gotRequests[index-1])
		if gap < 15*time.Millisecond {
			t.Fatalf("request gap = %s, want at least 15ms", gap)
		}
	}
}

func TestClientFetchesReactions(t *testing.T) {
	t.Parallel()

	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/repos/acme/rocket/issues/7/reactions" {
			t.Fatalf("request path = %q, want reactions endpoint", request.URL.Path)
		}
		if request.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want GitHub media type", request.Header.Get("Accept"))
		}
		return jsonResponse(http.StatusOK, `[
			{
				"id": 42,
				"content": "eyes",
				"user": {"login": "reviewer"},
				"created_at": "2026-07-28T10:30:00Z"
			}
		]`, http.Header{"Etag": {`"reactions-v1"`}}), nil
	})

	client, err := NewWithBaseURL(
		&http.Client{Transport: transport},
		"https://api.example.test",
	)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	repository := model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"}

	result, err := client.FetchReactions(context.Background(), repository, 7, "")
	if err != nil {
		t.Fatalf("FetchReactions() error = %v", err)
	}
	if len(result.Reactions) != 1 {
		t.Fatalf("len(FetchReactions().Reactions) = %d, want 1", len(result.Reactions))
	}
	if result.Reactions[0].Content != "eyes" {
		t.Fatalf("reaction content = %q, want eyes", result.Reactions[0].Content)
	}
}

func TestResponseErrorPreservesRateLimitReset(t *testing.T) {
	t.Parallel()

	before := time.Now()
	response := jsonResponse(
		http.StatusTooManyRequests,
		`{"message":"secondary rate limit"}`,
		http.Header{"Retry-After": {"30"}},
	)
	response.Status = "429 Too Many Requests"

	err := responseError(response)
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) {
		t.Fatalf("responseError() = %T, want *RateLimitError", err)
	}
	if rateLimit.RetryAt().Before(before.Add(30 * time.Second)) {
		t.Fatalf("RetryAt() = %s, want at least 30 seconds from now", rateLimit.RetryAt())
	}
}

func graphQLPage(nodes string) string {
	var values []json.RawMessage
	if err := json.Unmarshal([]byte(nodes), &values); err != nil {
		panic(err)
	}
	return graphQLPageWithInfo(nodes, len(values), false, "")
}

func graphQLPageWithInfo(
	nodes string,
	issueCount int,
	hasNextPage bool,
	endCursor string,
) string {
	cursor, err := json.Marshal(endCursor)
	if err != nil {
		panic(err)
	}
	if endCursor == "" {
		cursor = []byte("null")
	}
	return `{"data":{"search":{"issueCount":` +
		strconv.Itoa(issueCount) +
		`,"pageInfo":{"hasNextPage":` +
		strconv.FormatBool(hasNextPage) +
		`,"endCursor":` +
		string(cursor) +
		`},"nodes":` +
		nodes +
		`}}}`
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func jsonResponse(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
