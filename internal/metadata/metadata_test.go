package metadata

import (
	"strings"
	"testing"
	"time"
)

func i64(v int64) *int64 { return &v }

const psqlOut = `BEGIN
{"pg_version":"16.2","database":"app","objects":{"tables":2,"indexes":3,"views":1,"matviews":0,"sequences":1,"functions":4,"constraints":5,"triggers":1},"table_rows":{"public.b":7,"public.a":42},"sequences":{"public.a_id_seq":42,"public.unused_seq":null}}
COMMIT
`

func TestParseAndMarshalSortedKeys(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.FixedZone("x", 3600))
	m, err := Parse([]byte(psqlOut), now)
	if err != nil {
		t.Fatal(err)
	}
	if m.CapturedAtUTC != "2026-11-12T09:00:00Z" || m.FormatVersion != FormatVersion || m.PGVersion != "16.2" || m.Database != "app" {
		t.Fatalf("context fields: %+v", m)
	}
	if m.Sequences["public.unused_seq"] != nil || *m.Sequences["public.a_id_seq"] != 42 {
		t.Fatalf("sequences: %+v", m.Sequences)
	}
	b, err := Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	last := -1
	for _, key := range []string{`"captured_at_utc"`, `"database"`, `"format_version"`, `"objects"`, `"pg_version"`, `"sequences": {`, `"table_rows"`} {
		i := strings.Index(s, key)
		if i <= last {
			t.Fatalf("key %s out of order in:\n%s", key, s)
		}
		last = i
	}
	if strings.Index(s, `"public.a"`) > strings.Index(s, `"public.b"`) {
		t.Fatalf("table_rows not sorted:\n%s", s)
	}
	back, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(m, back); len(d) != 0 {
		t.Fatalf("round trip differs: %v", d)
	}
}

func TestParseErrors(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "no json": "BEGIN\nCOMMIT\n", "bad json": "{nope", "incomplete": `{"pg_version":"16"}`} {
		if _, err := Parse([]byte(in), time.Now()); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := Unmarshal([]byte(`{"format_version":99}`)); err == nil {
		t.Error("unknown format version accepted")
	}
}

func TestDiffIgnoresContextAndReportsEachMismatch(t *testing.T) {
	a, _ := Parse([]byte(psqlOut), time.Now())
	b, _ := Parse([]byte(psqlOut), time.Now().Add(time.Hour))
	b.PGVersion, b.Database = "17.0", "restore_test"
	if d := Diff(a, b); len(d) != 0 {
		t.Fatalf("context fields must not be compared: %v", d)
	}
	b.Objects["indexes"] = 2
	b.TableRows["public.a"] = 41
	delete(b.TableRows, "public.b")
	b.TableRows["public.extra"] = 1
	b.Sequences["public.a_id_seq"] = i64(40)
	b.Sequences["public.unused_seq"] = i64(1)
	got := map[string]string{}
	for _, m := range Diff(a, b) {
		got[m.Section+"/"+m.Object] = m.Expected + "->" + m.Actual
	}
	want := map[string]string{
		"objects/indexes":              "3->2",
		"table_rows/public.a":          "42->41",
		"table_rows/public.b":          "7-><absent>",
		"table_rows/public.extra":      "<absent>->1",
		"sequences/public.a_id_seq":    "42->40",
		"sequences/public.unused_seq":  "null->1",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
}

func TestScript(t *testing.T) {
	s, err := Script("00000003-0000001B-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;\nSET TRANSACTION SNAPSHOT '00000003-0000001B-1';\n") {
		t.Fatalf("script: %s", s)
	}
	plain, _ := Script("")
	if strings.Contains(plain, "SET TRANSACTION SNAPSHOT") {
		t.Fatal("no snapshot expected")
	}
	if _, err := Script("1'; DROP DATABASE x; --"); err == nil {
		t.Fatal("snapshot id must be validated")
	}
}

func TestQueryIsReadOnlyAndExcludes(t *testing.T) {
	for _, bad := range []string{"CREATE ", "INSERT ", "UPDATE ", "DELETE ", "DROP ", "ALTER ", "TEMPORARY"} {
		if strings.Contains(strings.ToUpper(Query), bad) {
			t.Errorf("query contains %q", bad)
		}
	}
	for _, want := range []string{"pg_catalog", "information_schema", `pg\_toast%`, `pg\_temp%`, "deptype = 'e'", "query_to_xml", "k.contype <> 'n'"} {
		if !strings.Contains(Query, want) {
			t.Errorf("query missing %q", want)
		}
	}
	for _, typ := range ObjectTypes {
		if !strings.Contains(Query, "'"+typ+"'") {
			t.Errorf("query missing object type %s", typ)
		}
	}
}

func TestValidSnapshotID(t *testing.T) {
	for id, ok := range map[string]bool{"00000003-0000001B-1": true, "00000003-0000001B": true, "": false, "x y": false, "1;2": false} {
		if ValidSnapshotID(id) != ok {
			t.Errorf("%q: want %v", id, ok)
		}
	}
}
