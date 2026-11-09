package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// toolsRunArgs builds the `docker run` of a one-shot tools container that
// runs one PostgreSQL client program against a remote server. The container
// is labelled like the test-restore ones, so leftovers of a crashed process
// are removed at startup. The connection settings are passed as `-e NAME`
// without a value: docker copies them from its own environment (see
// toolsEnv), so the password never shows up in a process listing.
func toolsRunArgs(name, image string, conn *domain.PGConn, tool string, toolArgs []string) []string {
	args := []string{"run", "--rm", "-i", "--name", name, "--label", restoreLabel, "--entrypoint", tool}
	for _, kv := range conn.Env() {
		key, _, _ := strings.Cut(kv, "=")
		args = append(args, "--env", key)
	}
	args = append(args, image)
	return append(args, toolArgs...)
}

// toolsEnv is the environment of the docker CLI: the caller's, with the
// connection settings added.
func toolsEnv(conn *domain.PGConn) []string {
	return append(os.Environ(), conn.Env()...)
}

func pgDumpToolArgs(s domain.DumpSpec) (tool string, args []string, err error) {
	switch s.Kind {
	case domain.DumpDatabase:
		return "pg_dump", []string{"--no-owner", "--disable-triggers", "--format=plain", "--no-password"}, nil
	case domain.DumpRoles:
		args = []string{"--roles-only", "--no-password"}
		if s.Conn != nil && s.Conn.Database != "" {
			args = append(args, "--database="+s.Conn.Database)
		}
		return "pg_dumpall", args, nil
	}
	return "", nil, fmt.Errorf("unknown dump kind %q", s.Kind)
}

func psqlToolArgs(database string) []string {
	return []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-password", "-d", database}
}

func toolsName() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "pgkeeper-tools-" + hex.EncodeToString(b[:])
}

// classifyTools turns the stderr tail of a tools container into a short error.
func classifyTools(image, tool, tail string, err error) error {
	switch {
	case strings.Contains(tail, "pull access denied"), strings.Contains(tail, "manifest unknown"):
		return fmt.Errorf("cannot pull tools image %q", image)
	case strings.Contains(tail, "executable file not found"):
		return fmt.Errorf("tools image %q does not contain %s", image, tool)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if line := lastNonEmptyLine(tail); line != "" {
			return fmt.Errorf("%s exited with status %d: %s", tool, ee.ExitCode(), line)
		}
		return fmt.Errorf("%s exited with status %d", tool, ee.ExitCode())
	}
	return err
}

// runTool runs one client program in a tools container. stdin and stdout may
// be nil; stderr goes to logw.
func (d DockerCLI) runTool(ctx context.Context, image string, conn *domain.PGConn, tool string, toolArgs []string,
	stdin io.Reader, stdout, logw io.Writer) error {

	if image == "" {
		return errors.New("tools-image is not configured")
	}
	name := toolsName()
	var tail tailBuf
	cmd := exec.CommandContext(ctx, d.bin(), toolsRunArgs(name, image, conn, tool, toolArgs)...)
	cmd.Env = toolsEnv(conn)
	cmd.Stdin = stdin
	if stdout != nil {
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.MultiWriter(logw, &tail)
	cmd.WaitDelay = 10 * time.Second
	err := cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		// Killing the docker CLI does not stop the container: remove it.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = exec.CommandContext(rctx, d.bin(), "rm", "--force", name).Run()
		return fmt.Errorf("%s interrupted: %w", tool, ctx.Err())
	}
	return classifyTools(image, tool, string(tail.b), err)
}
