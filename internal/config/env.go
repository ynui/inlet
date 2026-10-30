package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadEnvFile applies KEY=VALUE pairs from path (e.g. ".env") without
// overriding variables that are already set. A missing file is not an error,
// so the file stays optional. Supports blank lines, '#' comments, an optional
// "export " prefix, and single/double quoted values.
func LoadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); exists {
			continue
		}
		os.Setenv(k, v)
	}
	return s.Err()
}
