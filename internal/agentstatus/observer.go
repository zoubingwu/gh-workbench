package agentstatus

import (
	"cmp"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

const (
	defaultScanInterval = 3 * time.Second
	sourceScanTimeout   = 2 * time.Second
	gitResolveTimeout   = 2 * time.Second
	gitIdentityCacheTTL = 15 * time.Second
)

type observation struct {
	provider   string
	sessionKey string
	cwd        string
	state      model.LocalAgentState
	confidence model.LocalAgentConfidence
}

type source interface {
	scan(context.Context, time.Time) ([]observation, error)
}

type gitIdentity struct {
	repositoryKey string
	branch        string
	sha           string
}

type resolvedObservation struct {
	provider   string
	sessionKey string
	state      model.LocalAgentState
	confidence model.LocalAgentConfidence
	identity   gitIdentity
}

type cachedGitIdentity struct {
	identity  gitIdentity
	valid     bool
	expiresAt time.Time
}

type gitResolver func(context.Context, string) (gitIdentity, error)

type Observer struct {
	mu         sync.RWMutex
	activities []resolvedObservation
	identities map[string]cachedGitIdentity
	sources    []source
	resolve    gitResolver
	interval   time.Duration
	onChange   func()
}

func New(onChange func()) *Observer {
	home, _ := os.UserHomeDir()
	codexRoot := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexRoot == "" && home != "" {
		codexRoot = filepath.Join(home, ".codex")
	}
	claudeRoot := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if claudeRoot == "" && home != "" {
		claudeRoot = filepath.Join(home, ".claude")
	}

	sources := make([]source, 0, 2)
	if codexRoot != "" {
		sources = append(sources, newCodexSource(codexRoot))
	}
	if claudeRoot != "" {
		binary, _ := exec.LookPath("claude")
		sources = append(sources, &claudeSource{
			configDir: claudeRoot,
			binary:    binary,
		})
	}
	return &Observer{
		sources:  sources,
		resolve:  resolveGitIdentity,
		interval: defaultScanInterval,
		onChange: onChange,
	}
}

func (o *Observer) Run(ctx context.Context) error {
	o.refresh(ctx, time.Now().UTC())
	interval := o.interval
	if interval <= 0 {
		interval = defaultScanInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			o.refresh(ctx, now.UTC())
		}
	}
}

func (o *Observer) refresh(ctx context.Context, now time.Time) {
	observations := make([]observation, 0)
	for _, source := range o.sources {
		scanContext, cancel := context.WithTimeout(ctx, sourceScanTimeout)
		current, err := source.scan(scanContext, now)
		cancel()
		if err == nil {
			observations = append(observations, current...)
		}
	}

	resolve := o.resolve
	if resolve == nil {
		resolve = resolveGitIdentity
	}
	if o.identities == nil {
		o.identities = make(map[string]cachedGitIdentity)
	}
	seenDirectories := make(map[string]struct{})
	activities := make([]resolvedObservation, 0, len(observations))
	for _, current := range observations {
		seenDirectories[current.cwd] = struct{}{}
		cached, ok := o.identities[current.cwd]
		if !ok || !now.Before(cached.expiresAt) {
			resolveContext, cancel := context.WithTimeout(ctx, gitResolveTimeout)
			identity, err := resolve(resolveContext, current.cwd)
			cancel()
			cached = cachedGitIdentity{
				identity:  identity,
				valid:     err == nil,
				expiresAt: now.Add(gitIdentityCacheTTL),
			}
			o.identities[current.cwd] = cached
		}
		if !cached.valid {
			continue
		}
		activities = append(activities, resolvedObservation{
			provider:   current.provider,
			sessionKey: current.sessionKey,
			state:      current.state,
			confidence: current.confidence,
			identity:   cached.identity,
		})
	}
	for directory := range o.identities {
		if _, ok := seenDirectories[directory]; !ok {
			delete(o.identities, directory)
		}
	}
	slices.SortFunc(activities, compareResolvedObservations)

	o.mu.Lock()
	changed := !slices.Equal(o.activities, activities)
	o.activities = activities
	o.mu.Unlock()
	if changed && o.onChange != nil {
		o.onChange()
	}
}

func compareResolvedObservations(a, b resolvedObservation) int {
	for _, comparison := range []int{
		cmp.Compare(a.provider, b.provider),
		cmp.Compare(a.sessionKey, b.sessionKey),
		cmp.Compare(a.identity.repositoryKey, b.identity.repositoryKey),
		cmp.Compare(a.identity.branch, b.identity.branch),
		cmp.Compare(a.identity.sha, b.identity.sha),
	} {
		if comparison != 0 {
			return comparison
		}
	}
	return 0
}

func (o *Observer) Decorate(items []model.WorkItem) {
	o.mu.RLock()
	activities := slices.Clone(o.activities)
	o.mu.RUnlock()

	for index := range items {
		items[index].LocalAgentActivity = nil
		if items[index].Kind != model.ItemKindPullRequest {
			continue
		}

		matches := make([]resolvedObservation, 0)
		for _, activity := range activities {
			if matchesPullRequest(items[index], activity.identity) {
				matches = append(matches, activity)
			}
		}
		items[index].LocalAgentActivity = aggregate(matches)
	}
}

func matchesPullRequest(item model.WorkItem, identity gitIdentity) bool {
	repositoryMatches := identity.repositoryKey != "" &&
		strings.EqualFold(item.HeadRepositoryKey, identity.repositoryKey)
	if repositoryMatches &&
		identity.branch != "" &&
		item.HeadRefName == identity.branch {
		return true
	}
	if identity.sha == "" || item.HeadRefOID != identity.sha {
		return false
	}
	return repositoryMatches ||
		(identity.repositoryKey != "" &&
			strings.EqualFold(item.RepositoryKey, identity.repositoryKey))
}

func aggregate(activities []resolvedObservation) *model.LocalAgentActivity {
	if len(activities) == 0 {
		return nil
	}

	state := model.LocalAgentStateWorking
	for _, activity := range activities {
		if activity.state == model.LocalAgentStateNeedsInput {
			state = model.LocalAgentStateNeedsInput
			break
		}
	}

	confidence := model.LocalAgentConfidenceHeuristic
	providerSet := make(map[string]struct{})
	sessionSet := make(map[string]struct{})
	for _, activity := range activities {
		if activity.state != state {
			continue
		}
		providerSet[activity.provider] = struct{}{}
		sessionSet[activity.provider+"\x00"+activity.sessionKey] = struct{}{}
		if activity.confidence == model.LocalAgentConfidenceSupported {
			confidence = model.LocalAgentConfidenceSupported
		}
	}

	providers := make([]string, 0, len(providerSet))
	for provider := range providerSet {
		providers = append(providers, provider)
	}
	slices.Sort(providers)

	return &model.LocalAgentActivity{
		State:        state,
		Providers:    providers,
		SessionCount: len(sessionSet),
		Confidence:   confidence,
	}
}
