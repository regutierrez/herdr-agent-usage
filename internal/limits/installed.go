/**
 * Decides which subscription collectors the Agent Usage pane should run
 * from what is installed and signed in, not from which pane happens to be
 * focused or open.
 *
 * A collector is present when its own harness has a login (or, for a
 * statusLine-only collector, a recorded snapshot). Harnesses that only
 * route into someone else's quota — Pi and OMP — contribute a collector
 * when one of their saved logins maps to that collector. A login that maps
 * nowhere is omitted: it is not shown as a guessed window or as API spend.
 */
package limits

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/senna-lang/herdr-agent-usage/internal/providers/antigravity"
	"github.com/senna-lang/herdr-agent-usage/internal/providers/claude"
	"github.com/senna-lang/herdr-agent-usage/internal/providers/codex"
	"github.com/senna-lang/herdr-agent-usage/internal/providers/grok"
	"github.com/senna-lang/herdr-agent-usage/internal/providers/omp"
	"github.com/senna-lang/herdr-agent-usage/internal/providers/opencode"
)

// Routing harnesses save logins for subscriptions they do not own.
const (
	routingHarnessOMP = "omp"
	routingHarnessPi  = "pi"
)

// LoginHarness names which agent holds a subscription login.
type LoginHarness struct {
	CollectorID string
	Harness     string
}

// InstalledLogins returns one entry per signed-in collector. Logins are
// registered native CLIs first, then OMP, then Pi, and the first harness to
// claim a collector keeps it: a native Claude login stays labeled Claude
// even when Pi also holds an Anthropic OAuth login.
func InstalledLogins() []LoginHarness {
	var out []LoginHarness
	add := func(id, harness string) {
		if id == "" {
			return
		}
		for _, existing := range out {
			if existing.CollectorID == id {
				return
			}
		}
		out = append(out, LoginHarness{CollectorID: id, Harness: harness})
	}
	if claudeInstalled(ResolvedClaudeProfiles()) {
		for _, profile := range ResolvedClaudeProfiles() {
			add(profile.ID, "claude")
		}
	}
	if codexInstalled(ResolvedCodexProfiles()) {
		for _, profile := range ResolvedCodexProfiles() {
			add(profile.ID, "codex")
		}
	}
	if grokInstalled(ResolvedGrokProfiles()) {
		for _, profile := range ResolvedGrokProfiles() {
			add(profile.ID, "grok")
		}
	}
	if openCodeInstalled(ResolvedOpenCodeProfiles()) {
		for _, profile := range ResolvedOpenCodeProfiles() {
			add(profile.ID, "opencode")
		}
	}
	if antigravityInstalled() {
		add("antigravity", "antigravity")
	}
	for _, cred := range omp.ListOMPCredentials() {
		if route, ok := SubscriptionRouteForProviderAuth(cred.Provider, cred.Type); ok {
			add(route.CollectorProviderID, routingHarnessOMP)
		}
	}
	for _, cred := range omp.ListPiCredentials(piAgentDir()) {
		if route, ok := SubscriptionRouteForProviderAuth(cred.Provider, cred.Type); ok {
			add(route.CollectorProviderID, routingHarnessPi)
		}
	}
	return out
}

// InstalledProviderFilter returns the collector ids to show. The set is
// never nil. An empty set means nothing on this machine is signed in.
func InstalledProviderFilter() map[string]bool {
	set := map[string]bool{}
	for _, login := range InstalledLogins() {
		set[login.CollectorID] = true
	}
	return set
}

// ApplyLoginHarness stamps each collected row with the harness that holds
// its login. A signed-in login is never hidden: a collector marks a row
// Hide when its own CLI files are absent, but a routing harness's login for
// that collector is still a login the user has. Such a row without a quota
// snapshot stays visible and says why it has no numbers.
func ApplyLoginHarness(rows []ProviderLimits, logins []LoginHarness) []ProviderLimits {
	byID := map[string]string{}
	for _, login := range logins {
		byID[login.CollectorID] = login.Harness
	}
	out := append([]ProviderLimits(nil), rows...)
	for i := range out {
		harness := byID[out[i].ProviderID]
		if harness == "" {
			continue
		}
		out[i].Harness = harness
		out[i].Hide = false
		if !strings.EqualFold(harness, out[i].ProviderID) && (out[i].Source == "none" || out[i].Source == "") {
			note := "no subscription quota snapshot available via " + harness
			out[i].Note = &note
		}
	}
	return out
}

// VisibleProviderLimits drops rows marked Hide.
func VisibleProviderLimits(rows []ProviderLimits) []ProviderLimits {
	out := make([]ProviderLimits, 0, len(rows))
	for _, row := range rows {
		if row.Hide {
			continue
		}
		out = append(out, row)
	}
	return out
}

func claudeInstalled(profiles []claude.ClaudeProfile) bool {
	for _, profile := range profiles {
		if regularFile(profile.JSONPath) || regularFile(profile.LimitsCache) {
			return true
		}
	}
	return false
}

func codexInstalled(profiles []codex.CodexProfile) bool {
	for _, profile := range profiles {
		if !regularFile(filepath.Join(profile.Home, "auth.json")) {
			continue
		}
		if codex.AccountIDIn(profile.Home) != "" {
			return true
		}
	}
	return false
}

func grokInstalled(profiles []grok.GrokProfile) bool {
	for _, profile := range profiles {
		path := filepath.Join(profile.Home, "auth.json")
		if profile.Home == "" {
			path = ResolveGrokAuthPath()
		}
		if regularFile(path) {
			return true
		}
	}
	return false
}

func openCodeInstalled(profiles []opencode.OpenCodeProfile) bool {
	for _, profile := range profiles {
		path := opencode.ResolveOpenCodeDBPathIn(profile.DataDir)
		if profile.DataDir == "" || profile.Implicit {
			path = opencode.ResolveOpenCodeDBPath()
		}
		if path != "" {
			return true
		}
	}
	return false
}

func antigravityInstalled() bool {
	dir := antigravity.SessionsDir(antigravity.StateDir())
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return true
		}
	}
	return false
}

func piAgentDir() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

func regularFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
