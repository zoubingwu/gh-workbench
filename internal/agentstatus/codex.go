package agentstatus

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
	_ "modernc.org/sqlite"
)

const (
	codexCandidateLimit  = 32
	codexCandidateWindow = 24 * time.Hour
	codexInitialTail     = int64(8 << 20)
	codexStaleAfter      = 30 * time.Minute
	maxLifecycleLine     = 64 << 10
)

type codexSource struct {
	root  string
	files map[string]rolloutState
}

type codexCandidate struct {
	id      string
	rollout string
	cwd     string
}

type rolloutState struct {
	offset      int64
	active      bool
	skipPartial bool
	cwd         string
}

func newCodexSource(root string) *codexSource {
	return &codexSource{
		root:  root,
		files: make(map[string]rolloutState),
	}
}

func (s *codexSource) scan(
	ctx context.Context,
	now time.Time,
) ([]observation, error) {
	candidates, err := s.loadCandidates(ctx, now)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	observations := make([]observation, 0)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !pathWithinRoot(s.root, candidate.rollout) {
			continue
		}
		seen[candidate.rollout] = struct{}{}
		state := s.files[candidate.rollout]
		info, err := os.Stat(candidate.rollout)
		if errors.Is(err, os.ErrNotExist) {
			delete(s.files, candidate.rollout)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect Codex rollout: %w", err)
		}
		if !info.Mode().IsRegular() {
			delete(s.files, candidate.rollout)
			continue
		}
		if info.Size() < state.offset {
			state = rolloutState{}
		}
		if state.offset == 0 && info.Size() > codexInitialTail {
			state.offset = info.Size() - codexInitialTail
			state.skipPartial = true
		}
		state, err = scanRollout(ctx, candidate.rollout, state)
		if err != nil {
			return nil, fmt.Errorf("scan Codex rollout: %w", err)
		}
		s.files[candidate.rollout] = state
		info, err = os.Stat(candidate.rollout)
		if err != nil {
			return nil, fmt.Errorf("reinspect Codex rollout: %w", err)
		}
		if state.active && now.Sub(info.ModTime()) <= codexStaleAfter {
			workingDirectory := state.cwd
			if workingDirectory == "" {
				workingDirectory = candidate.cwd
			}
			observations = append(observations, observation{
				provider:   "codex",
				sessionKey: candidate.id,
				cwd:        workingDirectory,
				state:      model.LocalAgentStateWorking,
				confidence: model.LocalAgentConfidenceHeuristic,
			})
		}
	}
	for path := range s.files {
		if _, ok := seen[path]; !ok {
			delete(s.files, path)
		}
	}
	return observations, nil
}

func (s *codexSource) loadCandidates(
	ctx context.Context,
	now time.Time,
) (_ []codexCandidate, returnErr error) {
	path := filepath.Join(s.root, "state_5.sqlite")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}

	database, err := sql.Open("sqlite", sqliteReadOnlyDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open Codex state database: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, database.Close())
	}()
	database.SetMaxOpenConns(1)

	rows, err := database.QueryContext(
		ctx,
		`SELECT id, rollout_path, cwd
		FROM threads
		WHERE rollout_path <> ''
			AND cwd <> ''
			AND updated_at_ms >= ?
		ORDER BY updated_at_ms DESC
		LIMIT ?`,
		now.Add(-codexCandidateWindow).UnixMilli(),
		codexCandidateLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("query Codex threads: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, rows.Close())
	}()

	candidates := make([]codexCandidate, 0)
	for rows.Next() {
		var candidate codexCandidate
		if err := rows.Scan(
			&candidate.id,
			&candidate.rollout,
			&candidate.cwd,
		); err != nil {
			return nil, fmt.Errorf("scan Codex thread: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Codex threads: %w", err)
	}
	return candidates, nil
}

func sqliteReadOnlyDSN(path string) string {
	return sqliteReadOnlyDSNFromSlashPath(filepath.ToSlash(path))
}

func sqliteReadOnlyDSNFromSlashPath(path string) string {
	databaseURL := &url.URL{Scheme: "file"}
	switch {
	case isWindowsDrivePath(path):
		databaseURL.Path = "/" + path
	default:
		databaseURL.Path = path
	}
	query := databaseURL.Query()
	query.Set("mode", "ro")
	databaseURL.RawQuery = query.Encode()
	return databaseURL.String()
}

func isWindowsDrivePath(path string) bool {
	if len(path) < 3 || path[1] != ':' || path[2] != '/' {
		return false
	}
	drive := path[0]
	return drive >= 'A' && drive <= 'Z' || drive >= 'a' && drive <= 'z'
}

func scanRollout(
	ctx context.Context,
	path string,
	state rolloutState,
) (_ rolloutState, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return rolloutState{}, err
	}
	defer func() {
		returnErr = errors.Join(returnErr, file.Close())
	}()
	if _, err := file.Seek(state.offset, io.SeekStart); err != nil {
		return rolloutState{}, err
	}

	reader := bufio.NewReaderSize(file, maxLifecycleLine)
	consumed := state.offset
	completeOffset := state.offset
	line := make([]byte, 0, 1024)
	skipPartial := state.skipPartial
	for {
		if err := ctx.Err(); err != nil {
			return rolloutState{}, err
		}
		fragment, readErr := reader.ReadSlice('\n')
		consumed += int64(len(fragment))
		if remaining := maxLifecycleLine - len(line); remaining > 0 {
			line = append(line, fragment[:min(remaining, len(fragment))]...)
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			continue
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return rolloutState{}, readErr
		}
		if len(fragment) > 0 && fragment[len(fragment)-1] == '\n' {
			if !skipPartial {
				active, relevant := parseLifecycle(line)
				if relevant {
					state.active = active
					if active {
						state.cwd = ""
					}
				}
				if workingDirectory, ok := parseToolWorkingDirectory(line); ok {
					state.active = true
					state.cwd = workingDirectory
				}
			}
			skipPartial = false
			state.skipPartial = false
			line = line[:0]
			completeOffset = consumed
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
	}
	state.offset = completeOffset
	return state, nil
}

func parseLifecycle(line []byte) (active bool, relevant bool) {
	decoder := json.NewDecoder(bytes.NewReader(line))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false, false
	}

	outerType := ""
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false, false
		}
		key, ok := token.(string)
		if !ok {
			return false, false
		}
		switch key {
		case "type":
			if err := decoder.Decode(&outerType); err != nil {
				return false, false
			}
		case "payload":
			if outerType != "event_msg" {
				return false, false
			}
			payloadType, ok := decodePayloadType(decoder)
			if !ok {
				return false, false
			}
			switch payloadType {
			case "task_started":
				return true, true
			case "task_complete", "turn_aborted":
				return false, true
			default:
				return false, false
			}
		default:
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return false, false
			}
		}
	}
	return false, false
}

func decodePayloadType(decoder *json.Decoder) (string, bool) {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", false
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return "", false
		}
		key, ok := token.(string)
		if !ok {
			return "", false
		}
		if key == "type" {
			var payloadType string
			if err := decoder.Decode(&payloadType); err != nil {
				return "", false
			}
			return payloadType, true
		}
		var ignored json.RawMessage
		if err := decoder.Decode(&ignored); err != nil {
			return "", false
		}
	}
	return "", false
}

func parseToolWorkingDirectory(line []byte) (string, bool) {
	var envelope struct {
		Type    string `json:"type"`
		Payload struct {
			Type      string          `json:"type"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil ||
		envelope.Type != "response_item" ||
		envelope.Payload.Type != "function_call" ||
		envelope.Payload.Name != "exec_command" {
		return "", false
	}

	var encoded string
	if err := json.Unmarshal(envelope.Payload.Arguments, &encoded); err != nil {
		return "", false
	}
	var arguments struct {
		Workdir string `json:"workdir"`
		CWD     string `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(encoded), &arguments); err != nil {
		return "", false
	}
	workingDirectory := arguments.Workdir
	if workingDirectory == "" {
		workingDirectory = arguments.CWD
	}
	if !filepath.IsAbs(workingDirectory) {
		return "", false
	}
	return filepath.Clean(workingDirectory), true
}

func pathWithinRoot(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return relative != ".." &&
		relative != "." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
