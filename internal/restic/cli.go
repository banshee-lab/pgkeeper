package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// retryLock is how long restic keeps retrying to lock a repository held by
// another process (e.g. a prune) before failing. Needs restic >= 0.16.
const retryLock = "5m"

var versionRe = regexp.MustCompile(`restic (\d+)\.(\d+)\.(\d+)`)
var ErrInvalidSnapshotRequest = errors.New("invalid snapshot request")
var ErrSnapshotNotFound = errors.New("snapshot not found")

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

// Locked reports whether restic could not lock the repository (exit status 11).
func (e *ExitError) Locked() bool {
	if e.Code == 11 {
		return true
	}
	t := strings.ToLower(e.tail)
	return strings.Contains(t, "repository is already locked") || strings.Contains(t, "unable to create lock")
}

func (e *ExitError) SnapshotMissing() bool {
	t := strings.ToLower(e.tail)
	return strings.Contains(t, "failed to load snapshot") &&
		(strings.Contains(t, "no such file or directory") || strings.Contains(t, "does not exist"))
}

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
	args := []string{"backup", "--json", "--tag", tag, "--host", host, "--retry-lock", retryLock, "--"}
	return append(args, files...)
}

// forgetArgs returns nil when no retention value is set. It prunes the
// forgotten data too unless cfg.SkipPrune is set, in which case only the
// scheduled `restic prune` ([restic].prune-cron) reclaims the space.
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
	if !cfg.SkipPrune {
		args = append(args, "--prune")
	}
	return args
}

// pruneArgs builds `restic prune`. It takes an exclusive repository lock, so
// it waits (--retry-lock) for other processes instead of failing at once.
func pruneArgs() []string {
	return []string{"prune", "--retry-lock", retryLock}
}

// checkDataArgs builds `restic check` reading readPercent% of the pack data.
// check only needs a shared lock, but --retry-lock lets it wait for a running
// prune.
func checkDataArgs(readPercent int) []string {
	args := []string{"check", "--retry-lock", retryLock}
	if readPercent >= 100 {
		return append(args, "--read-data")
	}
	return append(args, "--read-data-subset="+strconv.Itoa(readPercent)+"%")
}

// ---- logging helpers ---------------------------------------------------------

func logLine(w io.Writer, level, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if l, ok := w.(interface {
		Logf(level, format string, args ...any)
	}); ok {
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
// tap, when not nil, filters the combined output before it is logged.
func (c *CLI) run(ctx context.Context, cfg domain.ResticConfig, dir string, args []string, logw io.Writer, tap *backupOutput) error {
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

	if tap != nil {
		tap.dst = out
		out = tap
	}

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = out // same writer for both: os/exec uses one goroutine
	cmd.Stderr = out
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) } // let restic drop its lock
	cmd.WaitDelay = 15 * time.Second

	runErr := cmd.Run()
	if tap != nil {
		_ = tap.Close()
	}
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
		xe := &ExitError{Code: ee.ExitCode(), Output: lastLine(t), tail: t, Err: runErr}
		if xe.Locked() {
			xe.Output = "repository is locked by another process, gave up after waiting " + retryLock + ": " + xe.Output
		}
		return xe
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
	err := c.run(ctx, cfg, "", []string{"cat", "config", "--retry-lock", retryLock}, nil, nil)
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
	if err := c.run(ctx, cfg, "", []string{"init"}, logw, nil); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	return nil
}

// Backup creates one snapshot and returns its summary, or nil when restic did
// not report one (the backup itself succeeded). Timestamp is left for the caller.
func (c *CLI) Backup(ctx context.Context, cfg domain.ResticConfig, tag, host, dir string, files []string, logw io.Writer) (*domain.SnapshotInfo, error) {
	if logw == nil {
		logw = io.Discard
	}
	if len(files) == 0 {
		return nil, errors.New("backup: no files to store")
	}
	logLine(logw, "INFO", "backup: tag=%s host=%s files=%s", tag, host, strings.Join(files, ","))
	tap := &backupOutput{}
	if err := c.run(ctx, cfg, dir, backupArgs(tag, host, files), logw, tap); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	if tap.summary == nil {
		return nil, nil
	}
	return &domain.SnapshotInfo{
		ID:           tap.summary.SnapshotID,
		FilesAdded:   tap.summary.FilesNew,
		FilesChanged: tap.summary.FilesChanged,
		BytesAdded:   tap.summary.DataAdded,
	}, nil
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
	if err := c.run(ctx, cfg, "", args, logw, nil); err != nil {
		return fmt.Errorf("forget: %w", err)
	}
	return nil
}

var _ domain.ResticMaintainer = (*CLI)(nil)
var _ domain.RepositoryInspector = (*CLI)(nil)

// Prune runs `restic prune`. A missing repository yields domain.ErrRepoMissing
// (nothing is initialised); a lock held by another process is retried for 5
// minutes and then reported as an ExitError with Locked() == true.
func (c *CLI) Prune(ctx context.Context, cfg domain.ResticConfig, logw io.Writer) error {
	if logw == nil {
		logw = io.Discard
	}
	if err := c.run(ctx, cfg, "", pruneArgs(), logw, nil); err != nil {
		return maintenanceErr("prune", err)
	}
	return nil
}

// CheckData runs `restic check` reading readPercent% (1-100) of the data.
func (c *CLI) CheckData(ctx context.Context, cfg domain.ResticConfig, readPercent int, logw io.Writer) error {
	if logw == nil {
		logw = io.Discard
	}
	if readPercent < 1 || readPercent > 100 {
		return fmt.Errorf("check: read percent must be between 1 and 100 (got %d)", readPercent)
	}
	if err := c.run(ctx, cfg, "", checkDataArgs(readPercent), logw, nil); err != nil {
		return maintenanceErr("check", err)
	}
	return nil
}

func (c *CLI) inspect(ctx context.Context, cfg domain.ResticConfig, args ...string) ([]byte, error) {
	var out bytes.Buffer
	if err := c.inspectTo(ctx, cfg, &out, args...); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (c *CLI) inspectTo(ctx context.Context, cfg domain.ResticConfig, stdout io.Writer, args ...string) error {
	unlock, err := c.lock(ctx, cfg.Repository)
	if err != nil {
		return fmt.Errorf("waiting for repository lock: %w", err)
	}
	defer unlock()
	cacheDir := c.cacheDir()
	_ = os.MkdirAll(cacheDir, 0o700)
	env := buildEnv(cfg, cacheDir, c.environ())
	red := NewRedactor(secretsFor(cfg, env)...)
	cmd := exec.CommandContext(ctx, c.bin(), args...)
	cmd.Env = env
	cmd.Stdout = stdout
	var stderr tailBuf
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("restic inspection interrupted: %w", ctx.Err())
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		tail := red.Redact(string(stderr.b))
		exitErr := &ExitError{Code: ee.ExitCode(), Output: lastLine(tail), tail: tail, Err: ee}
		if repoMissing(exitErr) {
			return domain.ErrRepoMissing
		}
		return exitErr
	}
	return fmt.Errorf("run restic: %w", err)
}

func (c *CLI) RepositoryStats(ctx context.Context, cfg domain.ResticConfig) (int64, int, error) {
	out, err := c.inspect(ctx, cfg, "stats", "--json", "--mode", "raw-data")
	if errors.Is(err, domain.ErrRepoMissing) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var stats struct {
		TotalSize      int64 `json:"total_size"`
		SnapshotsCount int   `json:"snapshots_count"`
	}
	if err := json.Unmarshal(out, &stats); err != nil {
		return 0, 0, fmt.Errorf("decode restic stats: %w", err)
	}
	return stats.TotalSize, stats.SnapshotsCount, nil
}

func (c *CLI) RepositorySnapshots(ctx context.Context, cfg domain.ResticConfig) ([]domain.RepositorySnapshot, error) {
	out, err := c.inspect(ctx, cfg, "snapshots", "--json")
	if errors.Is(err, domain.ErrRepoMissing) {
		return []domain.RepositorySnapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	var snapshots []domain.RepositorySnapshot
	if err := json.Unmarshal(out, &snapshots); err != nil {
		return nil, fmt.Errorf("decode restic snapshots: %w", err)
	}
	if snapshots == nil {
		snapshots = []domain.RepositorySnapshot{}
	}
	for i := range snapshots {
		if snapshots[i].Paths == nil {
			snapshots[i].Paths = []string{}
		}
		if snapshots[i].Tags == nil {
			snapshots[i].Tags = []string{}
		}
	}
	return snapshots, nil
}

func (c *CLI) RepositorySnapshotStats(ctx context.Context, cfg domain.ResticConfig, snapshotID string) (domain.RepositorySnapshotStats, error) {
	if !validSnapshotID(snapshotID) {
		return domain.RepositorySnapshotStats{}, ErrInvalidSnapshotRequest
	}
	snapshots, err := c.RepositorySnapshots(ctx, cfg)
	if err != nil {
		return domain.RepositorySnapshotStats{}, err
	}
	var selected *domain.RepositorySnapshot
	for i := range snapshots {
		if snapshots[i].ID == snapshotID {
			selected = &snapshots[i]
			break
		}
	}
	if selected == nil {
		return domain.RepositorySnapshotStats{}, domain.ErrNotFound
	}
	if selected.Parent == "" {
		return domain.RepositorySnapshotStats{}, nil
	}
	if !validSnapshotID(selected.Parent) {
		return domain.RepositorySnapshotStats{}, fmt.Errorf("snapshot %s has an invalid parent ID", snapshotID)
	}

	output := &diffStatsWriter{source: selected.Parent, target: snapshotID}
	if err := c.inspectTo(ctx, cfg, output, "diff", "--json", selected.Parent, snapshotID); err != nil {
		return domain.RepositorySnapshotStats{}, err
	}
	if err := output.finish(); err != nil {
		return domain.RepositorySnapshotStats{}, err
	}
	return output.stats, nil
}

func (c *CLI) RepositoryTree(ctx context.Context, cfg domain.ResticConfig, snapshotID, snapshotPath string) ([]domain.RepositoryNode, error) {
	if !validSnapshotID(snapshotID) || !validSnapshotPath(snapshotPath) {
		return nil, ErrInvalidSnapshotRequest
	}
	args := []string{"ls", "--json", snapshotID}
	if snapshotPath != "" && snapshotPath != "." {
		args = append(args, snapshotPath)
	}
	out, err := c.inspect(ctx, cfg, args...)
	if err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) && exitErr.SnapshotMissing() {
			return nil, ErrSnapshotNotFound
		}
		return nil, err
	}
	nodes := make([]domain.RepositoryNode, 0)
	for _, line := range bytes.Split(out, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var row struct {
			MessageType string    `json:"message_type"`
			Path        string    `json:"path"`
			Name        string    `json:"name"`
			Type        string    `json:"type"`
			Size        int64     `json:"size"`
			ModTime     time.Time `json:"mtime"`
			Node        *struct {
				Path    string    `json:"path"`
				Name    string    `json:"name"`
				Type    string    `json:"type"`
				Size    int64     `json:"size"`
				ModTime time.Time `json:"mtime"`
			} `json:"node"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("decode restic tree: %w", err)
		}
		if row.Node != nil {
			nodes = append(nodes, domain.RepositoryNode{
				Path: row.Node.Path, Name: row.Node.Name, Type: row.Node.Type,
				Size: row.Node.Size, ModTime: row.Node.ModTime,
			})
		} else if row.MessageType == "node" {
			nodes = append(nodes, domain.RepositoryNode{
				Path: row.Path, Name: row.Name, Type: row.Type,
				Size: row.Size, ModTime: row.ModTime,
			})
		}
	}
	currentPath := pathpkg.Clean(snapshotPath)
	if currentPath == "." || currentPath == "" {
		currentPath = "/"
	}
	children := nodes[:0]
	for _, node := range nodes {
		parent := pathpkg.Dir(pathpkg.Clean(node.Path))
		if parent == "." || parent == "" {
			parent = "/"
		}
		if parent == currentPath {
			children = append(children, node)
		}
	}
	return children, nil
}

func (c *CLI) DownloadSnapshotFile(ctx context.Context, cfg domain.ResticConfig, snapshotID, path string, dst io.Writer) error {
	if err := ValidateSnapshotFile(snapshotID, path); err != nil {
		return err
	}
	unlock, err := c.lock(ctx, cfg.Repository)
	if err != nil {
		return fmt.Errorf("waiting for repository lock: %w", err)
	}
	defer unlock()
	cacheDir := c.cacheDir()
	_ = os.MkdirAll(cacheDir, 0o700)
	env := buildEnv(cfg, cacheDir, c.environ())
	cmd := exec.CommandContext(ctx, c.bin(), "dump", snapshotID, path)
	cmd.Env = env
	cmd.Stdout = dst
	var stderr tailBuf
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("restic dump interrupted: %w", ctx.Err())
		}
		red := NewRedactor(secretsFor(cfg, env)...)
		message := red.Redact(lastLine(string(stderr.b)))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("restic dump failed: %s", message)
	}
	return nil
}

func (c *CLI) Unlock(ctx context.Context, cfg domain.ResticConfig, logw io.Writer) error {
	if err := c.run(ctx, cfg, "", []string{"unlock", "--remove-all"}, logw, nil); err != nil {
		return fmt.Errorf("unlock: %w", err)
	}
	return nil
}

func validSnapshotID(id string) bool {
	if len(id) < 8 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func ValidateSnapshotFile(snapshotID, path string) error {
	if !validSnapshotID(snapshotID) || !validSnapshotPath(path) || path == "" || path == "." {
		return ErrInvalidSnapshotRequest
	}
	return nil
}

func validSnapshotPath(path string) bool {
	if !utf8.ValidString(path) || strings.ContainsRune(path, 0) || strings.HasPrefix(path, "-") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func maintenanceErr(op string, err error) error {
	var ee *ExitError
	if errors.As(err, &ee) && repoMissing(ee) {
		return fmt.Errorf("%s: %w", op, domain.ErrRepoMissing)
	}
	return fmt.Errorf("%s: %w", op, err)
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
