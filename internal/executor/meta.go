package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
	"github.com/banshee-lab/pgkeeper/internal/metadata"
)

var _ domain.MetaExecutor = DockerCLI{}

const snapshotTimeout = 30 * time.Second

// PsqlOutput runs a SQL script and writes the result rows (tuples only,
// unaligned) to out.
func (d DockerCLI) PsqlOutput(ctx context.Context, t domain.SQLTarget, in io.Reader, out, logw io.Writer) error {
	if t.Conn != nil {
		return d.runTool(ctx, t.Image, t.Conn, "psql", append(psqlToolArgs(t.Database), "-t", "-A"), in, out, logw)
	}
	var tail tailBuf
	cmd := exec.CommandContext(ctx, d.bin(), append(psqlArgs(t), "-t", "-A")...)
	cmd.Stdin = in
	cmd.Stdout = out
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

// snapshotWriter picks the exported snapshot id out of psql's stdout.
type snapshotWriter struct {
	buf   []byte
	done  bool
	found chan string // buffered, capacity 1
}

func (w *snapshotWriter) Write(p []byte) (int, error) {
	if w.done {
		return len(p), nil
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		if metadata.ValidSnapshotID(line) {
			w.done, w.buf = true, nil
			w.found <- line
			return len(p), nil
		}
	}
}

type snapSession struct {
	id        string
	stdin     io.WriteCloser
	done      chan error // result of cmd.Wait
	cancel    context.CancelFunc
	once      sync.Once
	err       error
	removeBin string // docker binary, when a tools container must be removed
	removeCtr string
}

func (s *snapSession) ID() string { return s.id }

// Close ends session A: closing stdin makes psql exit, which aborts the
// read-only transaction. A stuck process is killed after 10s. Safe to call
// more than once.
func (s *snapSession) Close() error {
	s.once.Do(func() {
		_ = s.stdin.Close()
		select {
		case s.err = <-s.done:
		case <-time.After(10 * time.Second):
			s.cancel()
			s.err = <-s.done
		}
		s.cancel()
		if s.removeCtr != "" {
			rctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_ = exec.CommandContext(rctx, s.removeBin, "rm", "--force", s.removeCtr).Run()
		}
	})
	return s.err
}

// OpenSnapshot starts session A: a psql process kept alive through its stdin
// pipe, inside a REPEATABLE READ READ ONLY transaction that exported a
// snapshot. It creates no object in the database.
func (d DockerCLI) OpenSnapshot(ctx context.Context, t domain.SQLTarget, logw io.Writer) (domain.SnapshotSession, error) {
	cctx, cancel := context.WithCancel(ctx)
	var cmd *exec.Cmd
	sess := &snapSession{cancel: cancel, done: make(chan error, 1)}
	label := "psql"
	if t.Conn != nil {
		if t.Image == "" {
			cancel()
			return nil, errors.New("tools-image is not configured")
		}
		name := toolsName()
		cmd = exec.CommandContext(cctx, d.bin(), toolsRunArgs(name, t.Image, t.Conn, "psql", append(psqlToolArgs(t.Database), "-t", "-A"))...)
		cmd.Env = toolsEnv(t.Conn)
		sess.removeBin, sess.removeCtr = d.bin(), name
	} else {
		cmd = exec.CommandContext(cctx, d.bin(), append(psqlArgs(t), "-t", "-A")...)
	}
	var tail tailBuf
	sw := &snapshotWriter{found: make(chan string, 1)}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	sess.stdin = stdin
	cmd.Stdout = sw
	cmd.Stderr = io.MultiWriter(logw, &tail)
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	go func() { sess.done <- cmd.Wait() }()

	if _, err := io.WriteString(stdin, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSELECT pg_export_snapshot();\n"); err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("snapshot session: %w", err)
	}
	select {
	case sess.id = <-sw.found:
		return sess, nil
	case err := <-sess.done:
		sess.once.Do(func() {}) // process already gone
		cancel()
		if t.Conn != nil {
			return nil, fmt.Errorf("snapshot session: %w", classifyTools(t.Image, "psql", string(tail.b), err))
		}
		return nil, fmt.Errorf("snapshot session: %w", classify(t.Container, string(tail.b), err))
	case <-time.After(snapshotTimeout):
		_ = sess.Close()
		return nil, errors.New("snapshot session: timed out waiting for pg_export_snapshot()")
	case <-ctx.Done():
		_ = sess.Close()
		return nil, fmt.Errorf("snapshot session interrupted: %w", ctx.Err())
	}
}
