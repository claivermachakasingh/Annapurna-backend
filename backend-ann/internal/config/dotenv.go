package config

import (
	"bufio"
	"os"
	"strings"
)

// LoadDotEnv reads a .env file and exports any KEY=value pairs into the
// process environment. Blank lines and comments (#) are ignored, quotes around
// values are stripped, and real OS environment variables always win so that
// `DATABASE_URL=... go run ./cmd/...` behaves as expected.
//
// A missing file is not an error: .env is a developer convenience, and every
// command must still work in CI or any environment that supplies real vars.
func LoadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		value = strings.TrimSpace(value)
		// Strip a single pair of matching quotes so values may contain spaces.
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}
