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
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/zoubingwu/gh-workbench/internal/github"
	"github.com/zoubingwu/gh-workbench/internal/server"
	"github.com/zoubingwu/gh-workbench/internal/store"
	"github.com/zoubingwu/gh-workbench/internal/syncer"
)

const (
	githubTimeout  = 30 * time.Second
	publishDelay   = 200 * time.Millisecond
	shutdownPeriod = 5 * time.Second
	workerCount    = 4
)

type Options struct {
	DataDir   string
	NoBrowser bool
	Stdout    io.Writer
	Stderr    io.Writer
}

func Run(ctx context.Context, options Options) error {
	if options.Stdout == nil {
		options.Stdout = os.Stdout
	}
	if options.Stderr == nil {
		options.Stderr = os.Stderr
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

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on loopback: %w", err)
	}
	defer listener.Close()

	var localServer *server.Server
	snapshotUpdates := make(chan struct{}, 1)
	runner := syncer.New(database, githubClient, host, viewer, workerCount, func() {
		select {
		case snapshotUpdates <- struct{}{}:
		default:
		}
	})
	localServer, err = server.New(database, runner, host, viewer)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Handler:           localServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan error, 3)
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
		results <- publishSnapshots(
			runContext,
			snapshotUpdates,
			localServer.PublishSnapshot,
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
	if options.NoBrowser {
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

	for received < 3 {
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
