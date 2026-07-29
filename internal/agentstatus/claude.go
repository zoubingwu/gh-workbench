package agentstatus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

const claudeCommandTimeout = 2 * time.Second

type commandOutput func(context.Context, string, ...string) ([]byte, error)

type claudeSource struct {
	configDir    string
	binary       string
	output       commandOutput
	processAlive func(int) bool
}

type claudeSession struct {
	CWD       string `json:"cwd"`
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	State     string `json:"state"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
	SessionID string `json:"sessionId"`
	StartedAt int64  `json:"startedAt"`
}

func (s *claudeSource) scan(
	ctx context.Context,
	_ time.Time,
) ([]observation, error) {
	processAlive := s.processAlive
	if processAlive == nil {
		processAlive = processIsAlive
	}
	markers, err := loadClaudeSessionMarkers(s.configDir, processAlive)
	if err != nil {
		return nil, err
	}
	if s.binary == "" {
		return markers, nil
	}

	output := s.output
	if output == nil {
		output = runCommand
	}
	statusContext, statusCancel := context.WithTimeout(ctx, claudeCommandTimeout)
	_, err = output(statusContext, s.binary, "daemon", "status")
	statusCancel()
	if err != nil {
		return markers, nil
	}

	commandContext, commandCancel := context.WithTimeout(ctx, claudeCommandTimeout)
	defer commandCancel()
	raw, err := output(commandContext, s.binary, "agents", "--json")
	if err != nil {
		return markers, nil
	}
	supported, err := parseClaudeAgentsJSON(raw)
	if err != nil {
		return markers, nil
	}
	return supported, nil
}

func loadClaudeSessionMarkers(
	configDir string,
	processAlive func(int) bool,
) ([]observation, error) {
	entries, err := os.ReadDir(filepath.Join(configDir, "sessions"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect Claude session markers: %w", err)
	}

	observations := make([]observation, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(configDir, "sessions", entry.Name())
		raw, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read Claude session marker: %w", err)
		}
		var session claudeSession
		if err := json.Unmarshal(raw, &session); err != nil {
			continue
		}
		if !processAlive(session.PID) {
			continue
		}
		if current, ok := claudeObservation(
			session,
			model.LocalAgentConfidenceHeuristic,
		); ok {
			observations = append(observations, current)
		}
	}
	return observations, nil
}

func parseClaudeAgentsJSON(raw []byte) ([]observation, error) {
	var sessions []claudeSession
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return nil, fmt.Errorf("decode Claude sessions: %w", err)
	}

	observations := make([]observation, 0, len(sessions))
	for _, session := range sessions {
		if current, ok := claudeObservation(
			session,
			model.LocalAgentConfidenceSupported,
		); ok {
			observations = append(observations, current)
		}
	}
	return observations, nil
}

func claudeObservation(
	session claudeSession,
	confidence model.LocalAgentConfidence,
) (observation, bool) {
	if session.CWD == "" {
		return observation{}, false
	}

	var state model.LocalAgentState
	switch {
	case session.Kind == "background" && session.State == "working":
		state = model.LocalAgentStateWorking
	case session.Kind == "background" && session.State == "blocked":
		state = model.LocalAgentStateNeedsInput
	case session.Status == "busy", session.Status == "working":
		state = model.LocalAgentStateWorking
	case session.Status == "waiting":
		state = model.LocalAgentStateNeedsInput
	default:
		return observation{}, false
	}
	return observation{
		provider:   "claude",
		sessionKey: claudeSessionKey(session),
		cwd:        session.CWD,
		state:      state,
		confidence: confidence,
	}, true
}

func claudeSessionKey(session claudeSession) string {
	switch {
	case session.SessionID != "":
		return session.SessionID
	case session.ID != "":
		return session.ID
	case session.PID != 0:
		return strconv.Itoa(session.PID)
	default:
		return session.CWD + ":" + strconv.FormatInt(session.StartedAt, 10)
	}
}

func runCommand(
	ctx context.Context,
	name string,
	arguments ...string,
) ([]byte, error) {
	return exec.CommandContext(ctx, name, arguments...).Output()
}
