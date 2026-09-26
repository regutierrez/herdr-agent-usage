package omp

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// AuthCredential is one saved login. Provider and Type identify it; the
// bearer fields are filled only where the harness stores them in a format
// this adapter can read (Pi's auth.json). They are handed to the quota
// collector for the same subscription and are never written anywhere.
type AuthCredential struct {
	Provider string
	Type     string
	// AccessToken is the OAuth bearer token. Empty when not readable.
	AccessToken string
	// AccountID is the vendor account id when the login records one.
	AccountID string
	// ExpiresAtMs is the access token expiry in epoch ms. 0 means unknown.
	ExpiresAtMs int64
}

// ListOMPCredentials returns every enabled OMP login as provider plus
// credential kind. Secrets in auth_credentials.data are never read, so OMP
// logins carry no bearer fields.
func ListOMPCredentials() []AuthCredential {
	dbPath := ResolveAgentDBPath()
	if dbPath == "" {
		return nil
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT provider, credential_type FROM auth_credentials WHERE disabled_cause IS NULL`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []AuthCredential
	for rows.Next() {
		var cred AuthCredential
		if rows.Scan(&cred.Provider, &cred.Type) != nil {
			continue
		}
		cred.Provider = strings.ToLower(strings.TrimSpace(cred.Provider))
		cred.Type = strings.ToLower(strings.TrimSpace(cred.Type))
		if cred.Provider == "" {
			continue
		}
		out = append(out, cred)
	}
	return out
}

// CredentialType returns the active OMP credential kind for a provider
// without reading its secret material. OMP records OAuth and API-key
// credentials separately in agent.db.
func CredentialType(provider string) string {
	dbPath := ResolveAgentDBPath()
	if dbPath == "" {
		return ""
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return ""
	}
	defer db.Close()
	var kind string
	err = db.QueryRow(`SELECT credential_type FROM auth_credentials WHERE provider = ? AND disabled_cause IS NULL ORDER BY updated_at DESC LIMIT 1`, provider).Scan(&kind)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(kind))
}

// PiCredentialType reads only the credential kind from Pi's auth.json. Pi
// stores provider entries as JSON objects; tokens are intentionally ignored.
func PiCredentialType(provider string) string {
	return piCredentialTypeIn(defaultPiAgentDir(), provider)
}

// ListPiCredentials returns every provider login in a Pi agent dir: the
// provider id, credential kind, and for OAuth logins the access token,
// account id and expiry Pi saved. The refresh token is never read: using it
// would rotate Pi's login out from under Pi.
func ListPiCredentials(agentDir string) []AuthCredential {
	entries := readPiAuth(agentDir)
	if len(entries) == 0 {
		return nil
	}
	var out []AuthCredential
	for provider, entry := range entries {
		kind := credentialKind(entry)
		if kind == "" && len(entry) == 0 {
			continue
		}
		out = append(out, AuthCredential{
			Provider:    strings.ToLower(strings.TrimSpace(provider)),
			Type:        kind,
			AccessToken: stringField(entry, "access"),
			AccountID:   stringField(entry, "accountId"),
			ExpiresAtMs: int64Field(entry, "expires"),
		})
	}
	return out
}

func piCredentialTypeIn(agentDir, provider string) string {
	return credentialKind(readPiAuth(agentDir)[provider])
}

func defaultPiAgentDir() string {
	if dir := os.Getenv("PI_CODING_AGENT_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi", "agent")
}

func readPiAuth(agentDir string) map[string]map[string]any {
	if agentDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		return nil
	}
	var entries map[string]map[string]any
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	return entries
}

func credentialKind(entry map[string]any) string {
	for _, key := range []string{"type", "credential_type", "auth_type"} {
		if value, ok := entry[key].(string); ok {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func stringField(entry map[string]any, key string) string {
	value, _ := entry[key].(string)
	return strings.TrimSpace(value)
}

// int64Field reads a JSON number. encoding/json decodes numbers into
// float64, which holds epoch milliseconds exactly.
func int64Field(entry map[string]any, key string) int64 {
	value, ok := entry[key].(float64)
	if !ok || value <= 0 {
		return 0
	}
	return int64(value)
}
