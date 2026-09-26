package limits

import (
	"path/filepath"
	"strings"
	"testing"
)

func isolateInstalledHomes(t *testing.T) {
	t.Helper()
	isolatePluginConfig(t)
	// Codex profiles resolve under $HOME/.codex regardless of CODEX_HOME, so
	// a fixture written there must land in a per-test home.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_JSON", filepath.Join(t.TempDir(), "missing-claude.json"))
	t.Setenv("USAGEBAR_CLAUDE_LIMITS_PATH", filepath.Join(t.TempDir(), "missing-limits.json"))
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	t.Setenv("OPENCODE_DB", filepath.Join(t.TempDir(), "missing.db"))
	t.Setenv("USAGEBAR_OMP_AGENT_DB", filepath.Join(t.TempDir(), "missing.db"))
	t.Setenv("USAGEBAR_STATE_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
}

func TestInstalledProviderFilter_PiOAuthRoutesCodexAndGrok(t *testing.T) {
	isolateInstalledHomes(t)
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeFile(t, filepath.Join(agentDir, "auth.json"), `{
		"openai-codex": {"type": "oauth", "access": "secret", "accountId": "acct"},
		"xai": {"type": "oauth", "access": "secret"},
		"github-copilot": {"type": "oauth", "access": "secret"}
	}`)

	got := InstalledProviderFilter()
	if !got["codex"] || !got["grok"] {
		t.Fatalf("got %v, want codex and grok from Pi auth.json", got)
	}
	if got["claude"] || got["opencode"] || got["antigravity"] {
		t.Fatalf("unrelated collectors must stay hidden: %v", got)
	}
	if got["github-copilot"] {
		t.Fatalf("unknown subscription must be omitted: %v", got)
	}
}

func TestInstalledProviderFilter_XaiAPIKeyDoesNotRouteToGrok(t *testing.T) {
	isolateInstalledHomes(t)
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeFile(t, filepath.Join(agentDir, "auth.json"), `{
		"xai": {"type": "api_key", "key": "secret"}
	}`)

	got := InstalledProviderFilter()
	if got["grok"] {
		t.Fatalf("api-key xai must not show Grok: %v", got)
	}
}

func TestInstalledProviderFilter_ClaudeJSONShowsClaude(t *testing.T) {
	isolateInstalledHomes(t)
	jsonPath := filepath.Join(t.TempDir(), ".claude.json")
	t.Setenv("CLAUDE_CONFIG_JSON", jsonPath)
	writeFile(t, jsonPath, `{"oauthAccount":{"billingType":"stripe_subscription"}}`)

	got := InstalledProviderFilter()
	if !got["claude"] {
		t.Fatalf("got %v, want claude from ~/.claude.json", got)
	}
}

func TestApplyLoginHarness_KeepsPiLoginWithoutQuotaSnapshot(t *testing.T) {
	note := "no rollout jsonl under ~/.codex/sessions"
	rows := []ProviderLimits{{
		ProviderID: "codex",
		Label:      "Codex",
		Source:     "none",
		Note:       &note,
		// What CollectCodexLimitsIn returns for a home with no rollouts.
		Hide: true,
	}, {
		ProviderID: "claude",
		Label:      "Claude",
		Source:     "claude.json cachedUsageUtilization",
		Note:       &note,
	}}
	got := ApplyLoginHarness(rows, []LoginHarness{
		{CollectorID: "codex", Harness: "pi"},
		{CollectorID: "claude", Harness: "claude"},
	})
	if got[0].Harness != "pi" || got[0].Hide || got[0].Note == nil || *got[0].Note != "no subscription quota snapshot available via pi" {
		t.Fatalf("pi login should remain visible without invented quota: %+v", got[0])
	}
	panel := FormatLimitsPanel(VisibleProviderLimits(got), 1_800_000_000_000, PanelLayout{Columns: 80, Rows: 30})
	if !strings.Contains(panel, "Codex · via pi") || !strings.Contains(panel, "no subscription quota snapshot available via pi") {
		t.Fatalf("panel should name the Pi login and missing quota: %q", panel)
	}
	if compact := compactLine(got[0], PanelLayout{Columns: 80}); !strings.Contains(compact, "Codex via pi") {
		t.Fatalf("compact panel should retain Pi attribution: %q", compact)
	}
	visible := VisibleProviderLimits(got)
	if len(visible) != 2 || visible[0].ProviderID != "codex" || visible[0].Primary != nil {
		t.Fatalf("visible = %+v, want codex with no quota and claude", visible)
	}
	if got[1].Harness != "claude" || got[1].Note == nil || *got[1].Note != note {
		t.Fatalf("native claude note should stay, harness should be named: %+v", got[1])
	}
}

func TestInstalledLogins_PrefersNativeCodexOverPi(t *testing.T) {
	isolateInstalledHomes(t)
	home := ResolvedCodexProfiles()[0].Home
	writeFile(t, filepath.Join(home, "auth.json"), `{"tokens":{"account_id":"acct-1"}}`)
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	writeFile(t, filepath.Join(agentDir, "auth.json"), `{"openai-codex":{"type":"oauth","access":"secret"}}`)

	got := InstalledLogins()
	for _, login := range got {
		if login.CollectorID == "codex" && login.Harness != "codex" {
			t.Fatalf("native Codex login should win, got %+v", got)
		}
	}
}

func TestInstalledProviderFilter_EmptyMachineHidesAll(t *testing.T) {
	isolateInstalledHomes(t)
	t.Setenv("HOME", t.TempDir())

	got := InstalledProviderFilter()
	if got == nil || len(got) != 0 {
		t.Fatalf("got %v, want empty set", got)
	}
}
