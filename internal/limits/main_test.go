/**
 * Package-wide test defaults.
 */
package limits

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain isolates the package from the developer's machine.
//
// Collectors fall back to another agent's observations, read OAuth tokens
// saved by Claude Code and Pi, and call vendor usage endpoints with them.
// Without this a developer's real ~/.omp agent.db, ~/.pi/agent/auth.json,
// ~/.claude/.credentials.json and ~/.codex would leak into every "no data"
// assertion, tests would make real network requests with real tokens, and
// fixtures written under the home directory would land in the real one.
// A test that wants any of these overrides the variable with t.Setenv.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "usagebar-limits-home-")
	if err != nil {
		panic(err)
	}
	absent := filepath.Join(home, "absent")
	unreachable := "http://127.0.0.1:1/unreachable"
	for key, value := range map[string]string{
		"HOME":                          home,
		"USAGEBAR_OMP_AGENT_DB":         filepath.Join(absent, "agent.db"),
		"PI_CODING_AGENT_DIR":           filepath.Join(absent, "pi"),
		"USAGEBAR_USAGE_API_CACHE_PATH": filepath.Join(home, "usage-api-cache.json"),
		"USAGEBAR_CODEX_USAGE_URL":      unreachable,
		"USAGEBAR_GROK_USAGE_URL":       unreachable,
		"USAGEBAR_CLAUDE_USAGE_URL":     unreachable,
	} {
		_ = os.Setenv(key, value)
	}
	for _, key := range []string{"CODEX_HOME", "GROK_HOME", "CLAUDE_CONFIG_DIR", "CLAUDE_CONFIG_JSON", "USAGEBAR_CLAUDE_LIMITS_PATH", "USAGEBAR_HISTORY_PATH"} {
		_ = os.Unsetenv(key)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
