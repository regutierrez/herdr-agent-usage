package omp

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestListPiCredentials_CarriesBearerFieldsButNotRefreshToken(t *testing.T) {
	dir := t.TempDir()
	body := `{
		"openai-codex": {"type":"oauth","access":" codex-access ","refresh":"codex-refresh","expires":1790384400000,"accountId":"acct-1"},
		"xai": {"type":"oauth","access":"xai-access","refresh":"xai-refresh","expires":1790384400000},
		"OpenRouter": {"type":"api_key","key":"sk-secret"}
	}`
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := ListPiCredentials(dir)
	sort.Slice(got, func(i, j int) bool { return got[i].Provider < got[j].Provider })
	want := []AuthCredential{
		{Provider: "openai-codex", Type: "oauth", AccessToken: "codex-access", AccountID: "acct-1", ExpiresAtMs: 1790384400000},
		{Provider: "openrouter", Type: "api_key"},
		{Provider: "xai", Type: "oauth", AccessToken: "xai-access", ExpiresAtMs: 1790384400000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	for _, cred := range got {
		if strings.Contains(cred.AccessToken+cred.AccountID, "refresh") || strings.Contains(cred.AccessToken, "sk-secret") {
			t.Fatalf("only the OAuth access token may be copied out: %+v", cred)
		}
	}
}

func TestListPiCredentials_MissingOrInvalidFile(t *testing.T) {
	if got := ListPiCredentials(t.TempDir()); got != nil {
		t.Fatalf("missing file: got %+v", got)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ListPiCredentials(dir); got != nil {
		t.Fatalf("invalid file: got %+v", got)
	}
	if got := ListPiCredentials(""); got != nil {
		t.Fatalf("empty dir: got %+v", got)
	}
}
