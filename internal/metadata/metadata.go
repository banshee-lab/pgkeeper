// Package metadata captures a schema-agnostic fingerprint of a PostgreSQL
// database (object counts, exact row counts, sequence positions) and compares
// two of them. It only reads the PostgreSQL catalog and never creates objects.
package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// FormatVersion is the version of the metadata file layout.
const FormatVersion = 1

// Metadata is the content of a <dump>.meta.json file. Fields are declared in
// alphabetical order of their JSON keys, and maps are sorted by encoding/json,
// so the file has sorted keys.
type Metadata struct {
	CapturedAtUTC string `json:"captured_at_utc"` // context, not compared
	Database      string `json:"database"`        // context, not compared
	FormatVersion int    `json:"format_version"`  // context, not compared
	// Objects counts user objects by type: tables, indexes, views, matviews,
	// sequences, functions, constraints, triggers.
	Objects   map[string]int64  `json:"objects"`
	PGVersion string            `json:"pg_version"` // context, not compared
	Sequences map[string]*int64 `json:"sequences"`  // last_value; null = never used
	TableRows map[string]int64  `json:"table_rows"` // exact count(*) per table
}

// ObjectTypes lists the keys of Metadata.Objects.
var ObjectTypes = []string{"tables", "indexes", "views", "matviews", "sequences", "functions", "constraints", "triggers"}

// notExcluded filters a catalog row: not in a system schema and not owned by
// an extension. %[1]s is the schema oid expression, %[2]s the class oid
// expression and %[3]s the pg_depend classid regclass of the object itself.
const userSchema = `(SELECT nspname FROM pg_namespace WHERE oid = %s) NOT IN ('pg_catalog','information_schema')
 AND (SELECT nspname FROM pg_namespace WHERE oid = %s) NOT LIKE 'pg\_toast%%'
 AND (SELECT nspname FROM pg_namespace WHERE oid = %s) NOT LIKE 'pg\_temp%%'`

func notExt(classid, objid string) string {
	return fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = '%s'::regclass AND d.objid = %s AND d.deptype = 'e')`, classid, objid)
}

func schemaOK(nsExpr string) string { return fmt.Sprintf(userSchema, nsExpr, nsExpr, nsExpr) }

// rel is the WHERE clause for pg_class rows aliased c.
var rel = schemaOK("c.relnamespace") + " AND " + notExt("pg_class", "c.oid")

// Query is the read-only catalog query. It returns one JSON document. Row
// counts use query_to_xml so no object is created in the database.
var Query = `SELECT json_build_object(
 'pg_version', current_setting('server_version'),
 'database', current_database(),
 'objects', json_build_object(
  'tables',      (SELECT count(*) FROM pg_class c WHERE c.relkind IN ('r','p') AND ` + rel + `),
  'indexes',     (SELECT count(*) FROM pg_index i JOIN pg_class c ON c.oid = i.indrelid WHERE ` + rel + `),
  'views',       (SELECT count(*) FROM pg_class c WHERE c.relkind = 'v' AND ` + rel + `),
  'matviews',    (SELECT count(*) FROM pg_class c WHERE c.relkind = 'm' AND ` + rel + `),
  'sequences',   (SELECT count(*) FROM pg_class c WHERE c.relkind = 'S' AND ` + rel + `),
  'functions',   (SELECT count(*) FROM pg_proc p WHERE p.prokind IN ('f','p','w') AND ` + schemaOK("p.pronamespace") + ` AND ` + notExt("pg_proc", "p.oid") + `),
  'constraints', (SELECT count(*) FROM pg_constraint k WHERE ` + schemaOK("k.connamespace") + ` AND ` + notExt("pg_constraint", "k.oid") + `
                    AND (k.conrelid = 0 OR NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid = 'pg_class'::regclass AND d.objid = k.conrelid AND d.deptype = 'e'))),
  'triggers',    (SELECT count(*) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid WHERE NOT t.tgisinternal AND ` + rel + `)
 ),
 'table_rows', COALESCE((SELECT json_object_agg(format('%I.%I', n.nspname, c.relname),
      (xpath('/row/c/text()', query_to_xml(format('SELECT count(*) AS c FROM %I.%I', n.nspname, c.relname), false, true, '')))[1]::text::bigint)
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind IN ('r','p') AND ` + rel + `), '{}'::json),
 'sequences', COALESCE((SELECT json_object_agg(format('%I.%I', n.nspname, c.relname), pg_sequence_last_value(c.oid))
    FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE c.relkind = 'S' AND ` + rel + `), '{}'::json)
);
`

var snapshotIDRe = regexp.MustCompile(`^[0-9A-Fa-f]+-[0-9A-Fa-f]+(-[0-9]+)?$`)

// ValidSnapshotID reports whether id looks like a pg_export_snapshot() id.
func ValidSnapshotID(id string) bool { return snapshotIDRe.MatchString(id) }

// Script is the SQL fed to psql for one capture. With a snapshot id it runs
// in session B: a REPEATABLE READ READ ONLY transaction that adopts the
// snapshot exported by session A. Without one it reads the current state.
func Script(snapshotID string) (string, error) {
	if snapshotID == "" {
		return "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\n" + Query + "COMMIT;\n", nil
	}
	if !ValidSnapshotID(snapshotID) {
		return "", fmt.Errorf("invalid snapshot id %q", snapshotID)
	}
	return "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSET TRANSACTION SNAPSHOT '" + snapshotID + "';\n" + Query + "COMMIT;\n", nil
}

// Parse reads the psql output of Query (the JSON line) and stamps the
// context fields.
func Parse(out []byte, now time.Time) (*Metadata, error) {
	var line string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "{") {
			line = l
		}
	}
	if line == "" {
		return nil, errors.New("no metadata JSON in psql output")
	}
	var m Metadata
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return nil, fmt.Errorf("parse metadata: %w", err)
	}
	if m.Objects == nil || m.TableRows == nil || m.Sequences == nil {
		return nil, errors.New("incomplete metadata from database")
	}
	m.FormatVersion = FormatVersion
	m.CapturedAtUTC = now.UTC().Format(time.RFC3339)
	return &m, nil
}

// Marshal encodes the metadata as indented JSON with sorted keys.
func Marshal(m *Metadata) ([]byte, error) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// Unmarshal decodes a metadata file and rejects unknown format versions.
func Unmarshal(b []byte) (*Metadata, error) {
	var m Metadata
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&m); err != nil {
		return nil, fmt.Errorf("parse metadata file: %w", err)
	}
	if m.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("unsupported metadata format_version %d", m.FormatVersion)
	}
	return &m, nil
}

// Diff lists every difference between expected and actual, ordered by
// section then object name. Context fields are not compared.
func Diff(expected, actual *Metadata) []domain.Mismatch {
	var out []domain.Mismatch
	out = append(out, diffMap("objects", expected.Objects, actual.Objects)...)
	out = append(out, diffMap("table_rows", expected.TableRows, actual.TableRows)...)
	out = append(out, diffSeq(expected.Sequences, actual.Sequences)...)
	return out
}

const absent = "<absent>"

func diffMap(section string, exp, act map[string]int64) []domain.Mismatch {
	var out []domain.Mismatch
	for _, k := range unionKeys(exp, act) {
		e, eok := exp[k]
		a, aok := act[k]
		if eok && aok && e == a {
			continue
		}
		out = append(out, domain.Mismatch{Section: section, Object: k, Expected: fmtOpt(e, eok), Actual: fmtOpt(a, aok)})
	}
	return out
}

func diffSeq(exp, act map[string]*int64) []domain.Mismatch {
	var out []domain.Mismatch
	for _, k := range unionKeys(exp, act) {
		e, eok := exp[k]
		a, aok := act[k]
		if eok && aok && ptrEq(e, a) {
			continue
		}
		out = append(out, domain.Mismatch{Section: "sequences", Object: k, Expected: fmtSeq(e, eok), Actual: fmtSeq(a, aok)})
	}
	return out
}

func ptrEq(a, b *int64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func fmtOpt(v int64, ok bool) string {
	if !ok {
		return absent
	}
	return strconv.FormatInt(v, 10)
}

func fmtSeq(v *int64, ok bool) string {
	switch {
	case !ok:
		return absent
	case v == nil:
		return "null"
	}
	return strconv.FormatInt(*v, 10)
}

func unionKeys[V any](a, b map[string]V) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
