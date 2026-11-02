package restic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

const retryLock = "5m" // needs restic >= 0.16

var versionRe = regexp.MustCompile(`restic (\d+)\.(\d+)\.(\d+)`)

// CLI implements domain.ResticClient by running the restic binary (no shell).
type CLI struct {
	Bin      string          // default "restic"
	TempPath string          // base of <temp>/pgkeeper/.restic-cache; empty = os.TempDir()
	Environ  func() []string // default os.Environ (override in tests)

	mu    sync.Mutex
	locks map[string]chan struct{}
}

var _ domain.ResticClient = (*CLI)(nil)

func New(tempPath string) *CLI { return &CLI{TempPath: tempPath} }

func (c *CLI) bin() string {
	if c.Bin == "" {
		return "restic"
	}
	return c.Bin
}

func (c *CLI) environ() []string {
	if c.Environ != nil {
		return c.Environ()
	}
	return os.Environ()
}

func (c *CLI) cacheDir() string {
	base := c.TempPath
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "pgkeeper", ".restic-cache")
}

// ExitError is returned when restic exits non-zero.
type ExitError struct {
	Code   int
	Output string // last output line, redacted
	tail   string // redacted tail, used to classify failures
	Err    error
}

func (e *ExitError) Error() string {
	msg := fmt.Sprintf("restic exited with status %d", e.Code)
	if e.Output != "" {
		msg += ": " + e.Output
	}
	return msg
}

func (e *ExitError) Unwrap() error { return e.Err }

// ---- per-repository serialisation -------------------------------------------

// lock serialises restic calls per repository; it gives up when ctx is done.
func (c *CLI) lock(ctx context.Context, repo string) (func(), error) {
	c.mu.Lock()
	if c.locks == nil {
		c.locks = map[string]chan struct{}{}
	}
	ch, ok := c.locks[repo]
	if !ok {
		ch = make(chan struct{}, 1)
		c.locks[repo] = ch
	}
	c.mu.Unlock()

	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ---- argv builders (pure, unit-tested) --------------------------------------

func backupArgs(tag, host string, files []string) []string {
	args := []string{"backup", "--tag", tag, "--host", host, "--retry-lock", retryLock, "--"}
	return append(args, files...)
}

// forgetArgs returns nil when no retention value is set.
func forgetArgs(cfg domain.ResticConfig, tag, host string) []string {
	var keep []string
	add := func(flag string, n int) {
		if n > 0 {
			keep = append(keep, flag, strconv.Itoa(n))
		}
	}
	add("--keep-daily", cfg.KeepDaily)
	add("--keep-weekly", cfg.KeepWeekly)
	add("--keep-monthly", cfg.KeepMonthly)
	add("--keep-yearly", cfg.KeepYearly)
	if len(keep) == 0 {
		return nil
	}
	args := []string{"forget", "--tag", tag, "--host", host, "--retry-lock", retryLock}
	args = append(args, keep...)
	return append(args, "--prune")
}

// ---- logging helpers ---------------------------------------------------------

func logLine(w io.Writer, level, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if l, ok := w.(interface{ Logf(level, format string, args ...any) }); ok {
		l.Logf(level, "[restic] %s", text)
		return
	}
	fmt.Fprintf(w, "[restic] %s\n", text)
}

// prefixWriter receives whole redacted lines and emits them as "[restic] ...".
type prefixWriter struct{ w io.Writer }

func (p prefixWriter) Write(b []byte) (int, error) {
	logLine(p.w, "INFO", "%s", strings.TrimSuffix(string(b), "\n"))
	return len(b), nil
}

type tailBuf struct{ b []byte }

func (t *tailBuf) Write(p []byte) (int, error) {
	t.b = append(t.b, p...)
	if len(t.b) > 8192 {
		t.b = t.b[len(t.b)-8192:]
	}
	return len(p), nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 300 {
		l = l[:300] + "…"
	}
	return l
}

// ---- execution ---------------------------------------------------------------

// run executes restic for cfg's repository. logw == nil means the output is
// not streamed (only kept for error reporting).
func (c *CLI) run(ctx context.Context, cfg domain.ResticConfig, dir string, args []string, logw io.Writer) error {
	unlock, err := c.lock(ctx, cfg.Repository)
	if err != nil {
		return fmt.Errorf("waiting for repository lock: %w", err)
	}
	defer unlock()

	cacheDir := c.cacheDir()
	_ = os.MkdirAll(cacheDir, 0o700)
	env := buildEnv(cfg, cacheDir, c.environ())
	red := NewRedactor(secretsFor(cfg, env)...)

	tail := &tailBuf{}
	var out io.Writer = tail
	var rw io.WriteCloser
	if logw != nil {
		rw = red.Writer(prefixWriter{logw})
		out = io.MultiWriter(rw, tail)
	}

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = out // same writer for both: os/exec uses one goroutine
	cmd.Stderr = out
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) } // let restic drop its lock
	cmd.WaitDelay = 15 * time.Second

	runErr := cmd.Run()
	if rw != nil {
		_ = rw.Close()
	}
	if runErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("restic interrupted: %w", ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		t := red.Redact(string(tail.b))
		return &ExitError{Code: ee.ExitCode(), Output: lastLine(t), tail: t, Err: runErr}
	}
	return fmt.Errorf("run restic: %w", runErr)
}

// repoMissing tells "repository does not exist" apart from wrong password,
// lock, permission or network errors (those must never trigger init).
func repoMissing(e *ExitError) bool {
	switch e.Code {
	case 10: // restic >= 0.17: repository does not exist
		return true
	case 11, 12: // failed to lock, wrong password
		return false
	}
	t := strings.ToLower(e.tail)
	if strings.Contains(t, "repository does not exist") {
		return true
	}
	if strings.Contains(t, "unable to open config file") {
		return strings.Contains(t, "no such file or directory") ||
			strings.Contains(t, "does not exist") ||
			strings.Contains(t, "not found") ||
			strings.Contains(t, "nosuchkey")
	}
	return false
}

// Check runs `restic cat config` quietly. exists=false with nil error means
// the repository is missing; any other failure is returned.
func (c *CLI) Check(ctx context.Context, cfg domain.ResticConfig) (bool, error) {
	err := c.run(ctx, cfg, "", []string{"cat", "config"}, nil)
	if err == nil {
		return true, nil
	}
	var ee *ExitError
	if errors.As(err, &ee) && repoMissing(ee) {
		return false, nil
	}
	return false, err
}

func (c *CLI) EnsureRepo(ctx context.Context, cfg domain.ResticConfig, logw io.Writer) error {
	if logw == nil {
		logw = io.Discard
	}
	exists, err := c.Check(ctx, cfg)
	if err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if exists {
		logLine(logw, "INFO", "repository %s is ready", RedactRepo(cfg.Repository))
		return nil
	}
	logLine(logw, "INFO", "repository %s does not exist, running init", RedactRepo(cfg.Repository))
	if err := c.run(ctx, cfg, "", []string{"init"}, logw); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	return nil
}

func (c *CLI) Backup(ctx context.Context, cfg domain.ResticConfig, tag, host, dir string, files []string, logw io.Writer) error {
	if logw == nil {
		logw = io.Discard
	}
	if len(files) == 0 {
		return errors.New("backup: no files to store")
	}
	logLine(logw, "INFO", "backup: tag=%s host=%s files=%s", tag, host, strings.Join(files, ","))
	if err := c.run(ctx, cfg, dir, backupArgs(tag, host, files), logw); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

func (c *CLI) Forget(ctx context.Context, cfg domain.ResticConfig, tag, host string, logw io.Writer) error {
	if logw == nil {
		logw = io.Discard
	}
	args := forgetArgs(cfg, tag, host)
	if args == nil {
		logLine(logw, "INFO", "retention not configured, skipping forget")
		return nil
	}
	if err := c.run(ctx, cfg, "", args, logw); err != nil {
		return fmt.Errorf("forget: %w", err)
	}
	return nil
}

// CheckVersion runs `restic version`; it errors if restic is missing or older than 0.16.
func (c *CLI) CheckVersion(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, c.bin(), "version")
	cmd.Env = inheritEnv(c.environ())
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("restic not available: %w", err)
	}
	m := versionRe.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("cannot parse restic version from %q", strings.TrimSpace(string(out)))
	}
	major, _ := strconv.Atoi(string(m[1]))
	minor, _ := strconv.Atoi(string(m[2]))
	v := fmt.Sprintf("%s.%s.%s", m[1], m[2], m[3])
	if major == 0 && minor < 16 {
		return v, fmt.Errorf("restic %s is older than the minimum supported 0.16 (--retry-lock)", v)
	}
	return v, nil
}