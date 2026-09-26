package limits

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Response bodies captured from the live endpoints, trimmed to the fields
// the parsers read plus a few they must ignore.
const (
	codexUsageBody = `{"plan_type":"pro","email":"a@example.com","rate_limit":{"allowed":true,"limit_reached":false,
		"primary_window":{"used_percent":67,"limit_window_seconds":604800,"reset_after_seconds":1,"reset_at":1790430786},
		"secondary_window":{"used_percent":null,"limit_window_seconds":null,"reset_at":null}},"credits":{}}`
	grokBillingBody = `{"config":{"currentPeriod":{"type":"USAGE_PERIOD_TYPE_WEEKLY",
		"start":"2026-09-25T12:18:15.748273+00:00","end":"2026-10-02T12:18:15.748273+00:00"},
		"creditUsagePercent":1.0,"productUsage":[{"product":"GrokBuild","usagePercent":1.0}],"isUnifiedBillingUser":true}}`
)

// testNowMs is 2026-09-26T00:00:00Z: after every body's period start and
// before every reset in the bodies above.
const testNowMs = int64(1790380800000)

// fakeVendorBodies is what the fake vendor serves per path. Each provider's
// test file registers its own endpoint's body.
var fakeVendorBodies = map[string]string{"/codex": codexUsageBody, "/grok": grokBillingBody}

type recordedRequest struct {
	Path, Authorization, AccountID, Beta string
}

// fakeUsageVendor serves every vendor's usage endpoint and records each
// request, so a test can prove which token went where and how often.
type fakeUsageVendor struct {
	mu       sync.Mutex
	requests []recordedRequest
	status   int
}

func startFakeUsageVendor(t *testing.T) *fakeUsageVendor {
	t.Helper()
	vendor := &fakeUsageVendor{status: http.StatusOK}
	bodies := fakeVendorBodies
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vendor.mu.Lock()
		vendor.requests = append(vendor.requests, recordedRequest{
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			AccountID:     r.Header.Get("Chatgpt-Account-Id"),
			Beta:          r.Header.Get("anthropic-beta"),
		})
		status := vendor.status
		vendor.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte(bodies[r.URL.Path]))
	}))
	t.Cleanup(server.Close)
	t.Setenv("USAGEBAR_CODEX_USAGE_URL", server.URL+"/codex")
	t.Setenv("USAGEBAR_GROK_USAGE_URL", server.URL+"/grok")
	t.Setenv("USAGEBAR_CLAUDE_USAGE_URL", server.URL+"/claude")
	t.Setenv("USAGEBAR_USAGE_API_CACHE_PATH", filepath.Join(t.TempDir(), "usage-api-cache.json"))
	return vendor
}

func (v *fakeUsageVendor) hits(path string) []recordedRequest {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []recordedRequest
	for _, r := range v.requests {
		if r.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func (v *fakeUsageVendor) setStatus(status int) {
	v.mu.Lock()
	v.status = status
	v.mu.Unlock()
}

// writePiAuth writes a Pi auth.json whose tokens expire an hour after
// testNowMs unless expiresMs overrides it.
func writePiAuth(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	writeFile(t, filepath.Join(dir, "auth.json"), body)
}

const piCodexAndXAI = `{
	"openai-codex": {"type":"oauth","access":"pi-codex-token","refresh":"pi-codex-refresh","expires":1790384400000,"accountId":"acct-pi"},
	"xai": {"type":"oauth","access":"pi-xai-token","refresh":"pi-xai-refresh","expires":1790384400000}
}`

// collectPanelRows runs the same pipeline as cmd/usagebar collectPanel:
// installed logins filter the collectors, then the harness is applied and
// hidden rows are dropped.
func collectPanelRows(nowMs int64) []ProviderLimits {
	logins := InstalledLogins()
	opts := DefaultCollectOptions()
	opts.Only = map[string]bool{}
	for _, login := range logins {
		opts.Only[login.CollectorID] = true
	}
	return VisibleProviderLimits(ApplyLoginHarness(CollectAllProviderLimits(nil, nowMs, opts), logins))
}

func rowByID(t *testing.T, rows []ProviderLimits, id string) ProviderLimits {
	t.Helper()
	for _, row := range rows {
		if row.ProviderID == id {
			return row
		}
	}
	t.Fatalf("no %q row in %+v", id, rows)
	return ProviderLimits{}
}

func TestProviderLimitsFromCodexUsage(t *testing.T) {
	got, err := ProviderLimitsFromCodexUsage([]byte(codexUsageBody), "codex", "Codex", testNowMs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Primary == nil || got.Primary.UsedPercentage != 67 || *got.Primary.WindowMinutes != 10080 || *got.Primary.ResetsAt != 1790430786 {
		t.Fatalf("primary = %+v, want 67%% of a 7d window resetting at 1790430786", got.Primary)
	}
	if got.Secondary != nil {
		t.Fatalf("secondary = %+v, want nil for a null window", got.Secondary)
	}
	if got.PlanType == nil || *got.PlanType != "Pro" {
		t.Fatalf("plan = %v, want Pro", got.PlanType)
	}
	for name, body := range map[string]string{
		"not json":      `<html>`,
		"no rate_limit": `{"plan_type":"pro"}`,
		"no windows":    `{"rate_limit":{"primary_window":{"used_percent":null},"secondary_window":null}}`,
	} {
		if _, err := ProviderLimitsFromCodexUsage([]byte(body), "codex", "Codex", testNowMs); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestProviderLimitsFromGrokCLIBilling(t *testing.T) {
	got, err := ProviderLimitsFromGrokCLIBilling([]byte(grokBillingBody), "grok", "Grok", testNowMs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Primary == nil || got.Primary.UsedPercentage != 1 || *got.Primary.WindowMinutes != 10080 {
		t.Fatalf("primary = %+v, want 1%% of a 7d period", got.Primary)
	}
	if want := time.Date(2026, 10, 2, 12, 18, 15, 0, time.UTC).Unix(); *got.Primary.ResetsAt != want {
		t.Fatalf("resetsAt = %d, want period end %d", *got.Primary.ResetsAt, want)
	}
	for name, body := range map[string]string{
		"not json":   `nope`,
		"no config":  `{}`,
		"no percent": `{"config":{"currentPeriod":{}}}`,
	} {
		if _, err := ProviderLimitsFromGrokCLIBilling([]byte(body), "grok", "Grok", testNowMs); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

// The regression this change exists for: Pi-only Codex and xAI logins were
// found by InstalledLogins but dropped as hidden rows.
func TestPanel_PiOnlyLoginsShowLiveQuota(t *testing.T) {
	isolateInstalledHomes(t)
	vendor := startFakeUsageVendor(t)
	writePiAuth(t, piCodexAndXAI)

	rows := collectPanelRows(testNowMs)

	codexRow := rowByID(t, rows, "codex")
	if codexRow.Harness != "pi" || codexRow.Primary == nil || codexRow.Primary.UsedPercentage != 67 {
		t.Fatalf("codex row = %+v, want 67%% used via pi", codexRow)
	}
	grokRow := rowByID(t, rows, "grok")
	if grokRow.Harness != "pi" || grokRow.Primary == nil || grokRow.Primary.UsedPercentage != 1 {
		t.Fatalf("grok row = %+v, want 1%% used via pi", grokRow)
	}
	codexHits, grokHits := vendor.hits("/codex"), vendor.hits("/grok")
	if len(codexHits) != 1 || codexHits[0].Authorization != "Bearer pi-codex-token" || codexHits[0].AccountID != "acct-pi" {
		t.Fatalf("codex requests = %+v, want one with Pi's token and account id", codexHits)
	}
	if len(grokHits) != 1 || grokHits[0].Authorization != "Bearer pi-xai-token" {
		t.Fatalf("grok requests = %+v, want one with Pi's xAI token", grokHits)
	}

	panel := FormatLimitsPanel(rows, testNowMs, PanelLayout{Columns: 80, Rows: 40})
	for _, want := range []string{"Codex · via pi · Pro", "Grok · via pi", " 33% left", " 99% left"} {
		if !strings.Contains(panel, want) {
			t.Errorf("panel missing %q:\n%s", want, panel)
		}
	}
}

func TestPanel_ExpiredPiTokenIsReportedNotUsed(t *testing.T) {
	isolateInstalledHomes(t)
	vendor := startFakeUsageVendor(t)
	writePiAuth(t, `{
		"openai-codex":{"type":"oauth","access":"old-codex","refresh":"r","expires":1000,"accountId":"acct-pi"},
		"xai":{"type":"oauth","access":"old-xai","refresh":"r","expires":1000}
	}`)

	rows := collectPanelRows(testNowMs)
	for _, id := range []string{"codex", "grok"} {
		if n := len(vendor.hits("/" + id)); n != 0 {
			t.Fatalf("%s: %d requests, want 0: an expired token must not be sent", id, n)
		}
		row := rowByID(t, rows, id)
		if row.Hide || hasAnyWindow(row) || row.Note == nil || !strings.Contains(*row.Note, "pi login expired — run `pi auth check --provider ") {
			t.Fatalf("%s row = %+v, want a visible row saying the pi token expired", id, row)
		}
	}
}

func TestUsageAPI_ThrottledToOneRequestPerFiveMinutes(t *testing.T) {
	isolateInstalledHomes(t)
	vendor := startFakeUsageVendor(t)
	writePiAuth(t, piCodexAndXAI)

	collectPanelRows(testNowMs)
	collectPanelRows(testNowMs + 4*60_000)
	if n := len(vendor.hits("/codex")); n != 1 {
		t.Fatalf("requests within 5 minutes = %d, want 1", n)
	}
	collectPanelRows(testNowMs + 5*60_000)
	if n := len(vendor.hits("/codex")); n != 2 {
		t.Fatalf("requests after 5 minutes = %d, want 2", n)
	}
}

func TestUsageAPI_FailureIsShownAndAlsoThrottled(t *testing.T) {
	isolateInstalledHomes(t)
	vendor := startFakeUsageVendor(t)
	vendor.setStatus(http.StatusUnauthorized)
	writePiAuth(t, piCodexAndXAI)

	row := rowByID(t, collectPanelRows(testNowMs), "codex")
	if row.Hide || row.Note == nil || !strings.Contains(*row.Note, "HTTP 401") {
		t.Fatalf("row = %+v, want a visible row naming the HTTP 401", row)
	}
	collectPanelRows(testNowMs + 60_000)
	if n := len(vendor.hits("/codex")); n != 1 {
		t.Fatalf("requests after a failure = %d, want 1 (failures are throttled too)", n)
	}
}

func TestCodex_PiTokenForAnotherAccountDoesNotReplaceNativeRollout(t *testing.T) {
	isolateInstalledHomes(t)
	vendor := startFakeUsageVendor(t)
	writePiAuth(t, piCodexAndXAI)
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "auth.json"), `{"tokens":{"account_id":"acct-native"}}`)

	CollectCodexLimitsIn(home, "codex", "Codex", testNowMs)
	if n := len(vendor.hits("/codex")); n != 0 {
		t.Fatalf("requests = %d, want 0: Pi's login is a different account", n)
	}

	writeFile(t, filepath.Join(home, "auth.json"), `{"tokens":{"account_id":"acct-pi"}}`)
	got := CollectCodexLimitsIn(home, "codex", "Codex", testNowMs)
	if got.Primary == nil || got.Primary.UsedPercentage != 67 {
		t.Fatalf("same account: got %+v, want the live reading", got)
	}
}

func TestGrok_NativeMetersAreNotReplacedByPiToken(t *testing.T) {
	isolateInstalledHomes(t)
	vendor := startFakeUsageVendor(t)
	writePiAuth(t, piCodexAndXAI)
	authPath := filepath.Join(t.TempDir(), "auth.json")
	writeFile(t, authPath, `{"https://accounts.x.ai/sign-in":{"key":"native","email":"n@example.com"}}`)

	got := CollectGrokLimits(testNowMs, CollectGrokLimitsOptions{
		AuthPath:        authPath,
		FetchPlanTier:   func(string) *string { return nil },
		FetchWebBilling: func(string) *LimitWindow { return &LimitWindow{UsedPercentage: 40} },
	})
	if got.Primary == nil || got.Primary.UsedPercentage != 40 || len(vendor.hits("/grok")) != 0 {
		t.Fatalf("got %+v with %d requests, want native 40%% and no Pi request", got, len(vendor.hits("/grok")))
	}
}

func TestApplyLoginHarness_UnhidesRealCollectorRow(t *testing.T) {
	isolateInstalledHomes(t)
	row := CollectCodexLimitsIn(t.TempDir(), "codex", "Codex", testNowMs)
	if !row.Hide {
		t.Fatalf("precondition: an empty Codex home should be hidden, got %+v", row)
	}
	got := VisibleProviderLimits(ApplyLoginHarness([]ProviderLimits{row}, []LoginHarness{{CollectorID: "codex", Harness: "pi"}}))
	if len(got) != 1 || got[0].Harness != "pi" {
		t.Fatalf("got %+v, want the Pi login visible", got)
	}
}

func TestUsageAPICacheFresh(t *testing.T) {
	ok := &ProviderLimits{}
	cases := []struct {
		name  string
		entry usageAPICacheEntry
		now   int64
		want  bool
	}{
		{"fresh success", usageAPICacheEntry{FetchedAtMs: 1000, Limits: ok}, 1000 + usageAPICacheTTLMs - 1, true},
		{"fresh failure", usageAPICacheEntry{FetchedAtMs: 1000, Failure: "HTTP 500"}, 2000, true},
		{"expired", usageAPICacheEntry{FetchedAtMs: 1000, Limits: ok}, 1000 + usageAPICacheTTLMs, false},
		{"clock went backwards", usageAPICacheEntry{FetchedAtMs: 5000, Limits: ok}, 1000, false},
		{"empty entry", usageAPICacheEntry{FetchedAtMs: 1000}, 1000, false},
	}
	for _, c := range cases {
		if got := usageAPICacheFresh(c.entry, c.now); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
