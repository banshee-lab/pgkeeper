package restic

import (
	"bytes"
	"io"
	"regexp"
	"sort"
	"strings"
)

var urlCredsRe = regexp.MustCompile(`(://)[^/@\s]*@`)

// RedactRepo turns rest:https://user:pass@host/ into rest:https://***@host/.
func RedactRepo(s string) string { return urlCredsRe.ReplaceAllString(s, "${1}***@") }

// Redactor replaces known secrets (and URL credentials) with ***.
type Redactor struct{ repl *strings.Replacer }

func NewRedactor(secrets ...string) *Redactor {
	uniq := map[string]struct{}{}
	for _, s := range secrets {
		if s != "" {
			uniq[s] = struct{}{}
		}
	}
	list := make([]string, 0, len(uniq))
	for s := range uniq {
		list = append(list, s)
	}
	// longest first so a secret containing another one is fully masked
	sort.Slice(list, func(i, j int) bool { return len(list[i]) > len(list[j]) })
	pairs := make([]string, 0, 2*len(list))
	for _, s := range list {
		pairs = append(pairs, s, "***")
	}
	return &Redactor{repl: strings.NewReplacer(pairs...)}
}

func (x *Redactor) Redact(s string) string { return RedactRepo(x.repl.Replace(s)) }

const maxRedactLine = 64 << 10

type redactWriter struct {
	x   *Redactor
	w   io.Writer
	buf []byte
}

// Writer returns a line-buffered writer: every complete line is redacted and
// written to w as a single Write call ending in "\n". Close flushes a partial line.
func (x *Redactor) Writer(w io.Writer) io.WriteCloser { return &redactWriter{x: x, w: w} }

func (r *redactWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			r.buf = append(r.buf, p...)
			if len(r.buf) > maxRedactLine {
				return n, r.flush()
			}
			break
		}
		r.buf = append(r.buf, p[:i]...)
		p = p[i+1:]
		if err := r.flush(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (r *redactWriter) flush() error {
	line := strings.ToValidUTF8(strings.TrimRight(string(r.buf), "\r"), "")
	r.buf = r.buf[:0]
	_, err := io.WriteString(r.w, r.x.Redact(line)+"\n")
	return err
}

func (r *redactWriter) Close() error {
	if len(r.buf) > 0 {
		return r.flush()
	}
	return nil
}