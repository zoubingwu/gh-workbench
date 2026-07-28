package agentstatus

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

func TestParseClaudeAgentsJSON(t *testing.T) {
	t.Parallel()

	raw := []byte(`[
		{
			"cwd": "/workspace/rocket",
			"kind": "background",
			"id": "job-1",
			"state": "working",
			"startedAt": 1785232800000
		},
		{
			"cwd": "/workspace/rocket",
			"kind": "background",
			"id": "job-2",
			"state": "blocked",
			"startedAt": 1785232801000
		},
		{
			"cwd": "/workspace/satellite",
			"kind": "background",
			"id": "job-3",
			"state": "done",
			"startedAt": 1785232802000
		},
		{
			"cwd": "/workspace/interactive",
			"kind": "interactive",
			"sessionId": "session-1",
			"status": "busy",
			"startedAt": 1785232803000
		}
	]`)

	observations, err := parseClaudeAgentsJSON(raw)
	if err != nil {
		t.Fatalf("parseClaudeAgentsJSON() error = %v", err)
	}
	if len(observations) != 3 {
		t.Fatalf("len(parseClaudeAgentsJSON()) = %d, want 3", len(observations))
	}
	got := []model.LocalAgentState{
		observations[0].state,
		observations[1].state,
		observations[2].state,
	}
	want := []model.LocalAgentState{
		model.LocalAgentStateWorking,
		model.LocalAgentStateNeedsInput,
		model.LocalAgentStateWorking,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Claude states = %q, want %q", got, want)
	}
	for _, observation := range observations {
		if observation.provider != "claude" ||
			observation.confidence != model.LocalAgentConfidenceSupported {
			t.Fatalf("Claude observation = %#v", observation)
		}
	}
	if observations[2].sessionKey != "session-1" {
		t.Fatalf("interactive session key = %q, want session-1", observations[2].sessionKey)
	}
}

func TestClaudeSourceReadsForegroundSessionMarkers(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	markers := map[string]string{
		"101.json": `{
			"pid": 101,
			"sessionId": "session-busy",
			"cwd": "/workspace/rocket",
			"kind": "interactive",
			"status": "busy"
		}`,
		"102.json": `{
			"pid": 102,
			"sessionId": "session-waiting",
			"cwd": "/workspace/satellite",
			"kind": "interactive",
			"status": "waiting"
		}`,
		"103.json": `{
			"pid": 103,
			"sessionId": "session-idle",
			"cwd": "/workspace/idle",
			"kind": "interactive",
			"status": "idle"
		}`,
	}
	for name, contents := range markers {
		if err := os.WriteFile(
			filepath.Join(sessionsDir, name),
			[]byte(contents),
			0o600,
		); err != nil {
			t.Fatalf("write session marker: %v", err)
		}
	}

	observations, err := (&claudeSource{
		configDir: configDir,
		processAlive: func(int) bool {
			return true
		},
	}).scan(
		context.Background(),
		time.Now(),
	)
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 2 {
		t.Fatalf("scan() = %#v", observations)
	}
	if observations[0].state != model.LocalAgentStateWorking ||
		observations[1].state != model.LocalAgentStateNeedsInput {
		t.Fatalf("marker states = %#v", observations)
	}
	for _, observation := range observations {
		if observation.confidence != model.LocalAgentConfidenceHeuristic {
			t.Fatalf("marker confidence = %q", observation.confidence)
		}
	}
}

func TestClaudeSourceRejectsDeadSessionMarkers(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "101.json"), []byte(`{
		"pid": 101,
		"sessionId": "session-dead",
		"cwd": "/workspace/rocket",
		"kind": "interactive",
		"status": "busy"
	}`), 0o600); err != nil {
		t.Fatalf("write session marker: %v", err)
	}
	checkedPID := 0
	source := &claudeSource{
		configDir: configDir,
		processAlive: func(pid int) bool {
			checkedPID = pid
			return false
		},
	}

	observations, err := source.scan(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 0 || checkedPID != 101 {
		t.Fatalf("scan() = %#v, checked PID = %d", observations, checkedPID)
	}
}

func TestClaudeSourceQueriesRunningSupervisorWithoutSessionMarker(t *testing.T) {
	t.Parallel()

	calls := 0
	source := &claudeSource{
		configDir: t.TempDir(),
		binary:    "/usr/local/bin/claude",
		output: func(
			_ context.Context,
			name string,
			arguments ...string,
		) ([]byte, error) {
			calls++
			if name != "/usr/local/bin/claude" {
				t.Fatalf("Claude command = %q %q", name, arguments)
			}
			switch calls {
			case 1:
				if !slices.Equal(arguments, []string{"daemon", "status"}) {
					t.Fatalf("Claude status command arguments = %q", arguments)
				}
				return []byte("running"), nil
			case 2:
				if !slices.Equal(arguments, []string{"agents", "--json"}) {
					t.Fatalf("Claude agents command arguments = %q", arguments)
				}
				return []byte(`[{
					"cwd": "/workspace/rocket",
					"kind": "background",
					"id": "job-1",
					"state": "working",
					"startedAt": 1785232800000
				}]`), nil
			default:
				t.Fatalf("unexpected Claude command call %d", calls)
				return nil, nil
			}
		},
	}

	observations, err := source.scan(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 1 || calls != 2 {
		t.Fatalf("scan() = %#v, calls = %d", observations, calls)
	}
}

func TestClaudeSourceKeepsMarkersWhenSupervisorIsUnavailable(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "101.json"), []byte(`{
		"pid": 101,
		"sessionId": "session-busy",
		"cwd": "/workspace/rocket",
		"kind": "interactive",
		"status": "busy"
	}`), 0o600); err != nil {
		t.Fatalf("write session marker: %v", err)
	}
	calls := 0
	source := &claudeSource{
		configDir: configDir,
		binary:    "/usr/local/bin/claude",
		processAlive: func(int) bool {
			return true
		},
		output: func(
			_ context.Context,
			_ string,
			arguments ...string,
		) ([]byte, error) {
			calls++
			if !slices.Equal(arguments, []string{"daemon", "status"}) {
				t.Fatalf("Claude command arguments = %q", arguments)
			}
			return nil, errors.New("supervisor unavailable")
		},
	}

	observations, err := source.scan(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 1 ||
		observations[0].confidence != model.LocalAgentConfidenceHeuristic ||
		calls != 1 {
		t.Fatalf("scan() = %#v, calls = %d", observations, calls)
	}
}

func TestClaudeSourcePrefersSupportedSessionState(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "101.json"), []byte(`{
		"pid": 101,
		"sessionId": "session-1",
		"cwd": "/workspace/rocket",
		"kind": "interactive",
		"status": "busy"
	}`), 0o600); err != nil {
		t.Fatalf("write session marker: %v", err)
	}
	source := &claudeSource{
		configDir: configDir,
		binary:    "/usr/local/bin/claude",
		processAlive: func(int) bool {
			return true
		},
		output: func(
			_ context.Context,
			_ string,
			arguments ...string,
		) ([]byte, error) {
			if slices.Equal(arguments, []string{"daemon", "status"}) {
				return []byte("running"), nil
			}
			return []byte(`[{
				"cwd": "/workspace/rocket",
				"kind": "background",
				"id": "job-1",
				"sessionId": "session-1",
				"state": "blocked"
			}]`), nil
		},
	}

	observations, err := source.scan(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 1 ||
		observations[0].state != model.LocalAgentStateNeedsInput ||
		observations[0].confidence != model.LocalAgentConfidenceSupported {
		t.Fatalf("scan() = %#v", observations)
	}
}

func TestClaudeSourceReconcilesSuccessfulOfficialSnapshot(t *testing.T) {
	t.Parallel()

	configDir := t.TempDir()
	sessionsDir := filepath.Join(configDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "101.json"), []byte(`{
		"pid": 101,
		"sessionId": "session-stale",
		"cwd": "/workspace/rocket",
		"kind": "interactive",
		"status": "busy"
	}`), 0o600); err != nil {
		t.Fatalf("write session marker: %v", err)
	}
	source := &claudeSource{
		configDir: configDir,
		binary:    "/usr/local/bin/claude",
		processAlive: func(int) bool {
			return true
		},
		output: func(
			_ context.Context,
			_ string,
			arguments ...string,
		) ([]byte, error) {
			if slices.Equal(arguments, []string{"daemon", "status"}) {
				return []byte("running"), nil
			}
			return []byte(`[]`), nil
		},
	}

	observations, err := source.scan(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 0 {
		t.Fatalf("scan() = %#v, want authoritative empty snapshot", observations)
	}
}
