package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/zoubingwu/gh-workbench/internal/agentstatus"
	"github.com/zoubingwu/gh-workbench/internal/github"
	"github.com/zoubingwu/gh-workbench/internal/model"
	"github.com/zoubingwu/gh-workbench/internal/notification"
	"github.com/zoubingwu/gh-workbench/internal/server"
	"github.com/zoubingwu/gh-workbench/internal/store"
	"github.com/zoubingwu/gh-workbench/internal/syncer"
	"github.com/zoubingwu/gh-workbench/internal/tui"
	"golang.org/x/term"
)

const (
	githubTimeout  = 30 * time.Second
	publishDelay   = 200 * time.Millisecond
	shutdownPeriod = 5 * time.Second
	workerCount    = 4
)

type Options struct {
	Browser bool
	DataDir string
	NoOpen  bool
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

func Run(ctx context.Context, options Options) error {
	if options.Stdin == nil {
		options.Stdin = os.Stdin
	}
	if options.Stdout == nil {
		options.Stdout = os.Stdout
	}
	if options.Stderr == nil {
		options.Stderr = os.Stderr
	}
	if options.NoOpen && !options.Browser {
		return fmt.Errorf("--no-open requires --browser")
	}
	if !options.Browser {
		if err := validateTUIStreams(options.Stdin, options.Stdout); err != nil {
			return err
		}
	}

	host, _ := auth.DefaultHost()
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("resolve active gh host: host is empty")
	}
	token, err := keyringToken(ctx, host)
	if err != nil {
		return err
	}
	httpClient, err := api.NewHTTPClient(api.ClientOptions{
		AuthToken: token,
		Host:      host,
		Timeout:   githubTimeout,
	})
	if err != nil {
		return fmt.Errorf("create GitHub client for %s: %w", host, err)
	}
	githubClient, err := github.New(httpClient, host)
	if err != nil {
		return err
	}
	viewer, err := githubClient.FetchViewer(ctx)
	if err != nil {
		return fmt.Errorf("load active gh account for %s: %w", host, err)
	}

	dataDir, err := resolveDataDir(options.DataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}

	databasePath := filepath.Join(dataDir, databaseFilename(host, viewer))
	database, err := store.Open(databasePath)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := os.Chmod(databasePath, 0o600); err != nil {
		return fmt.Errorf("protect database file: %w", err)
	}
	if err := database.EnsureAccount(ctx, host, time.Now().UTC()); err != nil {
		return err
	}

	snapshotUpdates := make(chan struct{}, 1)
	notifySnapshot := func() {
		select {
		case snapshotUpdates <- struct{}{}:
		default:
		}
	}
	agentObserver := agentstatus.New(notifySnapshot)
	runner := syncer.New(
		database,
		githubClient,
		host,
		viewer,
		workerCount,
		notifySnapshot,
	)
	var notificationWarning sync.Once
	reportNotificationError := func(err error) {
		notificationWarning.Do(func() {
			_, _ = fmt.Fprintf(
				options.Stderr,
				"System notifications are unavailable: %v\n",
				err,
			)
		})
	}
	notificationManager := notification.New(notification.SystemSender())
	observeNotifications := func(
		observeContext context.Context,
		snapshot model.Snapshot,
	) {
		if err := notificationManager.Observe(
			observeContext,
			snapshot,
		); err != nil {
			reportNotificationError(err)
		}
	}
	baselineContext, baselineCancel := context.WithTimeout(
		ctx,
		shutdownPeriod,
	)
	baselineItems, err := database.NotificationBaselineItems(
		baselineContext,
		host,
	)
	baselineCancel()
	if err != nil {
		return fmt.Errorf("load notification baseline: %w", err)
	}
	notificationManager.Seed(baselineItems)

	if !options.Browser {
		source := &terminalSnapshotSource{
			database:               database,
			runner:                 runner,
			decorator:              agentObserver,
			host:                   host,
			viewer:                 viewer,
			notificationsSupported: notification.Supported,
			observeNotifications:   observeNotifications,
			signalSnapshotUpdate:   notifySnapshot,
		}
		return runTerminalUI(
			ctx,
			options,
			runner,
			agentObserver,
			source,
			snapshotUpdates,
		)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	defer listener.Close()

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	var snapshotWarning sync.Once
	reportSnapshotError := func(err error) {
		snapshotWarning.Do(func() {
			_, _ = fmt.Fprintf(
				options.Stderr,
				"Snapshot publication failed: %v\n",
				err,
			)
		})
	}

	localServer, err := server.New(
		database,
		runner,
		host,
		viewer,
		notification.Supported,
		agentObserver,
		observeNotifications,
	)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Handler:           localServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	initialContext, initialCancel := context.WithTimeout(
		runContext,
		shutdownPeriod,
	)
	_, err = localServer.PublishSnapshot(initialContext)
	initialCancel()
	if err != nil {
		return fmt.Errorf("load initial workbench snapshot: %w", err)
	}

	results := make(chan error, 4)
	go func() {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		results <- err
	}()
	go func() {
		results <- runner.Run(runContext)
	}()
	go func() {
		results <- agentObserver.Run(runContext)
	}()
	go func() {
		publish := func() {
			publishContext, publishCancel := context.WithTimeout(
				runContext,
				shutdownPeriod,
			)
			defer publishCancel()

			_, err := localServer.PublishSnapshot(publishContext)
			if err != nil {
				reportSnapshotError(err)
			}
		}
		results <- publishSnapshots(
			runContext,
			snapshotUpdates,
			publish,
		)
	}()

	baseURL := "http://" + listener.Addr().String()
	sessionURL := baseURL + localServer.SessionPath()
	_, _ = fmt.Fprintf(
		options.Stdout,
		"GitHub Workbench is running for %s at %s\n",
		viewer+"@"+host,
		baseURL,
	)
	if options.NoOpen {
		_, _ = fmt.Fprintf(options.Stdout, "Open %s\n", sessionURL)
	} else {
		launcher := browser.New("", options.Stdout, options.Stderr)
		if err := launcher.Browse(sessionURL); err != nil {
			_, _ = fmt.Fprintf(
				options.Stderr,
				"Browser launch failed: %v\nOpen %s\n",
				err,
				sessionURL,
			)
		}
	}

	var runErr error
	received := 0
	select {
	case <-ctx.Done():
	case runErr = <-results:
		received = 1
	}

	cancel()
	shutdownContext, shutdownCancel := context.WithTimeout(
		context.Background(),
		shutdownPeriod,
	)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil && runErr == nil {
		runErr = fmt.Errorf("shut down local server: %w", err)
	}

	for received < 4 {
		select {
		case err := <-results:
			received++
			if err != nil && runErr == nil {
				runErr = err
			}
		case <-shutdownContext.Done():
			if runErr == nil {
				runErr = fmt.Errorf("stop GitHub Workbench: %w", shutdownContext.Err())
			}
			return runErr
		}
	}
	return runErr
}

type terminalSnapshotSource struct {
	mu                     sync.Mutex
	database               *store.Store
	runner                 *syncer.Runner
	decorator              server.ItemDecorator
	host                   string
	viewer                 string
	notificationsSupported bool
	observeNotifications   func(context.Context, model.Snapshot)
	signalSnapshotUpdate   func()
}

func (s *terminalSnapshotSource) Snapshot(
	ctx context.Context,
) (model.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot, err := s.database.Snapshot(
		ctx,
		s.host,
		s.runner.Running(),
		time.Now().UTC(),
	)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot.Viewer = s.viewer
	snapshot.Notifications.Supported = s.notificationsSupported
	if s.decorator != nil {
		s.decorator.Decorate(snapshot.Items)
	}
	if s.observeNotifications != nil {
		s.observeNotifications(ctx, snapshot)
	}
	return snapshot, nil
}

func (s *terminalSnapshotSource) UpdateNotificationPreferences(
	ctx context.Context,
	update model.NotificationPreferencesUpdate,
) error {
	s.mu.Lock()
	err := s.database.UpdateNotificationPreferences(
		ctx,
		update,
	)
	s.mu.Unlock()
	if err != nil {
		return fmt.Errorf("save notification settings: %w", err)
	}
	if s.signalSnapshotUpdate != nil {
		s.signalSnapshotUpdate()
	}
	return nil
}

func runTerminalUI(
	ctx context.Context,
	options Options,
	runner *syncer.Runner,
	agentObserver *agentstatus.Observer,
	source *terminalSnapshotSource,
	snapshotUpdates <-chan struct{},
) error {
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	launcher := browser.New("", io.Discard, io.Discard)
	results := make(chan error, 3)
	go func() {
		results <- runner.Run(runContext)
	}()
	go func() {
		results <- agentObserver.Run(runContext)
	}()
	go func() {
		results <- tui.Run(runContext, tui.Options{
			Source:                        source,
			Updates:                       snapshotUpdates,
			Trigger:                       runner.Trigger,
			UpdateNotificationPreferences: source.UpdateNotificationPreferences,
			OpenURL:                       launcher.Browse,
			Input:                         options.Stdin,
			Output:                        options.Stdout,
		})
	}()

	var runErr error
	received := 0
	select {
	case <-ctx.Done():
	case runErr = <-results:
		received = 1
	}

	cancel()
	shutdownContext, shutdownCancel := context.WithTimeout(
		context.Background(),
		shutdownPeriod,
	)
	defer shutdownCancel()
	for received < 3 {
		select {
		case err := <-results:
			received++
			if err != nil && runErr == nil {
				runErr = err
			}
		case <-shutdownContext.Done():
			if runErr == nil {
				runErr = fmt.Errorf(
					"stop GitHub Workbench: %w",
					shutdownContext.Err(),
				)
			}
			return runErr
		}
	}
	return runErr
}

type fileDescriptor interface {
	Fd() uintptr
}

func validateTUIStreams(input io.Reader, output io.Writer) error {
	inputFile, inputOK := input.(fileDescriptor)
	outputFile, outputOK := output.(fileDescriptor)
	if !inputOK ||
		!outputOK ||
		!term.IsTerminal(int(inputFile.Fd())) ||
		!term.IsTerminal(int(outputFile.Fd())) {
		return fmt.Errorf(
			"terminal UI requires an interactive terminal on stdin and stdout",
		)
	}
	return nil
}

func publishSnapshots(
	ctx context.Context,
	updates <-chan struct{},
	publish func(),
) error {
	var (
		timer  *time.Timer
		timerC <-chan time.Time
	)
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return nil
		case <-updates:
			if timerC == nil {
				timer = time.NewTimer(publishDelay)
				timerC = timer.C
			}
		case <-timerC:
			publish()
			timer = nil
			timerC = nil
		}
	}
}

func keyringToken(ctx context.Context, host string) (string, error) {
	return readKeyringToken(
		ctx,
		host,
		os.Environ(),
		func(
			ctx context.Context,
			arguments []string,
			environment []string,
		) ([]byte, error) {
			command := exec.CommandContext(ctx, "gh", arguments...)
			command.Env = environment
			return command.Output()
		},
	)
}

type commandOutput func(
	context.Context,
	[]string,
	[]string,
) ([]byte, error)

func readKeyringToken(
	ctx context.Context,
	host string,
	environment []string,
	output commandOutput,
) (string, error) {
	arguments := []string{
		"auth",
		"token",
		"--secure-storage",
		"--hostname",
		host,
	}
	raw, err := output(ctx, arguments, withoutTokenEnvironment(environment))
	if err != nil {
		return "", fmt.Errorf(
			"read active gh keyring token for %s; run gh auth login --hostname %s: %w",
			host,
			host,
			err,
		)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf(
			"load active gh token for %s from secure storage: token is empty",
			host,
		)
	}
	return token, nil
}

func withoutTokenEnvironment(environment []string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GH_TOKEN",
			"GITHUB_TOKEN",
			"GH_ENTERPRISE_TOKEN",
			"GITHUB_ENTERPRISE_TOKEN":
			continue
		default:
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func databaseFilename(host, viewer string) string {
	sum := sha256.Sum256([]byte(host + "\x00" + viewer))
	return fmt.Sprintf("workbench-%x.db", sum[:8])
}

func resolveDataDir(value string) (string, error) {
	if value != "" {
		return filepath.Abs(value)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find user cache directory: %w", err)
	}
	return filepath.Join(cacheDir, "gh-workbench"), nil
}
