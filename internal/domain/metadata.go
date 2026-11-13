package domain

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Mismatch is one difference between the metadata captured at backup time
// (expected) and the one captured from the restored database (actual).
type Mismatch struct {
	Section  string // "objects", "table_rows" or "sequences"
	Object   string // object type, or schema-qualified object name
	Expected string
	Actual   string
}

func (m Mismatch) String() string {
	return fmt.Sprintf("section=%s object=%s expected=%s actual=%s", m.Section, m.Object, m.Expected, m.Actual)
}

// ValidationWarning is returned by a restore that completed but whose result
// does not match the backup metadata. The runner records it as StatusWarning.
type ValidationWarning struct{ Mismatches []Mismatch }

func (w *ValidationWarning) Error() string {
	n := len(w.Mismatches)
	if n == 1 {
		return "restore ok, metadata validation found 1 mismatch: " + w.Mismatches[0].String()
	}
	first := make([]string, 0, 3)
	for i := 0; i < n && i < 3; i++ {
		first = append(first, w.Mismatches[i].String())
	}
	more := ""
	if n > 3 {
		more = fmt.Sprintf("; … and %d more (see log)", n-3)
	}
	return fmt.Sprintf("restore ok, metadata validation found %d mismatches: %s%s", n, strings.Join(first, "; "), more)
}

// SnapshotSession is an open, exported PostgreSQL snapshot. It must be closed
// once the dump and the metadata capture that use it are finished.
type SnapshotSession interface {
	ID() string
	Close() error
}

// MetaExecutor runs the SQL side of metadata capture.
type MetaExecutor interface {
	// OpenSnapshot starts a REPEATABLE READ READ ONLY transaction (session A)
	// and exports its snapshot; the transaction stays open until Close.
	OpenSnapshot(ctx context.Context, t SQLTarget, logw io.Writer) (SnapshotSession, error)
	// PsqlOutput runs a SQL script like RestoreExecutor.Psql and writes its
	// stdout (tuples only, unaligned) to out.
	PsqlOutput(ctx context.Context, t SQLTarget, in io.Reader, out, logw io.Writer) error
}
