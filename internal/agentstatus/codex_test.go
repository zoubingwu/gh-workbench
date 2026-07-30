package agentstatus

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
	_ "modernc.org/sqlite"
)

func TestCodexSourceTracksLifecycleRecords(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	root := filepath.Join(t.TempDir(), ".codex")
	rollout := filepath.Join(root, "sessions", "2026", "07", "28", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	initial := "" +
		"{\"type\":\"response_item\",\"payload\":{\"text\":\"task_started is only text\"}}\n" +
		"{\"timestamp\":\"" + now.Format(time.RFC3339Nano) +
		"\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"turn-1\"}}\n" +
		"{\"type\":\"response_item\",\"payload\":{\"type\":\"function_call\"," +
		"\"name\":\"exec_command\",\"arguments\":\"{\\\"cmd\\\":\\\"go test ./...\\\"," +
		"\\\"workdir\\\":\\\"/workspace/rocket-worktree\\\"}\"}}\n"
	if err := os.WriteFile(rollout, []byte(initial), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	createCodexStateDB(t, root, rollout, "/workspace/rocket")

	source := newCodexSource(root)
	observations, err := source.scan(context.Background(), now)
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 1 {
		t.Fatalf("len(scan()) = %d, want 1", len(observations))
	}
	if observation := observations[0]; observation.provider != "codex" ||
		observation.sessionKey != "thread-1" ||
		observation.cwd != "/workspace/rocket-worktree" ||
		observation.state != model.LocalAgentStateWorking ||
		observation.confidence != model.LocalAgentConfidenceHeuristic {
		t.Fatalf("scan() observation = %#v", observation)
	}

	file, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open rollout: %v", err)
	}
	terminal := "{\"timestamp\":\"" + now.Add(time.Second).Format(time.RFC3339Nano) +
		"\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_complete\",\"turn_id\":\"turn-1\"," +
		"\"last_agent_message\":\"" + strings.Repeat("x", maxLifecycleLine) + "\"}}\n"
	if _, err := file.WriteString(terminal); err != nil {
		_ = file.Close()
		t.Fatalf("append rollout: %v", err)
	}
	nested := "{\"type\":\"response_item\",\"payload\":{\"type\":\"tool_output\",\"content\":" +
		"{\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\"}}}}\n"
	if _, err := file.WriteString(nested); err != nil {
		_ = file.Close()
		t.Fatalf("append nested fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close rollout: %v", err)
	}

	observations, err = source.scan(context.Background(), now.Add(time.Second))
	if err != nil {
		t.Fatalf("second scan() error = %v", err)
	}
	if len(observations) != 0 {
		t.Fatalf("second scan() = %#v, want no active observations", observations)
	}
}

func TestCodexSourceDropsStaleStartedTask(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	root := filepath.Join(t.TempDir(), ".codex")
	rollout := filepath.Join(root, "sessions", "rollout.jsonl")
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatalf("create sessions directory: %v", err)
	}
	record := "{\"timestamp\":\"" + now.Add(-time.Hour).Format(time.RFC3339Nano) +
		"\",\"type\":\"event_msg\",\"payload\":{\"type\":\"task_started\",\"turn_id\":\"turn-1\"}}\n"
	if err := os.WriteFile(rollout, []byte(record), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	stale := now.Add(-time.Hour)
	if err := os.Chtimes(rollout, stale, stale); err != nil {
		t.Fatalf("age rollout: %v", err)
	}
	createCodexStateDB(t, root, rollout, "/workspace/rocket")

	observations, err := newCodexSource(root).scan(context.Background(), now)
	if err != nil {
		t.Fatalf("scan() error = %v", err)
	}
	if len(observations) != 0 {
		t.Fatalf("scan() = %#v, want stale task removed", observations)
	}
}

func TestSQLiteReadOnlyDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		slashPath string
		want      string
	}{
		{
			name:      "Unix path",
			slashPath: "/Users/me/.codex/state_5.sqlite",
			want:      "file:///Users/me/.codex/state_5.sqlite?mode=ro",
		},
		{
			name:      "Windows drive path",
			slashPath: "C:/Users/me/.codex/state_5.sqlite",
			want:      "file:///C:/Users/me/.codex/state_5.sqlite?mode=ro",
		},
		{
			name:      "Windows UNC path",
			slashPath: "//server/share/.codex/state_5.sqlite",
			want:      "file:////server/share/.codex/state_5.sqlite?mode=ro",
		},
		{
			name:      "escaped path",
			slashPath: "/Users/me/Codex Data/state_5.sqlite",
			want:      "file:///Users/me/Codex%20Data/state_5.sqlite?mode=ro",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := sqliteReadOnlyDSNFromSlashPath(test.slashPath)
			if got != test.want {
				t.Fatalf(
					"sqliteReadOnlyDSNFromSlashPath(%q) = %q, want %q",
					test.slashPath,
					got,
					test.want,
				)
			}
		})
	}
}

func createCodexStateDB(t *testing.T, root, rollout, cwd string) {
	t.Helper()

	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create Codex root: %v", err)
	}
	database, err := sql.Open("sqlite", filepath.Join(root, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	defer func() {
		_ = database.Close()
	}()
	if _, err := database.Exec(`
		CREATE TABLE threads (
			id TEXT PRIMARY KEY,
			rollout_path TEXT NOT NULL,
			cwd TEXT NOT NULL,
			updated_at_ms INTEGER NOT NULL
		);
		INSERT INTO threads (id, rollout_path, cwd, updated_at_ms)
		VALUES (?, ?, ?, ?)
	`, "thread-1", rollout, cwd, time.Now().UnixMilli()); err != nil {
		t.Fatalf("create fixture database: %v", err)
	}
}
