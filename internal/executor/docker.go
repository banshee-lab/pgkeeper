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

type DockerCLI struct{ Bin string } // Bin defaults to "docker"

var _ domain.Executor = DockerCLI{}

func (d DockerCLI) bin() string {
	if d.Bin == "" {
		return "docker"
	}
	return d.Bin
}

func buildArgs(s domain.DumpSpec) ([]string, error) {
	port := strconv.Itoa(s.Port)
	switch s.Kind {
	case domain.DumpDatabase:
		return []string{"exec", s.Container, "pg_dump", "--no-owner", "--disable-triggers", "--format=plain",
			"-d", s.Database, "--host=" + s.Host, "-p", port, "--username=" + s.Username, "--no-password"}, nil
	case domain.DumpRoles:
		return []string{"exec", s.Container, "pg_dumpall",
			"--host=" + s.Host, "-p", port, "--username=" + s.Username, "--no-password", "--roles-only"}, nil
	}
	return nil, fmt.Errorf("unknown dump kind %q", s.Kind)
}

type tailBuf struct{ b []byte }

func (t *tailBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 4096 {
		t.b = t.b[len(t.b)-4096:]
	}
	return len(p), nil
}

func (d DockerCLI) Dump(ctx context.Context, s domain.DumpSpec, out io.Writer, logw io.Writer) error {
	args, err := buildArgs(s)
	if err != nil {
		return err
	}
	var tail tailBuf
	cmd := exec.CommandContext(ctx, d.bin(), args...)
	cmd.Stdout = out // *os.File: no TTY, no shell
	cmd.Stderr = io.MultiWriter(logw, &tail)
	cmd.WaitDelay = 10 * time.Second

	err = cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("docker exec %s interrupted: %w", s.Kind, ctx.Err())
	}
	msg := string(tail.b)
	switch {
	case strings.Contains(msg, "No such container"):
		return fmt.Errorf("container %q not found", s.Container)
	case strings.Contains(msg, "is not running"):
		return fmt.Errorf("container %q is not running", s.Container)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Errorf("docker exec %s exited with status %d", s.Kind, ee.ExitCode())
	}
	return fmt.Errorf("docker exec %s: %w", s.Kind, err)
}

// Check verifies the Docker daemon is reachable (startup warning only).
func (d DockerCLI) Check(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, d.bin(), "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker not reachable: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}