package agentstatus

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

func resolveGitIdentity(ctx context.Context, directory string) (gitIdentity, error) {
	sha, err := gitOutput(ctx, directory, "rev-parse", "HEAD")
	if err != nil {
		return gitIdentity{}, fmt.Errorf("read git HEAD: %w", err)
	}

	branch, _ := gitOutput(ctx, directory, "symbolic-ref", "--quiet", "--short", "HEAD")
	remoteName := ""
	if branch != "" {
		remoteName = gitConfigValue(
			ctx,
			directory,
			"branch."+branch+".pushRemote",
		)
	}
	if remoteName == "" {
		remoteName = gitConfigValue(ctx, directory, "remote.pushDefault")
	}
	if remoteName == "" && branch != "" {
		remoteName = gitConfigValue(
			ctx,
			directory,
			"branch."+branch+".remote",
		)
	}
	if remoteName == "" || remoteName == "." {
		remoteName = "origin"
	}

	identity := gitIdentity{branch: branch, sha: sha}
	remoteURL, err := gitOutput(
		ctx,
		directory,
		"remote",
		"get-url",
		"--push",
		remoteName,
	)
	if err != nil {
		return identity, nil
	}
	identity.repositoryKey, err = repositoryKeyFromRemoteURL(remoteURL)
	if err != nil {
		return gitIdentity{}, fmt.Errorf("parse git remote: %w", err)
	}
	return identity, nil
}

func gitConfigValue(
	ctx context.Context,
	directory string,
	key string,
) string {
	value, _ := gitOutput(ctx, directory, "config", "--get", key)
	return value
}

func gitOutput(
	ctx context.Context,
	directory string,
	arguments ...string,
) (string, error) {
	command := exec.CommandContext(
		ctx,
		"git",
		append([]string{"-C", directory}, arguments...)...,
	)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func repositoryKeyFromRemoteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("remote URL is empty")
	}

	var host, path string
	if !strings.Contains(raw, "://") {
		var authority string
		var ok bool
		authority, path, ok = strings.Cut(raw, ":")
		if !ok || path == "" {
			return "", fmt.Errorf("unsupported remote URL %q", raw)
		}
		if _, value, found := strings.Cut(authority, "@"); found {
			authority = value
		}
		host = authority
	} else {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "", fmt.Errorf("parse remote URL: %w", err)
		}
		host = parsed.Hostname()
		path = parsed.Path
	}

	path = strings.Trim(strings.TrimSuffix(path, ".git"), "/")
	parts := strings.Split(path, "/")
	if host == "" || len(parts) < 2 {
		return "", fmt.Errorf("remote URL %q has no repository identity", raw)
	}
	owner := parts[len(parts)-2]
	repository := parts[len(parts)-1]
	if owner == "" || repository == "" {
		return "", fmt.Errorf("remote URL %q has no repository identity", raw)
	}
	return strings.ToLower(host) + "/" + owner + "/" + repository, nil
}
