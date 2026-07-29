package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/zoubingwu/gh-workbench/internal/model"
)

const (
	githubAPIVersion        = "2022-11-28"
	maxErrorBody            = 4 << 10
	pageSize                = 100
	maxSearchPages          = 10
	activityBatchSize       = 50
	maxActivityRunes        = 160
	requestInterval         = time.Second
	reviewCommentETagPrefix = "text-v1:"
)

type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
	graphqlURL *url.URL
	gate       *requestGate
}

type RateLimitError struct {
	Status  string
	Message string
	Retry   time.Time
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf(
		"GitHub API returned %s: %s (retry at %s)",
		e.Status,
		e.Message,
		e.Retry.Format(time.RFC3339),
	)
}

func (e *RateLimitError) RetryAt() time.Time {
	return e.Retry
}

func New(httpClient *http.Client, host string) (*Client, error) {
	return newWithURLs(httpClient, apiBaseURL(host), graphqlAPIURL(host))
}

func apiBaseURL(host string) string {
	if strings.EqualFold(host, "garage.github.com") {
		return "https://" + host + "/api/v3"
	}
	host = auth.NormalizeHostname(host)
	switch {
	case auth.IsEnterprise(host):
		return "https://" + host + "/api/v3"
	case strings.EqualFold(host, "github.localhost"):
		return "http://api." + host
	default:
		return "https://api." + host
	}
}

func graphqlAPIURL(host string) string {
	restURL := apiBaseURL(host)
	if strings.HasSuffix(restURL, "/api/v3") {
		return strings.TrimSuffix(restURL, "/v3") + "/graphql"
	}
	return strings.TrimRight(restURL, "/") + "/graphql"
}

func NewWithBaseURL(httpClient *http.Client, baseURL string) (*Client, error) {
	return newWithURLs(
		httpClient,
		baseURL,
		strings.TrimRight(baseURL, "/")+"/graphql",
	)
}

func newWithURLs(
	httpClient *http.Client,
	baseURL string,
	graphqlURL string,
) (*Client, error) {
	if httpClient == nil {
		return nil, fmt.Errorf("create GitHub client: HTTP client is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub API URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("parse GitHub API URL: absolute URL is required")
	}
	parsedGraphQL, err := url.Parse(graphqlURL)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub GraphQL URL: %w", err)
	}
	if parsedGraphQL.Scheme == "" || parsedGraphQL.Host == "" {
		return nil, fmt.Errorf("parse GitHub GraphQL URL: absolute URL is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return &Client{
		httpClient: httpClient,
		baseURL:    parsed,
		graphqlURL: parsedGraphQL,
		gate:       &requestGate{interval: requestInterval},
	}, nil
}

func (c *Client) FetchViewer(ctx context.Context) (string, error) {
	endpoint, err := c.endpoint("user")
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("create viewer request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "gh-workbench")

	response, err := c.do(request)
	if err != nil {
		return "", fmt.Errorf("fetch viewer: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", responseError(response)
	}

	var viewer userResponse
	if err := json.NewDecoder(response.Body).Decode(&viewer); err != nil {
		return "", fmt.Errorf("decode viewer: %w", err)
	}
	if viewer.Login == "" {
		return "", fmt.Errorf("decode viewer: login is empty")
	}
	return viewer.Login, nil
}

func (c *Client) FetchRelevantOpenItems(
	ctx context.Context,
	host string,
	viewer string,
) (model.ItemsResult, error) {
	authored, err := c.searchOpenItems(
		ctx,
		host,
		fmt.Sprintf(
			"is:open author:%s archived:false sort:updated-desc",
			viewer,
		),
		false,
	)
	if err != nil {
		return model.ItemsResult{}, fmt.Errorf("search open items authored by %s: %w", viewer, err)
	}
	assigned, err := c.searchOpenItems(
		ctx,
		host,
		fmt.Sprintf(
			"is:open assignee:%s archived:false sort:updated-desc",
			viewer,
		),
		false,
	)
	if err != nil {
		return model.ItemsResult{}, fmt.Errorf("search open items assigned to %s: %w", viewer, err)
	}
	mentioned, err := c.searchOpenItems(
		ctx,
		host,
		fmt.Sprintf(
			"is:open mentions:%s archived:false sort:updated-desc",
			viewer,
		),
		false,
	)
	if err != nil {
		return model.ItemsResult{}, fmt.Errorf("search open items mentioning %s: %w", viewer, err)
	}
	reviewed, err := c.searchOpenItems(
		ctx,
		host,
		fmt.Sprintf(
			"is:open is:pr reviewed-by:%s archived:false sort:updated-desc",
			viewer,
		),
		false,
	)
	if err != nil {
		return model.ItemsResult{}, fmt.Errorf(
			"search open pull requests reviewed by %s: %w",
			viewer,
			err,
		)
	}
	reviewRequested, err := c.searchOpenItems(
		ctx,
		host,
		fmt.Sprintf(
			"is:open is:pr review-requested:%s archived:false sort:updated-desc",
			viewer,
		),
		true,
	)
	if err != nil {
		return model.ItemsResult{}, fmt.Errorf(
			"search open pull requests requesting %s: %w",
			viewer,
			err,
		)
	}

	byURL := make(
		map[string]model.WorkItem,
		len(authored)+len(assigned)+len(mentioned)+len(reviewed)+len(reviewRequested),
	)
	for _, item := range authored {
		byURL[item.URL] = item
	}
	for _, item := range assigned {
		byURL[item.URL] = item
	}
	for _, item := range mentioned {
		byURL[item.URL] = item
	}
	for _, item := range reviewed {
		byURL[item.URL] = item
	}
	for _, item := range reviewRequested {
		if existing, ok := byURL[item.URL]; ok {
			existing.NeedsReview = true
			byURL[item.URL] = existing
			continue
		}
		byURL[item.URL] = item
	}

	items := make([]model.WorkItem, 0, len(byURL))
	for _, item := range byURL {
		items = append(items, item)
	}
	slices.SortFunc(items, func(a, b model.WorkItem) int {
		if ordered := b.UpdatedAt.Compare(a.UpdatedAt); ordered != 0 {
			return ordered
		}
		if ordered := strings.Compare(a.RepositoryKey, b.RepositoryKey); ordered != 0 {
			return ordered
		}
		return a.Number - b.Number
	})
	return model.ItemsResult{Items: items}, nil
}

func (c *Client) searchOpenItems(
	ctx context.Context,
	host string,
	searchQuery string,
	needsReview bool,
) ([]model.WorkItem, error) {
	items := make([]model.WorkItem, 0, pageSize)
	var cursor *string
	totalCount := -1
	fetchedCount := 0

	for page := 0; page < maxSearchPages; page++ {
		result, err := c.fetchSearchPage(ctx, searchQuery, cursor)
		if err != nil {
			return nil, err
		}
		if totalCount == -1 {
			totalCount = result.Data.Search.IssueCount
			if totalCount > maxSearchPages*pageSize {
				return nil, fmt.Errorf(
					"paginate GitHub search: reported %d items, exceeding the accessible limit of %d",
					totalCount,
					maxSearchPages*pageSize,
				)
			}
		} else if result.Data.Search.IssueCount != totalCount {
			return nil, fmt.Errorf(
				"paginate GitHub search: result count changed from %d to %d",
				totalCount,
				result.Data.Search.IssueCount,
			)
		}
		fetchedCount += len(result.Data.Search.Nodes)
		if fetchedCount > totalCount {
			return nil, fmt.Errorf(
				"paginate GitHub search: reported %d items but returned at least %d",
				totalCount,
				fetchedCount,
			)
		}
		for _, node := range result.Data.Search.Nodes {
			if !strings.EqualFold(node.State, "open") {
				continue
			}
			repository, err := repositoryFromNameWithOwner(
				host,
				node.Repository.NameWithOwner,
			)
			if err != nil {
				return nil, err
			}
			kind := model.ItemKindIssue
			if node.TypeName == "PullRequest" {
				kind = model.ItemKindPullRequest
			}
			labels := make([]model.Label, 0, len(node.Labels.Nodes))
			for _, label := range node.Labels.Nodes {
				labels = append(labels, model.Label{
					Name:  label.Name,
					Color: label.Color,
				})
			}
			items = append(items, model.WorkItem{
				NodeID:         node.ID,
				Repository:     repository.FullName(),
				RepositoryKey:  repository.Key(),
				Number:         node.Number,
				Kind:           kind,
				Title:          node.Title,
				URL:            node.URL,
				State:          strings.ToLower(node.State),
				Author:         login(node.Author),
				CreatedAt:      node.CreatedAt,
				UpdatedAt:      node.UpdatedAt,
				IsDraft:        node.IsDraft,
				ReviewDecision: normalizeGraphQLEnum(node.ReviewDecision),
				MergeState:     normalizeGraphQLEnum(node.MergeStateStatus),
				NeedsReview:    needsReview,
				Additions:      node.Additions,
				Deletions:      node.Deletions,
				Labels:         labels,
				Reactions:      make([]model.Reaction, 0),
			})
		}

		if !result.Data.Search.PageInfo.HasNextPage {
			if fetchedCount != totalCount {
				return nil, fmt.Errorf(
					"paginate GitHub search: reported %d items but returned %d",
					totalCount,
					fetchedCount,
				)
			}
			return items, nil
		}
		if result.Data.Search.PageInfo.EndCursor == nil ||
			*result.Data.Search.PageInfo.EndCursor == "" {
			return nil, fmt.Errorf("paginate GitHub search: next cursor is empty")
		}
		cursor = result.Data.Search.PageInfo.EndCursor
	}

	return nil, fmt.Errorf(
		"paginate GitHub search: result exceeds %d items",
		maxSearchPages*pageSize,
	)
}

func (c *Client) fetchSearchPage(
	ctx context.Context,
	searchQuery string,
	cursor *string,
) (graphQLSearchResponse, error) {
	body, err := json.Marshal(graphQLRequest{
		Query: relevantItemsQuery,
		Variables: graphQLVariables{
			Query: searchQuery,
			After: cursor,
		},
	})
	if err != nil {
		return graphQLSearchResponse{}, fmt.Errorf("encode GitHub search request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.graphqlURL.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return graphQLSearchResponse{}, fmt.Errorf("create GitHub search request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "gh-workbench")

	response, err := c.do(request)
	if err != nil {
		return graphQLSearchResponse{}, fmt.Errorf("send GitHub search request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return graphQLSearchResponse{}, responseError(response)
	}

	var result graphQLSearchResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return graphQLSearchResponse{}, fmt.Errorf("decode GitHub search response: %w", err)
	}
	if err := graphQLResponseError(response, result.Errors); err != nil {
		return graphQLSearchResponse{}, err
	}
	return result, nil
}

func (c *Client) FetchReactions(
	ctx context.Context,
	repository model.Repository,
	number int,
	etag string,
) (model.ReactionsResult, error) {
	endpoint, err := c.endpoint(
		"repos",
		repository.Owner,
		repository.Name,
		"issues",
		strconv.Itoa(number),
		"reactions",
	)
	if err != nil {
		return model.ReactionsResult{}, err
	}
	query := endpoint.Query()
	query.Set("per_page", strconv.Itoa(pageSize))
	endpoint.RawQuery = query.Encode()

	rawReactions, responseETag, unchanged, err := fetchPaginated[reactionResponse](
		ctx,
		c,
		endpoint,
		etag,
	)
	if err != nil {
		return model.ReactionsResult{}, fmt.Errorf(
			"fetch reactions for pull request %d: %w",
			number,
			err,
		)
	}
	if unchanged {
		return model.ReactionsResult{ETag: responseETag, Unchanged: true}, nil
	}

	reactions := make([]model.Reaction, 0, len(rawReactions))
	for _, raw := range rawReactions {
		reactions = append(reactions, model.Reaction{
			ID:        raw.ID,
			Content:   raw.Content,
			User:      login(raw.User),
			CreatedAt: raw.CreatedAt,
		})
	}
	return model.ReactionsResult{Reactions: reactions, ETag: responseETag}, nil
}

func (c *Client) FetchLatestActivities(
	ctx context.Context,
	targets []model.ActivityTarget,
) ([]model.ActivityResult, error) {
	ids := make([]string, 0, len(targets))
	for index, target := range targets {
		if strings.TrimSpace(target.NodeID) == "" {
			return nil, fmt.Errorf(
				"fetch GitHub activities: node id is required for target %d",
				index,
			)
		}
		ids = append(ids, target.NodeID)
	}
	activities := make(map[string]graphQLActivityCandidates, len(targets))
	for start := 0; start < len(ids); start += activityBatchSize {
		end := min(start+activityBatchSize, len(ids))
		batch, err := c.fetchActivityBatch(ctx, ids[start:end])
		if err != nil {
			return nil, fmt.Errorf("fetch GitHub activity batch: %w", err)
		}
		for id, activity := range batch {
			activities[id] = activity
		}
	}

	results := make([]model.ActivityResult, 0, len(targets))
	for _, target := range targets {
		candidates := activities[target.NodeID]
		activity := candidates.Latest
		var latestCommit *model.Activity
		if sameCommitActivity(activity, candidates.LatestCommit) {
			activity = stabilizeCommitActivity(activity, target.LatestCommit)
			latestCommit = activity
		} else {
			latestCommit = stabilizeCommitActivity(
				candidates.LatestCommit,
				target.LatestCommit,
			)
		}
		activity = laterActivity(activity, candidates.LatestComment)
		activity = laterActivity(activity, candidates.LatestReview)
		etag := target.ETag
		var latestReviewComment *model.Activity
		if target.Kind == model.ItemKindPullRequest {
			inline, responseETag, unchanged, err := c.fetchLatestReviewComment(
				ctx,
				target.Repository,
				target.Number,
				target.ETag,
			)
			if err != nil {
				return nil, fmt.Errorf(
					"fetch inline review activity for pull request %d: %w",
					target.Number,
					err,
				)
			}
			etag = responseETag
			if unchanged {
				inline = target.LatestReviewComment
			}
			latestReviewComment = inline
			activity = laterActivity(activity, inline)
		}
		results = append(results, model.ActivityResult{
			Activity:            activity,
			LatestCommit:        latestCommit,
			LatestReviewComment: latestReviewComment,
			ETag:                etag,
		})
	}
	return results, nil
}

func (c *Client) fetchLatestReviewComment(
	ctx context.Context,
	repository model.Repository,
	number int,
	etag string,
) (*model.Activity, string, bool, error) {
	endpoint, err := c.endpoint(
		"repos",
		repository.Owner,
		repository.Name,
		"pulls",
		strconv.Itoa(number),
		"comments",
	)
	if err != nil {
		return nil, etag, false, err
	}
	query := endpoint.Query()
	query.Set("sort", "updated")
	query.Set("direction", "desc")
	query.Set("per_page", "1")
	endpoint.RawQuery = query.Encode()
	requestETag, currentRepresentation := strings.CutPrefix(
		etag,
		reviewCommentETagPrefix,
	)
	if !currentRepresentation {
		requestETag = ""
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return nil, etag, false, fmt.Errorf("create GitHub request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github-commitcomment.text+json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "gh-workbench")
	if requestETag != "" {
		request.Header.Set("If-None-Match", requestETag)
	}

	response, err := c.do(request)
	if err != nil {
		return nil, etag, false, fmt.Errorf("send GitHub request: %w", err)
	}
	responseETag := ""
	if requestETag != "" {
		responseETag = reviewCommentETagPrefix + requestETag
	}
	if currentETag := response.Header.Get("ETag"); currentETag != "" {
		responseETag = reviewCommentETagPrefix + currentETag
	}
	if response.StatusCode == http.StatusNotModified {
		_ = response.Body.Close()
		return nil, responseETag, true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := responseError(response)
		_ = response.Body.Close()
		return nil, responseETag, false, err
	}

	var comments []reviewCommentResponse
	if err := json.NewDecoder(response.Body).Decode(&comments); err != nil {
		_ = response.Body.Close()
		return nil, responseETag, false, fmt.Errorf("decode GitHub response: %w", err)
	}
	if err := response.Body.Close(); err != nil {
		return nil, responseETag, false, fmt.Errorf("close GitHub response: %w", err)
	}
	if len(comments) == 0 {
		return nil, responseETag, false, nil
	}
	comment := comments[0]
	occurredAt := comment.UpdatedAt
	if occurredAt.IsZero() {
		occurredAt = comment.CreatedAt
	}
	return &model.Activity{
		Kind:       "review_comment",
		Actor:      login(comment.User),
		BodyText:   normalizeActivityBody(comment.BodyText),
		OccurredAt: occurredAt,
		URL:        comment.HTMLURL,
	}, responseETag, false, nil
}

func (c *Client) fetchActivityBatch(
	ctx context.Context,
	ids []string,
) (map[string]graphQLActivityCandidates, error) {
	body, err := json.Marshal(graphQLActivityRequest{
		Query: latestActivitiesQuery,
		Variables: graphQLActivityVariables{
			IDs: ids,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("encode GitHub activity request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.graphqlURL.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("create GitHub activity request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
	request.Header.Set("User-Agent", "gh-workbench")

	response, err := c.do(request)
	if err != nil {
		return nil, fmt.Errorf("send GitHub activity request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, responseError(response)
	}

	var result graphQLActivityResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode GitHub activity response: %w", err)
	}
	if err := graphQLResponseError(response, result.Errors); err != nil {
		return nil, err
	}

	activities := make(
		map[string]graphQLActivityCandidates,
		len(result.Data.Nodes),
	)
	for _, node := range result.Data.Nodes {
		if node == nil || node.ID == "" {
			continue
		}
		activities[node.ID] = latestGraphQLActivities(node)
	}
	return activities, nil
}

type graphQLActivityCandidates struct {
	Latest        *model.Activity
	LatestCommit  *model.Activity
	LatestComment *model.Activity
	LatestReview  *model.Activity
}

func latestGraphQLActivities(
	node *graphQLActivityNode,
) graphQLActivityCandidates {
	var latest *model.Activity
	var latestComment *model.Activity
	for _, comment := range node.Comments.Nodes {
		occurredAt := comment.UpdatedAt
		if occurredAt.IsZero() {
			occurredAt = comment.CreatedAt
		}
		activity := &model.Activity{
			Kind:       "comment",
			Actor:      login(comment.Author),
			BodyText:   normalizeActivityBody(comment.BodyText),
			OccurredAt: occurredAt,
			URL:        comment.URL,
		}
		latestComment = laterActivity(latestComment, activity)
		latest = laterActivity(latest, activity)
	}
	for _, event := range node.TimelineItems.Nodes {
		var activity *model.Activity
		switch event.TypeName {
		case "LabeledEvent":
			activity = &model.Activity{
				Kind:       "labeled",
				Actor:      login(event.Actor),
				BodyText:   event.Label.Name,
				OccurredAt: event.CreatedAt,
				URL:        node.URL,
			}
		case "UnlabeledEvent":
			activity = &model.Activity{
				Kind:       "unlabeled",
				Actor:      login(event.Actor),
				BodyText:   event.Label.Name,
				OccurredAt: event.CreatedAt,
				URL:        node.URL,
			}
		case "PullRequestReview":
			activity = pullRequestReviewActivity(event)
		case "IssueComment":
			occurredAt := event.UpdatedAt
			if occurredAt.IsZero() {
				occurredAt = event.CreatedAt
			}
			activity = &model.Activity{
				Kind:       "comment",
				Actor:      login(event.Author),
				BodyText:   normalizeActivityBody(event.BodyText),
				OccurredAt: occurredAt,
				URL:        event.URL,
			}
		case "PullRequestCommit":
			activity = pullRequestCommitActivity(event, node.UpdatedAt)
		case "ReopenedEvent":
			activity = timelineStateActivity("reopened", node.URL, event)
		case "ReviewRequestedEvent":
			activity = timelineStateActivity("review_requested", node.URL, event)
		case "ReviewRequestRemovedEvent":
			activity = timelineStateActivity("review_request_removed", node.URL, event)
		case "ReadyForReviewEvent":
			activity = timelineStateActivity("ready_for_review", node.URL, event)
		case "ConvertToDraftEvent":
			activity = timelineStateActivity("converted_to_draft", node.URL, event)
		}
		latest = laterActivity(latest, activity)
	}
	var latestCommit *model.Activity
	for _, event := range node.LatestCommit.Nodes {
		latestCommit = pullRequestCommitActivity(
			event,
			event.Commit.CommittedDate,
		)
	}
	if latestCommit == nil && latest != nil && latest.Kind == "commit" {
		latestCommit = latest
	}
	var latestReview *model.Activity
	for _, event := range node.LatestReview.Nodes {
		latestReview = pullRequestReviewActivity(event)
	}
	return graphQLActivityCandidates{
		Latest:        latest,
		LatestCommit:  latestCommit,
		LatestComment: latestComment,
		LatestReview:  latestReview,
	}
}

func pullRequestReviewActivity(event graphQLTimelineEvent) *model.Activity {
	occurredAt := event.UpdatedAt
	if occurredAt.IsZero() {
		occurredAt = event.SubmittedAt
	}
	kind := "review"
	if state := normalizeGraphQLEnum(event.State); state != "" {
		kind += "_" + state
	}
	return &model.Activity{
		Kind:       kind,
		Actor:      login(event.Author),
		BodyText:   normalizeActivityBody(event.BodyText),
		OccurredAt: occurredAt,
		URL:        event.URL,
	}
}

func pullRequestCommitActivity(
	event graphQLTimelineEvent,
	occurredAt time.Time,
) *model.Activity {
	actor := event.Commit.Committer.User
	if actor == nil || actor.Login == "" {
		actor = event.Commit.Author.User
	}
	if occurredAt.IsZero() {
		occurredAt = event.Commit.CommittedDate
	}
	return &model.Activity{
		Kind:  "commit",
		Actor: login(actor),
		BodyText: normalizeActivityBody(
			event.Commit.AbbreviatedOID + " " + event.Commit.MessageHeadline,
		),
		OccurredAt: occurredAt,
		URL:        event.URL,
	}
}

func stabilizeCommitActivity(
	current *model.Activity,
	previous *model.Activity,
) *model.Activity {
	if sameCommitActivity(current, previous) {
		current.OccurredAt = previous.OccurredAt
	}
	return current
}

func sameCommitActivity(left *model.Activity, right *model.Activity) bool {
	return left != nil &&
		right != nil &&
		left.Kind == "commit" &&
		right.Kind == "commit" &&
		left.URL != "" &&
		left.URL == right.URL
}

func timelineStateActivity(
	kind string,
	url string,
	event graphQLTimelineEvent,
) *model.Activity {
	return &model.Activity{
		Kind:       kind,
		Actor:      login(event.Actor),
		OccurredAt: event.CreatedAt,
		URL:        url,
	}
}

func laterActivity(current, candidate *model.Activity) *model.Activity {
	if candidate == nil {
		return current
	}
	if current == nil || !candidate.OccurredAt.Before(current.OccurredAt) {
		return candidate
	}
	return current
}

func normalizeActivityBody(body string) string {
	normalized := strings.Join(strings.Fields(body), " ")
	runes := []rune(normalized)
	if len(runes) <= maxActivityRunes {
		return normalized
	}
	return string(runes[:maxActivityRunes-1]) + "…"
}

func graphQLResponseError(
	response *http.Response,
	graphQLErrors []graphQLError,
) error {
	if len(graphQLErrors) == 0 {
		return nil
	}
	messages := make([]string, 0, len(graphQLErrors))
	for _, graphQLError := range graphQLErrors {
		messages = append(messages, graphQLError.Message)
	}
	message := strings.Join(messages, "; ")
	if retryAt, limited := rateLimitRetry(response, message); limited {
		return &RateLimitError{
			Status:  response.Status,
			Message: message,
			Retry:   retryAt,
		}
	}
	return fmt.Errorf("GitHub GraphQL API returned errors: %s", message)
}

func (c *Client) endpoint(parts ...string) (*url.URL, error) {
	joined, err := url.JoinPath(c.baseURL.String(), parts...)
	if err != nil {
		return nil, fmt.Errorf("build GitHub API URL: %w", err)
	}
	endpoint, err := url.Parse(joined)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub API endpoint: %w", err)
	}
	return endpoint, nil
}

func fetchPaginated[T any](
	ctx context.Context,
	client *Client,
	firstURL *url.URL,
	etag string,
) ([]T, string, bool, error) {
	current := cloneURL(firstURL)
	responseETag := etag
	values := make([]T, 0)
	firstPage := true
	paginated := false

	for current != nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return nil, responseETag, false, fmt.Errorf("create GitHub request: %w", err)
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("X-GitHub-Api-Version", githubAPIVersion)
		request.Header.Set("User-Agent", "gh-workbench")
		if firstPage && etag != "" {
			request.Header.Set("If-None-Match", etag)
		}

		response, err := client.do(request)
		if err != nil {
			return nil, responseETag, false, fmt.Errorf("send GitHub request: %w", err)
		}
		if firstPage {
			if currentETag := response.Header.Get("ETag"); currentETag != "" {
				responseETag = currentETag
			}
		}
		if response.StatusCode == http.StatusNotModified {
			_ = response.Body.Close()
			return nil, responseETag, true, nil
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			err := responseError(response)
			_ = response.Body.Close()
			return nil, responseETag, false, err
		}

		var page []T
		if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
			_ = response.Body.Close()
			return nil, responseETag, false, fmt.Errorf("decode GitHub response: %w", err)
		}
		next, err := client.nextPage(response.Header.Get("Link"))
		if closeErr := response.Body.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close GitHub response: %w", closeErr)
		}
		if err != nil {
			return nil, responseETag, false, err
		}
		values = append(values, page...)
		if next != nil {
			paginated = true
		}
		current = next
		firstPage = false
	}
	if paginated || len(values) >= pageSize {
		// A page ETag cannot prove that later pages stayed unchanged.
		responseETag = ""
	}
	return values, responseETag, false, nil
}

func (c *Client) do(request *http.Request) (*http.Response, error) {
	if err := c.gate.wait(request.Context()); err != nil {
		return nil, fmt.Errorf("wait to send GitHub request: %w", err)
	}
	return c.httpClient.Do(request)
}

type requestGate struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func (g *requestGate) wait(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if delay := time.Until(g.next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	g.next = time.Now().Add(g.interval)
	return nil
}

func (c *Client) nextPage(linkHeader string) (*url.URL, error) {
	for _, part := range strings.Split(linkHeader, ",") {
		sections := strings.Split(part, ";")
		if len(sections) < 2 || !strings.Contains(part, `rel="next"`) {
			continue
		}
		rawURL := strings.TrimSpace(sections[0])
		rawURL = strings.TrimPrefix(rawURL, "<")
		rawURL = strings.TrimSuffix(rawURL, ">")
		next, err := url.Parse(rawURL)
		if err != nil {
			return nil, fmt.Errorf("parse GitHub pagination URL: %w", err)
		}
		if next.Scheme != c.baseURL.Scheme || next.Host != c.baseURL.Host {
			return nil, fmt.Errorf("validate GitHub pagination URL: unexpected origin")
		}
		return next, nil
	}
	return nil, nil
}

func responseError(response *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	if err != nil {
		return fmt.Errorf("GitHub API returned %s", response.Status)
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = response.Status
	}

	if retryAt, limited := rateLimitRetry(response, message); limited {
		return &RateLimitError{
			Status:  response.Status,
			Message: message,
			Retry:   retryAt,
		}
	}
	return fmt.Errorf("GitHub API returned %s: %s", response.Status, message)
}

func rateLimitRetry(response *http.Response, message string) (time.Time, bool) {
	now := time.Now().UTC()
	var retryAt time.Time

	if raw := response.Header.Get("Retry-After"); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
			retryAt = now.Add(time.Duration(seconds) * time.Second)
		} else if parsed, err := http.ParseTime(raw); err == nil {
			retryAt = parsed
		}
	}
	if raw := response.Header.Get("X-RateLimit-Reset"); raw != "" {
		if unix, err := strconv.ParseInt(raw, 10, 64); err == nil {
			resetAt := time.Unix(unix, 0).UTC()
			if resetAt.After(retryAt) {
				retryAt = resetAt
			}
		}
	}

	limited := response.StatusCode == http.StatusTooManyRequests ||
		response.Header.Get("X-RateLimit-Remaining") == "0" ||
		response.Header.Get("Retry-After") != "" ||
		(response.StatusCode == http.StatusForbidden &&
			strings.Contains(strings.ToLower(message), "rate limit"))
	if !limited {
		return time.Time{}, false
	}
	if retryAt.Before(now) {
		retryAt = now.Add(time.Minute)
	}
	return retryAt.Add(time.Second), true
}

func cloneURL(value *url.URL) *url.URL {
	cloned := *value
	return &cloned
}

const relevantItemsQuery = `
query RelevantOpenItems($query: String!, $after: String) {
  search(query: $query, type: ISSUE, first: 100, after: $after) {
    issueCount
    pageInfo {
      hasNextPage
      endCursor
    }
    nodes {
      __typename
      ... on Issue {
        id
        number
        title
        url
        state
        author {
          login
        }
        createdAt
        updatedAt
        repository {
          nameWithOwner
        }
        labels(first: 100, orderBy: {field: NAME, direction: ASC}) {
          nodes {
            name
            color
          }
        }
      }
      ... on PullRequest {
        id
        number
        title
        url
        state
        author {
          login
        }
        createdAt
        updatedAt
        repository {
          nameWithOwner
        }
        isDraft
        reviewDecision
        mergeStateStatus
        additions
        deletions
      }
    }
  }
}`

const latestActivitiesQuery = `
query LatestActivities($ids: [ID!]!) {
  nodes(ids: $ids) {
    __typename
    ... on Issue {
      id
      url
      comments(first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) {
        nodes {
          author {
            login
          }
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
        nodes {
          __typename
          ... on LabeledEvent {
            actor {
              login
            }
            createdAt
            label {
              name
            }
          }
          ... on UnlabeledEvent {
            actor {
              login
            }
            createdAt
            label {
              name
            }
          }
          ... on ReopenedEvent {
            actor {
              login
            }
            createdAt
          }
        }
      }
    }
    ... on PullRequest {
      id
      url
      updatedAt
      comments(first: 1, orderBy: {field: UPDATED_AT, direction: DESC}) {
        nodes {
          author {
            login
          }
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
          ...PullRequestReviewActivity
          ... on IssueComment {
            author {
              login
            }
            bodyText
            createdAt
            updatedAt
            url
          }
          ...PullRequestCommitActivity
          ... on LabeledEvent {
            actor {
              login
            }
            createdAt
            label {
              name
            }
          }
          ... on UnlabeledEvent {
            actor {
              login
            }
            createdAt
            label {
              name
            }
          }
          ... on ReopenedEvent {
            actor {
              login
            }
            createdAt
          }
          ... on ReviewRequestedEvent {
            actor {
              login
            }
            createdAt
          }
          ... on ReviewRequestRemovedEvent {
            actor {
              login
            }
            createdAt
          }
          ... on ReadyForReviewEvent {
            actor {
              login
            }
            createdAt
          }
          ... on ConvertToDraftEvent {
            actor {
              login
            }
            createdAt
          }
        }
      }
      latestCommit: timelineItems(last: 1, itemTypes: [
        PULL_REQUEST_COMMIT
      ]) {
        nodes {
          __typename
          ...PullRequestCommitActivity
        }
      }
      latestReview: timelineItems(last: 1, itemTypes: [
        PULL_REQUEST_REVIEW
      ]) {
        nodes {
          __typename
          ...PullRequestReviewActivity
        }
      }
    }
  }
}

fragment PullRequestReviewActivity on PullRequestReview {
  author {
    login
  }
  bodyText
  state
  submittedAt
  updatedAt
  url
}

fragment PullRequestCommitActivity on PullRequestCommit {
  url
  commit {
    abbreviatedOid
    committedDate
    messageHeadline
    author {
      user {
        login
      }
    }
    committer {
      user {
        login
      }
    }
  }
}`

type graphQLRequest struct {
	Query     string           `json:"query"`
	Variables graphQLVariables `json:"variables"`
}

type graphQLVariables struct {
	Query string  `json:"query"`
	After *string `json:"after"`
}

type graphQLActivityRequest struct {
	Query     string                   `json:"query"`
	Variables graphQLActivityVariables `json:"variables"`
}

type graphQLActivityVariables struct {
	IDs []string `json:"ids"`
}

type graphQLSearchResponse struct {
	Data struct {
		Search struct {
			IssueCount int `json:"issueCount"`
			PageInfo   struct {
				HasNextPage bool    `json:"hasNextPage"`
				EndCursor   *string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []graphQLSearchNode `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLActivityResponse struct {
	Data struct {
		Nodes []*graphQLActivityNode `json:"nodes"`
	} `json:"data"`
	Errors []graphQLError `json:"errors"`
}

type graphQLActivityNode struct {
	TypeName  string    `json:"__typename"`
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	UpdatedAt time.Time `json:"updatedAt"`
	Comments  struct {
		Nodes []graphQLComment `json:"nodes"`
	} `json:"comments"`
	TimelineItems struct {
		Nodes []graphQLTimelineEvent `json:"nodes"`
	} `json:"timelineItems"`
	LatestCommit struct {
		Nodes []graphQLTimelineEvent `json:"nodes"`
	} `json:"latestCommit"`
	LatestReview struct {
		Nodes []graphQLTimelineEvent `json:"nodes"`
	} `json:"latestReview"`
}

type graphQLComment struct {
	Author    *userResponse `json:"author"`
	BodyText  string        `json:"bodyText"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
	URL       string        `json:"url"`
}

type graphQLTimelineEvent struct {
	TypeName    string        `json:"__typename"`
	Actor       *userResponse `json:"actor"`
	Author      *userResponse `json:"author"`
	BodyText    string        `json:"bodyText"`
	State       string        `json:"state"`
	CreatedAt   time.Time     `json:"createdAt"`
	SubmittedAt time.Time     `json:"submittedAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
	URL         string        `json:"url"`
	Label       labelResponse `json:"label"`
	Commit      graphQLCommit `json:"commit"`
}

type graphQLCommit struct {
	AbbreviatedOID  string          `json:"abbreviatedOid"`
	CommittedDate   time.Time       `json:"committedDate"`
	MessageHeadline string          `json:"messageHeadline"`
	Author          graphQLGitActor `json:"author"`
	Committer       graphQLGitActor `json:"committer"`
}

type graphQLGitActor struct {
	User *userResponse `json:"user"`
}

type graphQLSearchNode struct {
	TypeName         string        `json:"__typename"`
	ID               string        `json:"id"`
	Number           int           `json:"number"`
	Title            string        `json:"title"`
	URL              string        `json:"url"`
	State            string        `json:"state"`
	Author           *userResponse `json:"author"`
	CreatedAt        time.Time     `json:"createdAt"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	IsDraft          bool          `json:"isDraft"`
	ReviewDecision   string        `json:"reviewDecision"`
	MergeStateStatus string        `json:"mergeStateStatus"`
	Additions        int           `json:"additions"`
	Deletions        int           `json:"deletions"`
	Labels           struct {
		Nodes []labelResponse `json:"nodes"`
	} `json:"labels"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

type graphQLError struct {
	Message string `json:"message"`
}

type userResponse struct {
	Login string `json:"login"`
}

type labelResponse struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type reactionResponse struct {
	ID        int64         `json:"id"`
	Content   string        `json:"content"`
	User      *userResponse `json:"user"`
	CreatedAt time.Time     `json:"created_at"`
}

type reviewCommentResponse struct {
	User      *userResponse `json:"user"`
	BodyText  string        `json:"body_text"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
	HTMLURL   string        `json:"html_url"`
}

func login(user *userResponse) string {
	if user == nil {
		return "ghost"
	}
	return user.Login
}

func repositoryFromNameWithOwner(host, nameWithOwner string) (model.Repository, error) {
	parts := strings.SplitN(nameWithOwner, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return model.Repository{}, fmt.Errorf(
			"decode GitHub repository name %q",
			nameWithOwner,
		)
	}
	return model.Repository{Host: host, Owner: parts[0], Name: parts[1]}, nil
}

func normalizeGraphQLEnum(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
