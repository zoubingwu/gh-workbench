package agentstatus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryKeyFromRemoteURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "SSH shorthand",
			url:  "git@github.com:octocat/rocket.git",
			want: "github.com/octocat/rocket",
		},
		{
			name: "HTTPS",
			url:  "https://github.com/octocat/rocket.git",
			want: "github.com/octocat/rocket",
		},
		{
			name: "SSH URL",
			url:  "ssh://git@git.example.com/octocat/rocket.git",
			want: "git.example.com/octocat/rocket",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := repositoryKeyFromRemoteURL(test.url)
			if err != nil {
				t.Fatalf("repositoryKeyFromRemoteURL() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("repositoryKeyFromRemoteURL() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveGitIdentity(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "ship-rocket")
	runGit(t, repository, "remote", "add", "origin", "git@github.com:octocat/rocket.git")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("rocket\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGit(t, repository, "add", "README.md")
	runGit(
		t,
		repository,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.com",
		"commit",
		"-m",
		"fixture",
	)
	wantSHA := runGit(t, repository, "rev-parse", "HEAD")

	identity, err := resolveGitIdentity(context.Background(), repository)
	if err != nil {
		t.Fatalf("resolveGitIdentity() error = %v", err)
	}
	if identity.repositoryKey != "github.com/octocat/rocket" ||
		identity.branch != "ship-rocket" ||
		identity.sha != wantSHA {
		t.Fatalf("resolveGitIdentity() = %#v", identity)
	}
}

func TestResolveGitIdentityPrefersPushRemote(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	runGit(t, repository, "init", "-b", "ship-rocket")
	runGit(t, repository, "remote", "add", "origin", "git@github.com:upstream/rocket.git")
	runGit(t, repository, "remote", "add", "fork", "git@github.com:octocat/rocket.git")
	runGit(t, repository, "config", "branch.ship-rocket.remote", "origin")
	runGit(t, repository, "config", "branch.ship-rocket.pushRemote", "fork")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("rocket\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runGit(t, repository, "add", "README.md")
	runGit(
		t,
		repository,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.com",
		"commit",
		"-m",
		"fixture",
	)

	identity, err := resolveGitIdentity(context.Background(), repository)
	if err != nil {
		t.Fatalf("resolveGitIdentity() error = %v", err)
	}
	if identity.repositoryKey != "github.com/octocat/rocket" {
		t.Fatalf("repository key = %q", identity.repositoryKey)
	}
}

func TestResolveGitIdentityUsesUpstreamBranchName(t *testing.T) {
	t.Parallel()

	fixture := t.TempDir()
	remote := filepath.Join(fixture, "remote.git")
	repository := filepath.Join(fixture, "work")
	runGit(t, fixture, "init", "--bare", remote)
	runGit(t, fixture, "init", "-b", "local-work", repository)
	runGit(t, repository, "remote", "add", "origin", remote)
	runGit(
		t,
		repository,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.com",
		"commit",
		"--allow-empty",
		"-m",
		"fixture",
	)
	runGit(t, repository, "push", "--set-upstream", "origin", "HEAD:pr-head")
	runGit(
		t,
		repository,
		"-c",
		"user.name=Test",
		"-c",
		"user.email=test@example.com",
		"commit",
		"--allow-empty",
		"-m",
		"local only",
	)
	runGit(
		t,
		repository,
		"remote",
		"set-url",
		"origin",
		"git@github.com:octocat/rocket.git",
	)

	identity, err := resolveGitIdentity(context.Background(), repository)
	if err != nil {
		t.Fatalf("resolveGitIdentity() error = %v", err)
	}
	if identity.repositoryKey != "github.com/octocat/rocket" ||
		identity.branch != "pr-head" ||
		identity.sha != runGit(t, repository, "rev-parse", "HEAD") {
		t.Fatalf("resolveGitIdentity() = %#v", identity)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()

	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}
