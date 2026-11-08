package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// restoreLabel marks the containers pgkeeper starts for test restores, so
// leftovers of a crashed run can be found and removed.
const restoreLabel = "pgkeeper.restore=true"

var _ domain.RestoreExecutor = DockerCLI{}

// startEphemeralArgs builds the `docker run` for a throw-away PostgreSQL. The
// container has no network (restores go through `docker exec`), trusts local
// connections, and is removed by Docker as soon as it stops.
func startEphemeralArgs(name, image string) []string {
	return []string{"run", "--detach", "--rm", "--name", name, "--network", "none",
		"--label", restoreLabel, "--env", "POSTGRES_HOST_AUTH_METHOD=trust", image}
}

// psqlArgs builds the `docker exec` that runs a SQL script read from stdin.
func psqlArgs(t domain.SQLTarget) []string {
	return []string{"exec", "-i", t.Container, "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-password",
		"--host=" + t.Host, "-p", strconv.Itoa(t.Port), "--username=" + t.Username, "-d", t.Database}
}

func pgReadyArgs(t domain.SQLTarget) []string {
	return []string{"exec", t.Container, "pg_isready", "--host=" + t.Host, "-p", strconv.Itoa(t.Port),
		"--username=" + t.Username}
}

// classify turns docker's stderr tail into a short error for common problems.
func classify(container, tail string, err error) error {
	switch {
	case strings.Contains(tail, "No such container"):
		return fmt.Errorf("container %q not found", container)
	case strings.Contains(tail, "is not running"):
		return fmt.Errorf("container %q is not running", container)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if line := lastNonEmptyLine(tail); line != "" {
			return fmt.Errorf("exited with status %d: %s", ee.ExitCode(), line)
		}
		return fmt.Errorf("exited with status %d", ee.ExitCode())
	}
	return err
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 300 {
		l = l[:300] + "…"
	}
	return l
}

func (d DockerCLI) StartEphemeral(ctx context.Context, name, image string, logw io.Writer) error {
	var tail tailBuf
	cmd := exec.CommandContext(ctx, d.bin(), startEphemeralArgs(name, image)...)
	cmd.Stdout = io.MultiWriter(logw, &tail) // the new container id
	cmd.Stderr = io.MultiWriter(logw, &tail) // image pull progress
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("docker run interrupted: %w", ctx.Err())
		}
		return fmt.Errorf("docker run %s: %w", image, classify(name, string(tail.b), err))
	}
	return nil
}

func (d DockerCLI) RemoveContainer(ctx context.Context, name string, logw io.Writer) error {
	var tail tailBuf
	cmd := exec.CommandContext(ctx, d.bin(), "rm", "--force", "--volumes", name)
	cmd.Stdout = io.MultiWriter(logw, &tail)
	cmd.Stderr = io.MultiWriter(logw, &tail)
	if err := cmd.Run(); err != nil {
		// --rm may already have removed it.
		if strings.Contains(string(tail.b), "No such container") {
			return nil
		}
		return fmt.Errorf("docker rm %s: %w", name, classify(name, string(tail.b), err))
	}
	return nil
}

func (d DockerCLI) WaitReady(ctx context.Context, t domain.SQLTarget, timeout time.Duration, logw io.Writer) error {
	deadline := time.Now().Add(timeout)
	for {
		var tail tailBuf
		cmd := exec.CommandContext(ctx, d.bin(), pgReadyArgs(t)...)
		cmd.Stdout = &tail
		cmd.Stderr = &tail
		err := cmd.Run()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("waiting for PostgreSQL interrupted: %w", ctx.Err())
		}
		msg := string(tail.b)
		if strings.Contains(msg, "No such container") || strings.Contains(msg, "is not running") {
			return classify(t.Container, msg, err) // the container died: no point in waiting
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("PostgreSQL in container %q was not ready after %s", t.Container, timeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for PostgreSQL interrupted: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

func (d DockerCLI) Psql(ctx context.Context, t domain.SQLTarget, in io.Reader, logw io.Writer) error {
	var tail tailBuf
	cmd := exec.CommandContext(ctx, d.bin(), psqlArgs(t)...)
	cmd.Stdin = in
	cmd.Stdout = io.Discard // result rows (e.g. setval) are noise
	cmd.Stderr = io.MultiWriter(logw, &tail)
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("psql interrupted: %w", ctx.Err())
		}
		return fmt.Errorf("psql: %w", classify(t.Container, string(tail.b), err))
	}
	return nil
}

// CleanupOrphans removes test-restore containers left behind by a previous
// process (for example after a crash). It returns how many were removed.
func (d DockerCLI) CleanupOrphans(ctx context.Context) (int, error) {
	out, err := exec.CommandContext(ctx, d.bin(), "ps", "--all", "--quiet", "--filter", "label="+restoreLabel).Output()
	if err != nil {
		return 0, fmt.Errorf("list restore containers: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return 0, nil
	}
	args := append([]string{"rm", "--force", "--volumes"}, ids...)
	if err := exec.CommandContext(ctx, d.bin(), args...).Run(); err != nil {
		return 0, fmt.Errorf("remove restore containers: %w", err)
	}
	return len(ids), nil
}
