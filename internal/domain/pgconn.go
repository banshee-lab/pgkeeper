package domain

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// pgURLParams maps the supported URL query parameters to the libpq
// environment variables that carry them into a tools container.
var pgURLParams = map[string]string{
	"sslmode":          "PGSSLMODE",
	"connect_timeout":  "PGCONNECT_TIMEOUT",
	"application_name": "PGAPPNAME",
	"options":          "PGOPTIONS",
	"channel_binding":  "PGCHANNELBINDING",
}

// PGConn is a parsed PostgreSQL connection URL
// (postgresql://[user[:password]@][host][:port][/dbname][?param=value&...]).
// It is handed to a tools container as libpq environment variables, so the
// password never appears on a command line.
type PGConn struct {
	Host     string
	Port     int
	User     string
	Password Secret
	Database string
	// Params holds the libpq options of the URL query (sslmode, ...), keyed by
	// their URL name.
	Params map[string]string
}

// ParsePGURL parses a postgres:// or postgresql:// URL. Errors never contain
// the URL itself, because it may carry a password.
func ParsePGURL(raw string) (PGConn, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return PGConn{}, fmt.Errorf("malformed URL: %v", ue.Err)
		}
		return PGConn{}, errors.New("malformed URL")
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return PGConn{}, errors.New("scheme must be postgresql:// (or postgres://)")
	}
	c := PGConn{Host: u.Hostname(), Port: 5432, User: "postgres"}
	if c.Host == "" {
		return PGConn{}, errors.New("host is required")
	}
	if strings.Contains(u.Host, ",") {
		return PGConn{}, errors.New("multiple hosts are not supported")
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return PGConn{}, errors.New("port must be between 1 and 65535")
		}
		c.Port = n
	}
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			c.User = name
		}
		if pw, ok := u.User.Password(); ok {
			c.Password = Secret(pw)
		}
	}
	c.Database = strings.TrimPrefix(u.Path, "/")
	if strings.Contains(c.Database, "/") {
		return PGConn{}, errors.New("database name must not contain '/'")
	}
	for key, vals := range u.Query() {
		if _, ok := pgURLParams[key]; !ok {
			return PGConn{}, fmt.Errorf("unsupported parameter %q (supported: %s)", key, strings.Join(SupportedPGURLParams(), ", "))
		}
		if len(vals) != 1 {
			return PGConn{}, fmt.Errorf("parameter %q given more than once", key)
		}
		if c.Params == nil {
			c.Params = map[string]string{}
		}
		c.Params[key] = vals[0]
	}
	for _, v := range []string{c.Host, c.User, c.Password.Reveal(), c.Database} {
		if strings.ContainsAny(v, "\x00\n\r") {
			return PGConn{}, errors.New("values must not contain NUL or newline characters")
		}
	}
	for k, v := range c.Params {
		if strings.ContainsAny(v, "\x00\n\r") {
			return PGConn{}, fmt.Errorf("parameter %q must not contain NUL or newline characters", k)
		}
	}
	return c, nil
}

// SupportedPGURLParams lists the accepted URL query parameters, sorted.
func SupportedPGURLParams() []string {
	keys := make([]string, 0, len(pgURLParams))
	for k := range pgURLParams {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Env returns the libpq environment (NAME=value) that makes a tool reach this
// server. PGDATABASE is only set when the URL names a database.
func (c PGConn) Env() []string {
	env := []string{"PGHOST=" + c.Host, "PGPORT=" + strconv.Itoa(c.Port), "PGUSER=" + c.User}
	if c.Password != "" {
		env = append(env, "PGPASSWORD="+c.Password.Reveal())
	}
	if c.Database != "" {
		env = append(env, "PGDATABASE="+c.Database)
	}
	for _, k := range SupportedPGURLParams() {
		if v, ok := c.Params[k]; ok {
			env = append(env, pgURLParams[k]+"="+v)
		}
	}
	return env
}

// Redacted returns the URL without its password, for logs and the API.
func (c PGConn) Redacted() string {
	u := url.URL{Scheme: "postgresql", Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), User: url.User(c.User)}
	if c.Database != "" {
		u.Path = "/" + c.Database
	}
	return u.String()
}
