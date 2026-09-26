package limits

import (
	"path/filepath"
	"strings"
	"testing"
)

// claudeUsageBody is an api/oauth/usage response captured from the live
// endpoint, trimmed to the fields the parser reads plus one it must ignore.
const claudeUsageBody = `{"five_hour":{"utilization":0.0,"resets_at":"2026-09-26T05:09:59.548395+00:00"},
		"seven_day":{"utilization":29.0,"resets_at":"2026-10-01T11:59:59.548417+00:00"},
		"limits":[{"kind":"session","percent":0,"resets_at":"2026-09-26T05:09:59.548395+00:00","scope":null},
		{"kind":"weekly_all","percent":29,"resets_at":"2026-10-01T11:59:59.548417+00:00","scope":null},
		{"kind":"weekly_scoped","percent":3,"resets_at":"2026-10-01T11:59:59.548580+00:00",
		"scope":{"model":{"id":null,"display_name":"Fable"},"surface":null}}],"seven_day_opus":null}`

func init() { fakeVendorBodies["/claude"] = claudeUsageBody }

func TestProviderLimitsFromClaudeUsage_IncludesFable(t *testing.T) {
	got, err := ProviderLimitsFromClaudeUsage([]byte(claudeUsageBody), testNowMs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Primary == nil || got.Primary.UsedPercentage != 0 || got.Secondary == nil || got.Secondary.UsedPercentage != 29 {
		t.Fatalf("5h/7d = %+v / %+v, want 0 / 29", got.Primary, got.Secondary)
	}
	if got.Fable == nil || got.Fable.UsedPercentage != 3 || *got.Fable.WindowMinutes != 10080 {
		t.Fatalf("fable = %+v, want 3%% weekly", got.Fable)
	}
	if _, err := ProviderLimitsFromClaudeUsage([]byte(`{"limits":[]}`), testNowMs); err == nil {
		t.Fatal("empty payload: want error")
	}
}

// claudeFixture writes a stale ~/.claude.json holding a Fable window and a
// newer statusLine cache without one: the layout on the machine that
// reported "no Fable row, stale".
func claudeFixture(t *testing.T) CollectClaudeLimitsOptions {
	t.Helper()
	dir := t.TempDir()
	jsonPath := filepath.Join(dir, ".claude.json")
	statusPath := filepath.Join(dir, "claude-limits-latest.json")
	fiveDaysAgo := itoa(int(testNowMs - 5*24*60*60_000))
	oneHourAgo := itoa(int(testNowMs - 60*60_000))
	writeFile(t, jsonPath, `{"cachedUsageUtilization":{"fetchedAtMs":`+fiveDaysAgo+`,"utilization":{
		"five_hour":{"utilization":10,"resets_at":"2026-09-26T03:00:00Z"},
		"seven_day":{"utilization":50,"resets_at":"2026-10-01T12:00:00Z"},
		"limits":[{"kind":"weekly_scoped","percent":6,"resets_at":"2026-10-01T12:00:00Z","scope":{"model":{"display_name":"Fable"}}}]}}}`)
	writeFile(t, statusPath, `{"fiveHour":{"usedPercentage":20,"resetsAt":1790391600,"windowMinutes":300},
		"sevenDay":{"usedPercentage":55,"resetsAt":1790856000,"windowMinutes":10080},"fetchedAtMs":`+oneHourAgo+`}`)
	return CollectClaudeLimitsOptions{
		ClaudeJSONPath:      jsonPath,
		StatusLineCachePath: statusPath,
		CredentialsPath:     filepath.Join(dir, ".credentials.json"),
	}
}

func TestClaude_NewerStatusLineNoLongerDropsFable(t *testing.T) {
	opts := claudeFixture(t)

	got := CollectClaudeLimits(testNowMs, opts)
	if got.Secondary == nil || got.Secondary.UsedPercentage != 55 {
		t.Fatalf("7d = %+v, want the newer statusLine 55%%", got.Secondary)
	}
	if got.Fable == nil || got.Fable.UsedPercentage != 6 {
		t.Fatalf("fable = %+v, want 6%% carried from ~/.claude.json", got.Fable)
	}
	if got.Note == nil || !strings.Contains(*got.Note, "Fable as of ~7200m ago (claude.json cachedUsageUtilization)") {
		t.Fatalf("note = %v, want the Fable window labeled with its own age", got.Note)
	}
}

func TestClaude_LiveUsageReplacesStaleCaches(t *testing.T) {
	vendor := startFakeUsageVendor(t)
	opts := claudeFixture(t)
	writeFile(t, opts.CredentialsPath, `{"claudeAiOauth":{"accessToken":"claude-token","refreshToken":"r","expiresAt":1790384400000}}`)

	got := CollectClaudeLimits(testNowMs, opts)
	hits := vendor.hits("/claude")
	if len(hits) != 1 || hits[0].Authorization != "Bearer claude-token" || hits[0].Beta != "oauth-2025-04-20" {
		t.Fatalf("claude requests = %+v, want one with the profile token and OAuth beta header", hits)
	}
	if got.Source != "claude usage API" || got.FetchedAtMs != testNowMs || got.Note != nil {
		t.Fatalf("got source=%q fetched=%d note=%v, want a fresh live reading with no stale note", got.Source, got.FetchedAtMs, got.Note)
	}
	if got.Fable == nil || got.Fable.UsedPercentage != 3 || got.Secondary.UsedPercentage != 29 {
		t.Fatalf("got fable=%+v 7d=%+v, want live 3%% / 29%%", got.Fable, got.Secondary)
	}
}

func TestClaude_ExpiredTokenFallsBackToCachesWithoutRequest(t *testing.T) {
	vendor := startFakeUsageVendor(t)
	opts := claudeFixture(t)
	writeFile(t, opts.CredentialsPath, `{"claudeAiOauth":{"accessToken":"old","expiresAt":1000}}`)

	got := CollectClaudeLimits(testNowMs, opts)
	if len(vendor.hits("/claude")) != 0 {
		t.Fatal("an expired Claude token must not be sent")
	}
	if got.Fable == nil || got.Secondary == nil || got.Secondary.UsedPercentage != 55 {
		t.Fatalf("got %+v, want the merged cached windows", got)
	}
}

func TestClaude_PiAnthropicLoginUsedOnlyWithoutNativeCredentials(t *testing.T) {
	vendor := startFakeUsageVendor(t)
	writePiAuth(t, `{"anthropic":{"type":"oauth","access":"pi-claude-token","expires":1790384400000}}`)
	opts := claudeFixture(t)

	got := CollectClaudeLimits(testNowMs, opts)
	if hits := vendor.hits("/claude"); len(hits) != 1 || hits[0].Authorization != "Bearer pi-claude-token" {
		t.Fatalf("requests = %+v, want Pi's Anthropic token", hits)
	}
	if got.Source != "claude usage API via pi" {
		t.Fatalf("source = %q", got.Source)
	}

	writeFile(t, opts.CredentialsPath, `{"claudeAiOauth":{"accessToken":"native","expiresAt":1790384400000}}`)
	t.Setenv("USAGEBAR_USAGE_API_CACHE_PATH", filepath.Join(t.TempDir(), "fresh-cache.json"))
	CollectClaudeLimits(testNowMs, opts)
	if hits := vendor.hits("/claude"); len(hits) != 2 || hits[1].Authorization != "Bearer native" {
		t.Fatalf("requests = %+v, want the native token once Claude Code is signed in", hits)
	}
}
