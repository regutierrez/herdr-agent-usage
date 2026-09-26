/**
 * The one HTTP GET every live usage request goes through, so timeout, size
 * cap and status handling are the same for every vendor. Vendor URLs and
 * headers stay in each provider's usage-API file.
 */
package limits

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	usageAPITimeout  = 10 * time.Second
	usageAPIMaxBytes = 1 << 20
)

// usageAPIEndpoint returns the override in envKey when set, else fallback.
// Tests point every endpoint at a local server through these variables.
func usageAPIEndpoint(envKey, fallback string) string {
	if v := os.Getenv(envKey); v != "" {
		return v
	}
	return fallback
}

// getUsageJSON performs an authenticated GET and returns the body of a 2xx
// response. The token is only ever placed in the Authorization header.
func getUsageJSON(url, accessToken string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	client := &http.Client{Timeout: usageAPITimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed")
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, usageAPIMaxBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return body, nil
}
