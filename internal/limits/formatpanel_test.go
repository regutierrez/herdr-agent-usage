/**
 * Tests for FormatLimitsPanel.
 * Color is disabled in the default layout so assertions use plain text.
 */
package limits

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/senna-lang/herdr-agent-usage/internal/core"
)

var wide = PanelLayout{Columns: 60, Rows: 9999, Color: false}

func sampleProvider() ProviderLimits {
	r1, r2 := int64(2_000_000_000), int64(2_000_500_000)
	wm1, wm2 := 300, 10080
	plan := "Plus"
	return ProviderLimits{
		ProviderID:  "codex",
		Label:       "Codex",
		Primary:     &LimitWindow{UsedPercentage: 10, ResetsAt: &r1, WindowMinutes: &wm1},
		Secondary:   &LimitWindow{UsedPercentage: 40, ResetsAt: &r2, WindowMinutes: &wm2},
		PlanType:    &plan,
		Source:      "rollout",
		FetchedAtMs: 1_700_000_000_000,
	}
}

func assertNoANSI(t *testing.T, text string) {
	t.Helper()
	if strings.Contains(text, "\x1b[") {
		t.Fatalf("unexpected ANSI in %q", text)
	}
}

func TestFormatProviderBlock_HeaderBar(t *testing.T) {
	text := FormatProviderBlock(sampleProvider(), wide, 1_700_000_000_000)
	for _, want := range []string{"Codex", "Plus", "5h", "7d", "90% left", "60% left", "█"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "source:") {
		t.Fatal("should not contain source")
	}
	assertNoANSI(t, text)
}

func TestFormatProviderBlock_UsedPercentInvertsNumberAndBarNotTone(t *testing.T) {
	layout := PanelLayout{Columns: 60, Rows: 9999, Color: false, LimitPercent: core.LimitPercentUsed}
	text := FormatProviderBlock(sampleProvider(), layout, 1_700_000_000_000)
	for _, want := range []string{"10% used", "40% used"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "left") {
		t.Fatalf("used mode must not say left:\n%s", text)
	}
	colored := FormatProviderBlock(sampleProvider(), PanelLayout{Columns: 60, Rows: 9999, Color: true, LimitPercent: core.LimitPercentUsed}, 0)
	if !strings.Contains(colored, "\x1b[32m") {
		t.Fatal("tone must stay remaining-based (green at 90% left / 10% used)")
	}
}

func TestFormatLimitsPanel_UsedCompactInvertsInlinePercent(t *testing.T) {
	layout := PanelLayout{Columns: 60, Rows: 9, Color: false, LimitPercent: core.LimitPercentUsed}
	compact := FormatLimitsPanel([]ProviderLimits{sampleProvider(), sampleProvider(), sampleProvider()}, 1_700_000_000_000, layout)
	if !strings.Contains(compact, "10%") || strings.Contains(compact, "90%") {
		t.Fatalf("compact used mode should show consumed percent:\n%s", compact)
	}
}

func TestCompactLine_ShowsUnavailableReason(t *testing.T) {
	note := "usage request failed"
	p := ProviderLimits{ProviderID: "claude", Label: "Claude", Note: &note, Unavailable: true}
	got := compactLine(p, PanelLayout{Columns: 60})
	if !strings.Contains(got, "no data: usage request failed") || strings.Contains(got, "%") {
		t.Fatalf("compact failure = %q", got)
	}
}

func TestFormatProviderBlock_EmDash(t *testing.T) {
	text := FormatProviderBlock(ProviderLimits{
		ProviderID: "opencode", Label: "OpenCode", Source: "none", FetchedAtMs: 0,
	}, wide, 0)
	// A row without windows shows its reason, not empty placeholder bars.
	if !strings.Contains(text, "no data yet") || strings.Contains(text, "—") || strings.Contains(text, "░") {
		t.Fatalf("got:\n%s", text)
	}
	if strings.Contains(text, "note:") {
		t.Fatal("note should not show")
	}
}

func TestFormatProviderBlock_PaneActivity(t *testing.T) {
	p := sampleProvider()
	p.PaneActivity = &ProviderPaneActivity{
		WindowMinutes: 300, TotalTokens: 1000,
		Panes: []PaneActivityShare{
			{PaneID: "w1:p1", Label: "codex-a", Tokens: 700, SharePercent: 70},
			{PaneID: "w1:p2", Label: "codex-b", Tokens: 300, SharePercent: 30},
		},
	}
	text := FormatProviderBlock(p, wide, 1_700_000_000_000)
	if want := "open panes used 100% of last 5h: codex-a 70%, codex-b 30%"; !strings.Contains(text, want) {
		t.Fatalf("missing %q:\n%s", want, text)
	}

	// With closed sessions in the mix the line leads with the open-pane
	// total, and the "closed / other" bucket is implied, not listed.
	p.PaneActivity.Panes = []PaneActivityShare{
		{PaneID: "w1:p1", Label: "chezmoi", Tokens: 26, SharePercent: 2.6},
		{PaneID: "w1:p2", Label: "henry", Tokens: 2, SharePercent: 0.2},
		{PaneID: OtherPaneID, Label: OtherLabel, Tokens: 972, SharePercent: 97.2},
	}
	text = FormatProviderBlock(p, wide, 1_700_000_000_000)
	if want := "open panes used 2.8% of last 5h: chezmoi 2.6%, henry 0.2%"; !strings.Contains(text, want) {
		t.Fatalf("missing %q:\n%s", want, text)
	}
	if strings.Contains(text, OtherLabel) || strings.Contains(text, "+1") {
		t.Fatalf("the closed bucket must not be listed:\n%s", text)
	}
}

func TestPaneActivityLine_OverflowCountsOnlyOpenPanes(t *testing.T) {
	panes := []PaneActivityShare{
		{PaneID: "a", Label: "alpha-repository", SharePercent: 30},
		{PaneID: "b", Label: "beta-repository", SharePercent: 20},
		{PaneID: "c", Label: "gamma-repository", SharePercent: 10},
		{PaneID: OtherPaneID, Label: OtherLabel, SharePercent: 40},
	}
	line := paneActivityLine(ProviderPaneActivity{WindowMinutes: 10080, Panes: panes}, PanelLayout{Columns: 60})
	if !strings.Contains(line, "open panes used 60% of last 7d: alpha-repository 30%") || !strings.HasSuffix(line, ", +2") {
		t.Fatalf("line = %q", line)
	}
	// The panel adds a one-column gutter, so a line may use columns-1.
	if plainWidth(line) > 60-1 {
		t.Fatalf("line wider than the pane: %d %q", plainWidth(line), line)
	}
}

func TestFormatProviderBlock_RunOutWarn(t *testing.T) {
	wm := 300
	plan := "Pro"
	text := FormatProviderBlock(ProviderLimits{
		ProviderID: "claude", Label: "Claude", PlanType: &plan, Source: "cache",
		Primary: &LimitWindow{
			UsedPercentage: 76, WindowMinutes: &wm,
			RunOut: &RunOutEstimate{MinutesToEmpty: 57, EmptyBeforeReset: true},
		},
	}, wide, 0)
	if !strings.Contains(text, "⚠ at this pace it runs out in ~57m") {
		t.Fatalf("got:\n%s", text)
	}
}

func TestRunOutText_WordedAgainstTheReset(t *testing.T) {
	now := int64(1_700_000_000_000)
	window := func(toEmptyMin float64, toResetMin int64) *LimitWindow {
		reset := now/1000 + toResetMin*60
		return &LimitWindow{ResetsAt: &reset, RunOut: &RunOutEstimate{MinutesToEmpty: toEmptyMin, EmptyBeforeReset: true}}
	}
	cases := []struct {
		name string
		w    *LimitWindow
		want string
	}{
		// The live case: ~4d 23h to empty against a 4d 23h 20m reset.
		{"lands on the reset", window(7140, 7160), "at this pace you'll use it all by the reset"},
		{"within the hour floor", window(100, 150), "at this pace you'll use it all by the reset"},
		{"well before the reset", window(2*24*60, 5*24*60), "at this pace it runs out in ~2d 0h, 3d 0h before the reset"},
		{"already empty", window(0, 60), "used up at this pace"},
	}
	for _, c := range cases {
		if got := runOutText(c.w, now); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatProviderBlock_NoRunOutWhenHolds(t *testing.T) {
	wm := 300
	holds := FormatProviderBlock(ProviderLimits{
		ProviderID: "codex", Label: "Codex", Source: "rollout",
		Primary: &LimitWindow{
			UsedPercentage: 20, WindowMinutes: &wm,
			RunOut: &RunOutEstimate{MinutesToEmpty: 600, EmptyBeforeReset: false},
		},
	}, wide, 0)
	if strings.Contains(holds, "at this pace") {
		t.Fatal("should not warn")
	}
	if strings.Contains(FormatProviderBlock(sampleProvider(), wide, 0), "at this pace") {
		t.Fatal("sample should not warn")
	}
}

func TestFormatProviderBlock_Color(t *testing.T) {
	text := FormatProviderBlock(sampleProvider(), PanelLayout{Columns: 60, Rows: 9999, Color: true}, 0)
	if !strings.Contains(text, "\x1b[") {
		t.Fatal("expected ANSI")
	}
}

func TestFormatLimitsPanel_Hints(t *testing.T) {
	text := FormatLimitsPanel([]ProviderLimits{sampleProvider()}, 1_700_000_000_000, wide)
	if strings.Contains(text, "Agent Usage") {
		t.Fatal("should not duplicate pane label")
	}
	if !strings.Contains(text, "q quit") || !strings.Contains(text, "Codex") {
		t.Fatalf("got:\n%s", text)
	}
}

func TestFormatLimitsPanel_Empty(t *testing.T) {
	text := FormatLimitsPanel(nil, 1_700_000_000_000, wide)
	if !strings.Contains(text, "no usage data") {
		t.Fatalf("got:\n%s", text)
	}
}

func TestFormatLimitsPanel_EmptyMessageOverride(t *testing.T) {
	layout := wide
	layout.EmptyMessage = "(no signed-in harness)"
	text := FormatLimitsPanel(nil, 1_700_000_000_000, layout)
	if !strings.Contains(text, "no signed-in harness") {
		t.Fatalf("got:\n%s", text)
	}
	if strings.Contains(text, "no usage data") {
		t.Fatalf("default message should be replaced, got:\n%s", text)
	}
}

func TestFormatLimitsPanel_CompactTier(t *testing.T) {
	three := []ProviderLimits{
		func() ProviderLimits { p := sampleProvider(); p.Label = "A"; return p }(),
		func() ProviderLimits { p := sampleProvider(); p.Label = "B"; return p }(),
		func() ProviderLimits { p := sampleProvider(); p.Label = "C"; return p }(),
	}
	rich := FormatLimitsPanel(three, 1_700_000_000_000, PanelLayout{Columns: 60, Rows: 9999, Color: false})
	compact := FormatLimitsPanel(three, 1_700_000_000_000, PanelLayout{Columns: 60, Rows: 9, Color: false})
	if len(strings.Split(compact, "\n")) >= len(strings.Split(rich, "\n")) {
		t.Fatal("compact should be shorter")
	}
	for _, name := range []string{"A", "B", "C", "90%"} {
		if !strings.Contains(compact, name) {
			t.Fatalf("missing %q", name)
		}
	}
}

func TestFormatLimitsPanel_CompactWarn(t *testing.T) {
	wm := 300
	warned := sampleProvider()
	warned.Primary = &LimitWindow{
		UsedPercentage: 90, WindowMinutes: &wm,
		RunOut: &RunOutEstimate{MinutesToEmpty: 20, EmptyBeforeReset: true},
	}
	compact := FormatLimitsPanel([]ProviderLimits{warned, warned, warned}, 1_700_000_000_000, PanelLayout{Columns: 60, Rows: 9, Color: false})
	if !strings.Contains(compact, "⚠") {
		t.Fatalf("got:\n%s", compact)
	}
}

func TestFormatLimitsPanel_NoSoftWrap(t *testing.T) {
	wm1, wm2 := 300, 10080
	r1, r2 := int64(2_000_000_000), int64(2_000_500_000)
	plan := "Pro"
	rich := ProviderLimits{
		ProviderID: "claude", Label: "Claude", PlanType: &plan, Source: "cache",
		Primary: &LimitWindow{
			UsedPercentage: 82, WindowMinutes: &wm1, ResetsAt: &r1,
			RunOut: &RunOutEstimate{MinutesToEmpty: 23, EmptyBeforeReset: true},
		},
		Secondary: &LimitWindow{
			UsedPercentage: 71, WindowMinutes: &wm2, ResetsAt: &r2,
			RunOut: &RunOutEstimate{MinutesToEmpty: 4080, EmptyBeforeReset: true},
		},
		PaneActivity: &ProviderPaneActivity{
			WindowMinutes: 300, TotalTokens: 100,
			Panes: []PaneActivityShare{
				{PaneID: "a", Label: "claude", Tokens: 81, SharePercent: 81.5},
				{PaneID: "b", Label: "claude", Tokens: 10, SharePercent: 10.5},
				{PaneID: "__other__", Label: "closed / other", Tokens: 8, SharePercent: 8},
			},
		},
	}
	for _, columns := range []int{20, 24, 30, 32, 38, 40, 44, 50, 60, 80} {
		for _, rows := range []int{8, 16, 40} {
			text := FormatLimitsPanel([]ProviderLimits{rich, rich, rich}, 1_700_000_000_000, PanelLayout{Columns: columns, Rows: rows, Color: false})
			for _, line := range strings.Split(text, "\n") {
				if utf8.RuneCountInString(line) > columns {
					t.Fatalf("line wider than %d: %q (%d)", columns, line, utf8.RuneCountInString(line))
				}
			}
		}
	}
}

func TestFormatLimitsPanel_RowBudget(t *testing.T) {
	many := make([]ProviderLimits, 4)
	for i := range many {
		p := sampleProvider()
		p.Label = "P" + string(rune('0'+i))
		many[i] = p
	}
	text := FormatLimitsPanel(many, 1_700_000_000_000, PanelLayout{Columns: 40, Rows: 15, Color: false})
	if len(strings.Split(text, "\n")) > 16 {
		t.Fatalf("too many lines: %d", len(strings.Split(text, "\n")))
	}
}

func TestFormatProviderBlock_HarnessInHeader(t *testing.T) {
	p := sampleProvider()
	p.Harness = "pi"
	text := FormatProviderBlock(p, wide, 1_700_000_000_000)
	if !strings.Contains(text, "via pi") {
		t.Fatalf("header missing harness:\\n%s", text)
	}
	if !strings.Contains(text, "Plus") {
		t.Fatalf("header dropped plan:\\n%s", text)
	}
}

func TestFormatProviderBlock_NativeHarnessIsNamed(t *testing.T) {
	p := sampleProvider()
	p.Harness = "codex"
	text := FormatProviderBlock(p, wide, 1_700_000_000_000)
	if !strings.Contains(text, "via codex") {
		t.Fatalf("native harness should be named:\\n%s", text)
	}
}

func TestFormatProviderBlock_Note(t *testing.T) {
	p := sampleProvider()
	note := "stale ~45m ago"
	p.Note = &note
	text := FormatProviderBlock(p, wide, 1_700_000_000_000)
	if !strings.Contains(text, "stale ~45m ago") {
		t.Fatalf("note not rendered:\n%s", text)
	}
}

func TestFormatProviderBlock_NoteTruncatesToFitWidth(t *testing.T) {
	narrow := PanelLayout{Columns: 20, Rows: 9999, Color: false}
	note := "a very long note that does not fit in a narrow pane at all"
	line := noteLine(&note, narrow)
	if utf8.RuneCountInString(line) > narrow.Columns+1 {
		t.Fatalf("note line exceeds width budget: %q", line)
	}
	if !strings.Contains(line, "…") {
		t.Fatalf("expected truncation ellipsis: %q", line)
	}
}

func TestFormatProviderBlock_NoNoteWhenAbsent(t *testing.T) {
	text := FormatProviderBlock(sampleProvider(), wide, 1_700_000_000_000)
	if strings.Contains(text, "…") {
		t.Fatalf("unexpected truncation with no note:\n%s", text)
	}
}

func claudeProfileSample(accountEmail string, usedPct float64) ProviderLimits {
	r1, r2 := int64(2_000_000_000), int64(2_000_500_000)
	wm1, wm2 := 300, 10080
	plan := "Pro"
	return ProviderLimits{
		ProviderID:   "claude-" + accountEmail,
		Label:        "claude-" + accountEmail,
		AccountLabel: accountEmail,
		GroupLabel:   "Claude",
		Primary:      &LimitWindow{UsedPercentage: usedPct, ResetsAt: &r1, WindowMinutes: &wm1},
		Secondary:    &LimitWindow{UsedPercentage: usedPct, ResetsAt: &r2, WindowMinutes: &wm2},
		PlanType:     &plan,
		Source:       "claude.json cachedUsageUtilization",
		FetchedAtMs:  1_700_000_000_000,
	}
}

func TestFormatUsagePanel_GroupsMultipleClaudeProfilesUnderSharedHeading(t *testing.T) {
	providers := []ProviderLimits{
		claudeProfileSample("you@example.com", 12),
		claudeProfileSample("colleague@example.com", 32),
	}
	text := FormatLimitsPanel(providers, 1_700_000_000_000, wide)
	if !strings.Contains(text, "Claude\n") && !strings.Contains(text, "Claude ") {
		t.Fatalf("expected a single shared Claude heading:\n%s", text)
	}
	for _, want := range []string{"you@example.com · Pro", "colleague@example.com · Pro"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing account line %q:\n%s", want, text)
		}
	}
	// Only one "Claude" heading, not one per account.
	if strings.Count(text, "Claude") != 1 {
		t.Fatalf("want exactly one Claude heading, got %d:\n%s", strings.Count(text, "Claude"), text)
	}
}

func TestFormatUsagePanel_SingleClaudeProfileNotGrouped(t *testing.T) {
	p := claudeProfileSample("you@example.com", 12)
	p.GroupLabel = ""
	p.AccountLabel = ""
	p.Label = "Claude"
	text := FormatLimitsPanel([]ProviderLimits{p}, 1_700_000_000_000, wide)
	if !strings.Contains(text, "Claude · Pro") {
		t.Fatalf("expected ungrouped single profile to keep its own header:\n%s", text)
	}
	if strings.Contains(text, "you@example.com") {
		t.Fatalf("single profile should not show an account email line:\n%s", text)
	}
}

func TestFormatUsagePanel_GroupsMultipleCodexProfilesUnderSharedHeading(t *testing.T) {
	providers := []ProviderLimits{
		codexProfileSample("personal", 4),
		codexProfileSample("product", 36),
	}
	text := FormatLimitsPanel(providers, 1_700_000_000_000, wide)
	if strings.Count(text, "Codex") != 1 {
		t.Fatalf("want exactly one Codex heading, got %d:\n%s", strings.Count(text, "Codex"), text)
	}
	for _, want := range []string{"personal", "product"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing account line %q:\n%s", want, text)
		}
	}
}

func TestFormatUsagePanel_LowCachePaneWarning(t *testing.T) {
	provider := sampleProvider()
	layout := PanelLayout{
		Columns: 80,
		Rows:    40,
		LowCachePanes: []LowCachePane{{
			Label:      "research",
			HitPercent: 43.1,
		}},
	}
	text := FormatUsagePanel([]ProviderLimits{provider}, nil, 1_700_000_000_000, layout)
	warning := "⚠ cache: research only 43% cached — each turn re-sends context"
	if !strings.Contains(text, warning) {
		t.Fatalf("missing low-cache warning:\n%s", text)
	}
	// It is about a pane, not the last provider block, so it sits below the
	// rule, just above the update line.
	if strings.Index(text, warning) <= strings.Index(text, ruleChar) || strings.Index(text, warning) >= strings.Index(text, "q quit") {
		t.Fatalf("warning must sit between the rule and the footer:\n%s", text)
	}

	layout.LowCachePanes = append(layout.LowCachePanes, LowCachePane{Label: "henry (pi)", HitPercent: 27.7})
	text = FormatUsagePanel([]ProviderLimits{provider}, nil, 1_700_000_000_000, layout)
	if want := "⚠ cache: research 43%, henry (pi) 28% cached — turns re-send context"; !strings.Contains(text, want) {
		t.Fatalf("missing %q:\n%s", want, text)
	}

	withoutWarnings := FormatUsagePanel([]ProviderLimits{provider}, nil, 1_700_000_000_000, PanelLayout{Columns: 80, Rows: 40})
	if strings.Contains(withoutWarnings, "⚠ cache") {
		t.Fatalf("unexpected cache warning:\n%s", withoutWarnings)
	}
}

func codexProfileSample(accountLabel string, usedPct float64) ProviderLimits {
	r1 := int64(2_000_000_000)
	wm1 := 10080
	return ProviderLimits{
		ProviderID:   "codex-" + accountLabel,
		Label:        "codex-" + accountLabel,
		AccountLabel: accountLabel,
		GroupLabel:   "Codex",
		Primary:      &LimitWindow{UsedPercentage: usedPct, ResetsAt: &r1, WindowMinutes: &wm1},
		Source:       "codex rollout",
		FetchedAtMs:  1_700_000_000_000,
	}
}

func TestFormatProviderBlock_NoDataReasonWrapsToTwoLines(t *testing.T) {
	note := "pi login expired — run `pi auth check --provider xai`"
	text := FormatProviderBlock(ProviderLimits{ProviderID: "grok", Label: "Grok", Harness: "pi", Note: &note}, PanelLayout{Columns: 60, Rows: 40}, 0)
	lines := strings.Split(text, "\n")
	if len(lines) != 3 {
		t.Fatalf("want header + two reason lines, got:\n%s", text)
	}
	joined := strings.TrimSpace(lines[1]) + " " + strings.TrimSpace(lines[2])
	if joined != "no data: "+note {
		t.Fatalf("reason split lost text: %q", joined)
	}
	for _, line := range lines {
		if plainWidth(line) > 60-1 {
			t.Fatalf("line wider than the pane: %q", line)
		}
	}
}

func TestResetsInText(t *testing.T) {
	for ms, want := range map[int64]string{
		0:                              "resets now",
		-5:                             "resets now",
		(4*60 + 27) * 60_000:           "resets in 4h 27m",
		(4*24*60 + 23*60 + 5) * 60_000: "resets in 4d 23h",
	} {
		if got := resetsInText(ms); got != want {
			t.Errorf("%d: got %q, want %q", ms, got, want)
		}
	}
}
