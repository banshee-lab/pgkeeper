package backup

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

const roleDollarTag = "$pgkeeper$"

// prepareRoles copies a `pg_dumpall --roles-only` script from src to dst and
// rewrites every `CREATE ROLE x;` so it no longer fails when the role already
// exists: an existing role is kept and the ALTER ROLE statements that follow
// in the script overwrite its attributes and password. A NOTICE logs each
// role that is being overwritten. It returns the number of roles defined.
func prepareRoles(src io.Reader, dst io.Writer) (int, error) {
	br := bufio.NewReader(src)
	bw := bufio.NewWriter(dst)
	roles := 0
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			out, isRole, rerr := rewriteCreateRole(line)
			if rerr != nil {
				return roles, rerr
			}
			if isRole {
				roles++
			}
			if _, werr := bw.WriteString(out); werr != nil {
				return roles, werr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return roles, err
		}
	}
	return roles, bw.Flush()
}

// rewriteCreateRole returns the replacement for one script line and whether
// the line was a CREATE ROLE statement.
func rewriteCreateRole(line string) (string, bool, error) {
	rest, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), "CREATE ROLE ")
	if !ok {
		return line, false, nil
	}
	ident, ok := strings.CutSuffix(rest, ";")
	if !ok || ident == "" {
		return line, false, nil
	}
	if strings.Contains(ident, roleDollarTag) {
		return "", false, fmt.Errorf("role name %s cannot be restored safely", ident)
	}
	lit := strings.ReplaceAll(ident, "'", "''")
	return fmt.Sprintf(
		"DO %[1]s BEGIN IF to_regrole('%[2]s') IS NULL THEN CREATE ROLE %[3]s; "+
			"ELSE RAISE NOTICE 'pgkeeper: role %% already exists, overwriting its attributes', '%[2]s'; END IF; END %[1]s;\n",
		roleDollarTag, lit, ident), true, nil
}
