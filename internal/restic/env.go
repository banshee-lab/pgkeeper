package restic

import (
	"regexp"
	"strings"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

var inheritExact = map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true}

var inheritPrefixes = []string{"AWS_", "AZURE_", "B2_", "GOOGLE_", "RESTIC_", "RCLONE_"}

// keys pgkeeper sets itself; inherited values would conflict.
var managedKeys = map[string]bool{
	"RESTIC_REPOSITORY":       true,
	"RESTIC_REPOSITORY_FILE":  true,
	"RESTIC_PASSWORD":         true,
	"RESTIC_PASSWORD_FILE":    true,
	"RESTIC_PASSWORD_COMMAND": true,
	"RESTIC_CACHE_DIR":        true,
}

// inheritEnv keeps only PATH/HOME/TMPDIR and cloud-backend variables.
func inheritEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || managedKeys[k] {
			continue
		}
		if inheritExact[k] {
			out = append(out, kv)
			continue
		}
		for _, p := range inheritPrefixes {
			if strings.HasPrefix(k, p) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// buildEnv: the repository and password only ever travel through the environment.
func buildEnv(cfg domain.ResticConfig, cacheDir string, environ []string) []string {
	return append(inheritEnv(environ),
		"RESTIC_REPOSITORY="+cfg.Repository,
		"RESTIC_PASSWORD="+cfg.Password.Reveal(),
		"RESTIC_CACHE_DIR="+cacheDir,
	)
}

var repoPassRe = regexp.MustCompile(`://[^/:@\s]*:([^@/\s]*)@`)

// secretsFor lists every value that must never reach logs or error messages.
func secretsFor(cfg domain.ResticConfig, env []string) []string {
	s := []string{cfg.Password.Reveal()}
	if m := repoPassRe.FindStringSubmatch(cfg.Repository); m != nil {
		s = append(s, m[1])
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		u := strings.ToUpper(k)
		if v != "" && (strings.Contains(u, "SECRET") || strings.Contains(u, "PASSWORD") ||
			strings.Contains(u, "TOKEN") || strings.Contains(u, "KEY")) {
			s = append(s, v)
		}
	}
	return s
}