/**
 * For each open pane, aggregates the per-provider token activity share
 * over the smallest window. Pure core with injectable deps.
 */
package limits

import (
	"os"
	"path/filepath"

	"github.com/senna-lang/herdr-agent-usage/internal/providers"
)

// OpenPaneSnapshot is one open agent pane used for share aggregation.
type OpenPaneSnapshot struct {
	PaneID    string
	Agent     string
	Label     string
	SessionID *string
	Cwd       *string
}

// agentToProvider maps a herdr pane agent id to ProviderLimits.providerId.
// Derived from providers.Registrations (identity for every registered agent)
// rather than duplicated, so a newly registered provider is picked up here
// automatically. Used as-is for the single-Claude-profile default; when
// multiple Claude profiles are configured, claude resolution instead goes
// through an injected PaneProviderResolver that can tell profiles apart (see
// BuildClaudePaneProviderResolver), since "claude" alone cannot say which
// profile a pane belongs to.
var agentToProvider = buildAgentToProvider()

func buildAgentToProvider() map[string]string {
	m := make(map[string]string, len(providers.All))
	for _, p := range providers.All {
		m[p.AgentID()] = p.AgentID()
	}
	return m
}

// PaneProviderResolver maps an open pane to the provider id its activity
// should be attributed to. ok=false means unresolved (e.g. a Claude pane whose
// session doesn't match any configured profile) — the pane is then excluded
// from every provider's activity rather than guessed into one.
type PaneProviderResolver func(pane OpenPaneSnapshot) (providerID string, ok bool)

// defaultPaneProviderResolver is an exact agent-id match. Used when
// PaneActivityDeps.ResolvePaneProvider is nil, which keeps pre-multi-profile
// callers (and their tests) unchanged.
func defaultPaneProviderResolver(pane OpenPaneSnapshot) (string, bool) {
	id, ok := agentToProvider[pane.Agent]
	return id, ok
}

// PaneActivityDeps injects token collectors for tests and I/O adapters.
type PaneActivityDeps struct {
	TokensForPane func(providerID string, pane OpenPaneSnapshot, windowStartMs, windowEndMs int64) float64
	// TotalTokensForProvider is total tokens across all sessions on disk (open + closed).
	TotalTokensForProvider func(providerID string, windowStartMs, windowEndMs int64) float64
	// ResolvePaneProvider attributes a pane to a provider id. nil defaults to
	// defaultPaneProviderResolver (today's single-Claude-profile behavior).
	ResolvePaneProvider PaneProviderResolver
}

// AttachPaneActivity attaches paneActivity by combining open panes with each provider's limits.
func AttachPaneActivity(
	providers []ProviderLimits,
	openPanes []OpenPaneSnapshot,
	nowMs int64,
	deps PaneActivityDeps,
) []ProviderLimits {
	resolve := deps.ResolvePaneProvider
	if resolve == nil {
		resolve = defaultPaneProviderResolver
	}
	// Resolve each pane once (not once per provider): a claude-family resolver
	// may scan disk, so this keeps cost O(panes) instead of O(providers*panes).
	paneProviderID := make(map[string]string, len(openPanes))
	for _, pane := range openPanes {
		if id, ok := resolve(pane); ok {
			paneProviderID[pane.PaneID] = id
		}
	}

	out := make([]ProviderLimits, len(providers))
	for i, p := range providers {
		out[i] = p

		var panesForProvider []OpenPaneSnapshot
		for _, pane := range openPanes {
			if paneProviderID[pane.PaneID] == p.ProviderID {
				panesForProvider = append(panesForProvider, pane)
			}
		}
		if len(panesForProvider) == 0 {
			continue
		}

		var primaryWM *int
		if p.Primary != nil {
			primaryWM = p.Primary.WindowMinutes
		}
		windowMinutes := ResolveActivityWindowMinutes(primaryWM)
		startMs := WindowStartMs(nowMs, windowMinutes)
		endMs := nowMs

		labels := paneShareLabels(panesForProvider)
		rawRows := make([]PaneTokenRow, len(panesForProvider))
		for j, pane := range panesForProvider {
			tokens := 0.0
			if deps.TokensForPane != nil {
				tokens = deps.TokensForPane(p.ProviderID, pane, startMs, endMs)
			}
			rawRows[j] = PaneTokenRow{PaneID: pane.PaneID, Label: labels[pane.PaneID], Tokens: tokens}
		}
		rows := DisambiguateLabels(rawRows)

		providerTotal := 0.0
		if deps.TotalTokensForProvider != nil {
			providerTotal = deps.TotalTokensForProvider(p.ProviderID, startMs, endMs)
		}
		totalTokens, panes := ComputeSharesWithOther(rows, providerTotal)
		if len(panes) == 0 {
			continue
		}
		// Convert float total to activity type - ProviderPaneActivity uses int total in TS
		// PaneActivityShare uses float tokens; TotalTokens stays int for display.
		activity := ProviderPaneActivity{
			WindowMinutes: windowMinutes,
			TotalTokens:   int(totalTokens),
			Panes:         panes,
		}
		out[i].PaneActivity = &activity
	}
	return out
}

// PaneRepoLabel names a pane by the directory it runs in — the repository,
// in practice — rather than by its Herdr tab title. A tab title is a tab
// number plus whatever the agent titled its chat ("2 · chezmoi › AWS SSO
// status"), which does not say why the pane is listed. Falls back to the
// pane's own label when the cwd is unknown.
func PaneRepoLabel(pane OpenPaneSnapshot) string {
	if pane.Cwd == nil || *pane.Cwd == "" {
		return pane.Label
	}
	cwd := filepath.Clean(*pane.Cwd)
	if home, err := os.UserHomeDir(); err == nil && cwd == filepath.Clean(home) {
		return "~"
	}
	base := filepath.Base(cwd)
	if base == "/" || base == "." {
		return pane.Label
	}
	return base
}

// paneShareLabels labels each pane by its repo, adding the agent when
// several panes share a repo ("chezmoi (pi)"). Panes still tied after that
// are told apart by DisambiguateLabels.
func paneShareLabels(panes []OpenPaneSnapshot) map[string]string {
	perRepo := map[string]int{}
	for _, pane := range panes {
		perRepo[PaneRepoLabel(pane)]++
	}
	labels := make(map[string]string, len(panes))
	for _, pane := range panes {
		label := PaneRepoLabel(pane)
		if perRepo[label] > 1 && pane.Agent != "" {
			label += " (" + pane.Agent + ")"
		}
		labels[pane.PaneID] = label
	}
	return labels
}
