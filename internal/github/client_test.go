package github

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
		if !strings.Contains(payload.Query, "labels(first: 100") {
			t.Fatalf("GraphQL query = %q, want issue labels", payload.Query)
		}
		if !strings.Contains(payload.Query, "\n        id\n") {
			t.Fatalf("GraphQL query = %q, want global node IDs", payload.Query)
		}
		queries = append(queries, payload.Variables.Query)

		switch {
		case strings.Contains(payload.Variables.Query, "review-requested:"):
			return jsonResponse(http.StatusOK, graphQLPage(`[
				{
					"__typename": "PullRequest",
					"id": "PR_rocket_7",
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
					"id": "PR_satellite_11",
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
					"id": "PR_rocket_7",
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
					"id": "I_rocket_3",
					"number": 3,
					"title": "Track fuel",
					"url": "https://github.com/acme/rocket/issues/3",
					"state": "OPEN",
					"author": {"login": "hubot"},
					"createdAt": "2026-07-20T10:00:00Z",
					"updatedAt": "2026-07-27T10:00:00Z",
					"repository": {"nameWithOwner": "acme/rocket"},
					"labels": {
						"nodes": [
							{"name": "bug", "color": "d73a4a"},
							{"name": "priority: high", "color": "b60205"}
						]
					}
				},
				{
					"__typename": "Issue",
					"id": "I_rocket_2",
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
					"id": "I_rocket_3",
					"number": 3,
					"title": "Track fuel",
					"url": "https://github.com/acme/rocket/issues/3",
					"state": "OPEN",
					"author": {"login": "hubot"},
					"createdAt": "2026-07-20T10:00:00Z",
					"updatedAt": "2026-07-27T10:00:00Z",
					"repository": {"nameWithOwner": "acme/rocket"},
					"labels": {
						"nodes": [
							{"name": "bug", "color": "d73a4a"},
							{"name": "priority: high", "color": "b60205"}
						]
					}
				}
			]`), nil), nil
		case strings.Contains(payload.Variables.Query, "mentions:"):
			return jsonResponse(http.StatusOK, graphQLPage(`[
				{
					"__typename": "PullRequest",
					"id": "PR_satellite_11",
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
	issue := byURL["https://github.com/acme/rocket/issues/3"]
	if issue.Kind != model.ItemKindIssue {
		t.Fatalf("issue kind = %q, want issue", issue.Kind)
	}
	if issue.NodeID != "I_rocket_3" {
		t.Fatalf("issue node ID = %q, want I_rocket_3", issue.NodeID)
	}
	wantLabels := []model.Label{
		{Name: "bug", Color: "d73a4a"},
		{Name: "priority: high", Color: "b60205"},
	}
	if !slices.Equal(issue.Labels, wantLabels) {
		t.Fatalf("issue labels = %#v, want %#v", issue.Labels, wantLabels)
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

func TestClientFetchLatestActivitiesSelectsNewestGraphQLCandidate(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/graphql" {
			t.Fatalf("request path = %q, want /graphql", request.URL.Path)
		}
		var payload struct {
			Query     string `json:"query"`
			Variables struct {
				IDs []string `json:"ids"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		if !slices.Equal(payload.Variables.IDs, []string{"I_comment", "I_label"}) {
			t.Fatalf(
				"GraphQL node IDs = %q, want [I_comment I_label]",
				payload.Variables.IDs,
			)
		}
		for _, fragment := range []string{
			"nodes(ids: $ids)",
			"comments(first: 1",
			"orderBy: {field: UPDATED_AT, direction: DESC}",
			"timelineItems(last: 1",
			"... on LabeledEvent",
		} {
			if !strings.Contains(payload.Query, fragment) {
				t.Fatalf("GraphQL query = %q, want fragment %q", payload.Query, fragment)
			}
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{
			"data": {
				"nodes": [
					{
						"__typename": "Issue",
						"id": "I_label",
						"url": "https://github.com/acme/rocket/issues/2",
						"comments": {
							"nodes": [{
								"author": {"login": "octocat"},
								"bodyText": "an older comment",
								"createdAt": "2026-07-28T10:00:00Z",
								"updatedAt": "2026-07-28T10:00:00Z",
								"url": "https://github.com/acme/rocket/issues/2#issuecomment-1"
							}]
						},
						"timelineItems": {
							"nodes": [{
								"__typename": "LabeledEvent",
								"actor": {"login": "maintainer"},
								"createdAt": "2026-07-28T10:30:00Z",
								"label": {"name": "priority: high", "color": "b60205"}
							}]
						}
					},
					{
						"__typename": "Issue",
						"id": "I_comment",
						"url": "https://github.com/acme/rocket/issues/1",
						"comments": {
							"nodes": [{
								"author": {"login": "reviewer"},
								"bodyText": "  Please \n cover\t the retry case.  ",
								"createdAt": "2026-07-28T10:10:00Z",
								"updatedAt": "2026-07-28T10:20:00Z",
								"url": "https://github.com/acme/rocket/issues/1#issuecomment-2"
							}]
						},
						"timelineItems": {
							"nodes": [{
								"__typename": "UnlabeledEvent",
								"actor": {"login": "maintainer"},
								"createdAt": "2026-07-28T10:15:00Z",
								"label": {"name": "blocked", "color": "d73a4a"}
							}]
						}
					}
				]
			}
		}`)
	}))
	defer server.Close()

	client, err := NewWithBaseURL(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	results, err := client.FetchLatestActivities(t.Context(), []model.ActivityTarget{
		{
			NodeID:     "I_comment",
			Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
			Number:     1,
			Kind:       model.ItemKindIssue,
			ETag:       `"unused-issue-etag"`,
		},
		{
			NodeID:     "I_label",
			Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
			Number:     2,
			Kind:       model.ItemKindIssue,
		},
	})
	if err != nil {
		t.Fatalf("FetchLatestActivities() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len(FetchLatestActivities()) = %d, want 2", len(results))
	}
	wantCommentTime := time.Date(2026, 7, 28, 10, 20, 0, 0, time.UTC)
	wantComment := &model.Activity{
		Kind:       "comment",
		Actor:      "reviewer",
		BodyText:   "Please cover the retry case.",
		OccurredAt: wantCommentTime,
		URL:        "https://github.com/acme/rocket/issues/1#issuecomment-2",
	}
	if results[0].Activity == nil || *results[0].Activity != *wantComment {
		t.Fatalf("comment activity = %#v, want %#v", results[0].Activity, wantComment)
	}
	if results[0].ETag != `"unused-issue-etag"` {
		t.Fatalf("issue ETag = %q, want input ETag", results[0].ETag)
	}
	wantLabelTime := time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC)
	wantLabel := &model.Activity{
		Kind:       "labeled",
		Actor:      "maintainer",
		BodyText:   "priority: high",
		OccurredAt: wantLabelTime,
		URL:        "https://github.com/acme/rocket/issues/2",
	}
	if results[1].Activity == nil || *results[1].Activity != *wantLabel {
		t.Fatalf("label activity = %#v, want %#v", results[1].Activity, wantLabel)
	}
}

func TestClientFetchLatestActivitiesNormalizesPullRequestReview(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/graphql":
			var payload graphQLActivityRequest
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatalf("decode GraphQL request: %v", err)
			}
			for _, fragment := range []string{
				"PULL_REQUEST_REVIEW",
				"REOPENED_EVENT",
				"READY_FOR_REVIEW_EVENT",
				"CONVERT_TO_DRAFT_EVENT",
				"... on PullRequestReview",
			} {
				if !strings.Contains(payload.Query, fragment) {
					t.Fatalf(
						"GraphQL query = %q, want fragment %q",
						payload.Query,
						fragment,
					)
				}
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{
				"data": {
					"nodes": [{
						"__typename": "PullRequest",
						"id": "PR_rocket_7",
						"url": "https://github.com/acme/rocket/pull/7",
						"comments": {"nodes": []},
						"timelineItems": {
							"nodes": [{
								"__typename": "PullRequestReview",
								"author": {"login": "reviewer"},
								"bodyText": "",
								"state": "APPROVED",
								"submittedAt": "2026-07-28T10:30:00Z",
								"updatedAt": "2026-07-28T10:30:00Z",
								"url": "https://github.com/acme/rocket/pull/7#pullrequestreview-42"
							}]
						}
					}]
				}
			}`)
		case "/repos/acme/rocket/pulls/7/comments":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, "[]")
		default:
			t.Fatalf("request path = %q, want GraphQL or review comments", request.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewWithBaseURL(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	results, err := client.FetchLatestActivities(t.Context(), []model.ActivityTarget{{
		NodeID:     "PR_rocket_7",
		Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
		Number:     7,
		Kind:       model.ItemKindPullRequest,
	}})
	if err != nil {
		t.Fatalf("FetchLatestActivities() error = %v", err)
	}
	want := &model.Activity{
		Kind:       "review_approved",
		Actor:      "reviewer",
		OccurredAt: time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC),
		URL:        "https://github.com/acme/rocket/pull/7#pullrequestreview-42",
	}
	if len(results) != 1 || results[0].Activity == nil ||
		*results[0].Activity != *want {
		t.Fatalf("review activity = %#v, want %#v", results, want)
	}
}

func TestClientFetchLatestActivitiesBatchesGlobalNodeIDs(t *testing.T) {
	t.Parallel()

	batches := make([][]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		var payload graphQLActivityRequest
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatalf("decode GraphQL request: %v", err)
		}
		if len(payload.Variables.IDs) > 50 {
			t.Fatalf("GraphQL batch size = %d, want at most 50", len(payload.Variables.IDs))
		}
		batches = append(batches, slices.Clone(payload.Variables.IDs))
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{"data":{"nodes":[]}}`)
	}))
	defer server.Close()

	client, err := NewWithBaseURL(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	targets := make([]model.ActivityTarget, 0, 51)
	for index := range 51 {
		targets = append(targets, model.ActivityTarget{
			NodeID: "I_" + strconv.Itoa(index),
			Repository: model.Repository{
				Host:  "github.com",
				Owner: "acme",
				Name:  "rocket",
			},
			Number: index + 1,
			Kind:   model.ItemKindIssue,
		})
	}
	results, err := client.FetchLatestActivities(t.Context(), targets)
	if err != nil {
		t.Fatalf("FetchLatestActivities() error = %v", err)
	}
	if len(results) != len(targets) {
		t.Fatalf("result count = %d, want %d", len(results), len(targets))
	}
	if len(batches) != 2 {
		t.Fatalf("GraphQL batch count = %d, want 2", len(batches))
	}
	if len(batches[0]) != 50 || len(batches[1]) != 1 {
		t.Fatalf("GraphQL batch sizes = [%d %d], want [50 1]", len(batches[0]), len(batches[1]))
	}
	if batches[0][0] != "I_0" || batches[0][49] != "I_49" ||
		batches[1][0] != "I_50" {
		t.Fatalf("GraphQL batches = %q, want stable input order", batches)
	}
}

func TestClientFetchLatestActivitiesRejectsEmptyNodeID(t *testing.T) {
	t.Parallel()

	client, err := NewWithBaseURL(
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("GitHub request sent for empty node ID")
			return nil, nil
		})},
		"https://api.example.test",
	)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}

	_, err = client.FetchLatestActivities(t.Context(), []model.ActivityTarget{{
		Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
		Number:     1,
		Kind:       model.ItemKindIssue,
	}})
	if err == nil || !strings.Contains(err.Error(), "node id is required") {
		t.Fatalf("FetchLatestActivities() error = %v, want missing node ID", err)
	}
}

func TestClientFetchLatestActivitiesPreservesGraphQLRateLimit(t *testing.T) {
	t.Parallel()

	client, err := NewWithBaseURL(
		&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return jsonResponse(
				http.StatusOK,
				`{"data":{"nodes":[]},"errors":[{"message":"API rate limit exceeded"}]}`,
				http.Header{"Retry-After": {"30"}},
			), nil
		})},
		"https://api.example.test",
	)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	_, err = client.FetchLatestActivities(t.Context(), []model.ActivityTarget{{
		NodeID:     "I_rocket_1",
		Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
		Number:     1,
		Kind:       model.ItemKindIssue,
	}})
	var rateLimit *RateLimitError
	if !errors.As(err, &rateLimit) {
		t.Fatalf("FetchLatestActivities() error = %T, want *RateLimitError", err)
	}
}

func TestClientFetchLatestActivitiesTruncatesUnicodeBodyTo160Runes(t *testing.T) {
	t.Parallel()

	longBody := strings.Repeat("界", 170)
	encodedBody, err := json.Marshal(longBody)
	if err != nil {
		t.Fatalf("encode comment body: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(
			response,
			`{"data":{"nodes":[{
				"__typename":"Issue",
				"id":"I_long",
				"url":"https://github.com/acme/rocket/issues/1",
				"comments":{"nodes":[{
					"author":{"login":"reviewer"},
					"bodyText":`+string(encodedBody)+`,
					"createdAt":"2026-07-28T10:00:00Z",
					"updatedAt":"2026-07-28T10:00:00Z",
					"url":"https://github.com/acme/rocket/issues/1#issuecomment-1"
				}]},
				"timelineItems":{"nodes":[]}
			}]}}`,
		)
	}))
	defer server.Close()

	client, err := NewWithBaseURL(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	results, err := client.FetchLatestActivities(t.Context(), []model.ActivityTarget{{
		NodeID:     "I_long",
		Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
		Number:     1,
		Kind:       model.ItemKindIssue,
	}})
	if err != nil {
		t.Fatalf("FetchLatestActivities() error = %v", err)
	}
	if len(results) != 1 || results[0].Activity == nil {
		t.Fatalf("activity results = %#v, want one activity", results)
	}
	want := strings.Repeat("界", 159) + "…"
	if results[0].Activity.BodyText != want {
		t.Fatalf(
			"activity body = %q (%d runes), want %q (%d runes)",
			results[0].Activity.BodyText,
			len([]rune(results[0].Activity.BodyText)),
			want,
			len([]rune(want)),
		)
	}
}

func TestClientFetchLatestActivitiesUsesNewestInlineReviewComment(t *testing.T) {
	t.Parallel()

	var reviewCommentRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/graphql":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"data":{"nodes":[{
				"__typename":"PullRequest",
				"id":"PR_rocket_7",
				"url":"https://github.com/acme/rocket/pull/7",
				"comments":{"nodes":[{
					"author":{"login":"octocat"},
					"bodyText":"conversation comment",
					"createdAt":"2026-07-28T10:00:00Z",
					"updatedAt":"2026-07-28T10:00:00Z",
					"url":"https://github.com/acme/rocket/pull/7#issuecomment-1"
				}]},
				"timelineItems":{"nodes":[]}
			}]}}`)
		case "/repos/acme/rocket/pulls/7/comments":
			if request.Method != http.MethodGet {
				t.Fatalf("review comment method = %q, want GET", request.Method)
			}
			wantQuery := "direction=desc&per_page=1&sort=updated"
			if request.URL.RawQuery != wantQuery {
				t.Fatalf("review comment query = %q, want %q", request.URL.RawQuery, wantQuery)
			}
			if request.Header.Get("If-None-Match") != "" {
				t.Fatalf(
					"If-None-Match = %q, want empty without a cached inline comment",
					request.Header.Get("If-None-Match"),
				)
			}
			if count := reviewCommentRequests.Add(1); count > 1 {
				t.Fatalf("review comment request count = %d, want 1", count)
			}
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("ETag", `"inline-v2"`)
			response.Header().Set(
				"Link",
				"<http://"+request.Host+
					"/repos/acme/rocket/pulls/7/comments?page=2>; rel=\"next\"",
			)
			_, _ = io.WriteString(response, `[{
				"user":{"login":"reviewer"},
				"body_text":"  Please\nrename\tthis value. ",
				"created_at":"2026-07-28T10:20:00Z",
				"updated_at":"2026-07-28T10:30:00Z",
				"html_url":"https://github.com/acme/rocket/pull/7#discussion_r42"
			}]`)
		default:
			t.Fatalf("request path = %q, want GraphQL or review comments", request.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewWithBaseURL(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	results, err := client.FetchLatestActivities(t.Context(), []model.ActivityTarget{{
		NodeID:     "PR_rocket_7",
		Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
		Number:     7,
		Kind:       model.ItemKindPullRequest,
		ETag:       `"inline-v1"`,
	}})
	if err != nil {
		t.Fatalf("FetchLatestActivities() error = %v", err)
	}
	want := &model.Activity{
		Kind:       "review_comment",
		Actor:      "reviewer",
		BodyText:   "Please rename this value.",
		OccurredAt: time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC),
		URL:        "https://github.com/acme/rocket/pull/7#discussion_r42",
	}
	if len(results) != 1 || results[0].Activity == nil ||
		*results[0].Activity != *want {
		t.Fatalf("latest activity = %#v, want %#v", results, want)
	}
	if results[0].ETag != `"inline-v2"` {
		t.Fatalf("activity ETag = %q, want inline-v2", results[0].ETag)
	}
	if reviewCommentRequests.Load() != 1 {
		t.Fatalf(
			"review comment request count = %d, want 1",
			reviewCommentRequests.Load(),
		)
	}
}

func TestClientFetchLatestActivitiesReusesInlineCommentOnNotModified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		current         *model.Activity
		expected        *model.Activity
		wantRequestETag string
		reviewComment   string
		responseStatus  int
	}{
		{
			name: "cached inline comment",
			current: &model.Activity{
				Kind:       "review_comment",
				Actor:      "reviewer",
				BodyText:   "cached inline comment",
				OccurredAt: time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC),
				URL:        "https://github.com/acme/rocket/pull/7#discussion_r42",
			},
			expected: &model.Activity{
				Kind:       "review_comment",
				Actor:      "reviewer",
				BodyText:   "cached inline comment",
				OccurredAt: time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC),
				URL:        "https://github.com/acme/rocket/pull/7#discussion_r42",
			},
			wantRequestETag: `"inline-v1"`,
			responseStatus:  http.StatusNotModified,
		},
		{
			name: "refetches when cached activity is not an inline comment",
			current: &model.Activity{
				Kind:       "comment",
				Actor:      "someone",
				BodyText:   "stale cached activity",
				OccurredAt: time.Date(2026, 7, 28, 10, 40, 0, 0, time.UTC),
				URL:        "https://github.com/acme/rocket/pull/7#issuecomment-1",
			},
			expected: &model.Activity{
				Kind:       "review_comment",
				Actor:      "reviewer",
				BodyText:   "new inline comment",
				OccurredAt: time.Date(2026, 7, 28, 10, 30, 0, 0, time.UTC),
				URL:        "https://github.com/acme/rocket/pull/7#discussion_r43",
			},
			reviewComment: `[{
				"user":{"login":"reviewer"},
				"body_text":"new inline comment",
				"created_at":"2026-07-28T10:30:00Z",
				"updated_at":"2026-07-28T10:30:00Z",
				"html_url":"https://github.com/acme/rocket/pull/7#discussion_r43"
			}]`,
			responseStatus: http.StatusOK,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				switch request.URL.Path {
				case "/graphql":
					response.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(response, `{"data":{"nodes":[{
						"__typename":"PullRequest",
						"id":"PR_rocket_7",
						"url":"https://github.com/acme/rocket/pull/7",
						"comments":{"nodes":[{
							"author":{"login":"octocat"},
							"bodyText":"new conversation comment",
							"createdAt":"2026-07-28T10:20:00Z",
							"updatedAt":"2026-07-28T10:20:00Z",
							"url":"https://github.com/acme/rocket/pull/7#issuecomment-2"
						}]},
						"timelineItems":{"nodes":[]}
					}]}}`)
				case "/repos/acme/rocket/pulls/7/comments":
					if request.Header.Get("If-None-Match") != test.wantRequestETag {
						t.Fatalf(
							"If-None-Match = %q, want %q",
							request.Header.Get("If-None-Match"),
							test.wantRequestETag,
						)
					}
					response.Header().Set("ETag", `"inline-v1"`)
					response.WriteHeader(test.responseStatus)
					if test.reviewComment != "" {
						_, _ = io.WriteString(response, test.reviewComment)
					}
				default:
					t.Fatalf("request path = %q, want GraphQL or review comments", request.URL.Path)
				}
			}))
			defer server.Close()

			client, err := NewWithBaseURL(server.Client(), server.URL)
			if err != nil {
				t.Fatalf("NewWithBaseURL() error = %v", err)
			}
			client.gate.interval = 0

			results, err := client.FetchLatestActivities(t.Context(), []model.ActivityTarget{{
				NodeID:         "PR_rocket_7",
				Repository:     model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
				Number:         7,
				Kind:           model.ItemKindPullRequest,
				LatestActivity: test.current,
				ETag:           `"inline-v1"`,
			}})
			if err != nil {
				t.Fatalf("FetchLatestActivities() error = %v", err)
			}
			if len(results) != 1 || results[0].Activity == nil ||
				*results[0].Activity != *test.expected {
				t.Fatalf("latest activity = %#v, want %#v", results, test.expected)
			}
			if results[0].ETag != `"inline-v1"` {
				t.Fatalf("activity ETag = %q, want inline-v1", results[0].ETag)
			}
		})
	}
}

func TestClientFetchLatestActivitiesPrefersInlineCommentOnTimestampTie(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/graphql":
			_, _ = io.WriteString(response, `{"data":{"nodes":[{
				"__typename":"PullRequest",
				"id":"PR_rocket_7",
				"url":"https://github.com/acme/rocket/pull/7",
				"comments":{"nodes":[]},
				"timelineItems":{"nodes":[{
					"__typename":"PullRequestReview",
					"author":{"login":"reviewer"},
					"bodyText":"",
					"state":"COMMENTED",
					"submittedAt":"2026-07-28T10:30:00Z",
					"updatedAt":"2026-07-28T10:30:00Z",
					"url":"https://github.com/acme/rocket/pull/7#pullrequestreview-42"
				}]}
			}]}}`)
		case "/repos/acme/rocket/pulls/7/comments":
			_, _ = io.WriteString(response, `[{
				"user":{"login":"reviewer"},
				"body_text":"Please rename this value.",
				"created_at":"2026-07-28T10:30:00Z",
				"updated_at":"2026-07-28T10:30:00Z",
				"html_url":"https://github.com/acme/rocket/pull/7#discussion_r42"
			}]`)
		default:
			t.Fatalf("request path = %q, want GraphQL or review comments", request.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewWithBaseURL(server.Client(), server.URL)
	if err != nil {
		t.Fatalf("NewWithBaseURL() error = %v", err)
	}
	client.gate.interval = 0

	results, err := client.FetchLatestActivities(t.Context(), []model.ActivityTarget{{
		NodeID:     "PR_rocket_7",
		Repository: model.Repository{Host: "github.com", Owner: "acme", Name: "rocket"},
		Number:     7,
		Kind:       model.ItemKindPullRequest,
	}})
	if err != nil {
		t.Fatalf("FetchLatestActivities() error = %v", err)
	}
	if len(results) != 1 || results[0].Activity == nil {
		t.Fatalf("latest activity = %#v, want inline review comment", results)
	}
	if results[0].Activity.Kind != "review_comment" ||
		results[0].Activity.BodyText != "Please rename this value." {
		t.Fatalf("latest activity = %#v, want inline review comment body", results[0].Activity)
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
