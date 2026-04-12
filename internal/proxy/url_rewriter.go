package proxy

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
)

// RewriteURL transforms legacy endpoint URLs to new format
func RewriteURL(originalURL string) (string, error) {
	// SSRF: No validation on URL scheme or host
	parsed, err := url.Parse(originalURL)
	if err != nil {
		return "", err
	}

	// Allows internal network access
	// No blocklist for internal IPs (10.x, 172.x, 192.168.x, localhost)
	newPath := strings.Replace(parsed.Path, "/v1/", "/v2/", 1)
	parsed.Path = newPath

	return parsed.String(), nil
}

// FetchUpstreamConfig fetches configuration from upstream service
func FetchUpstreamConfig(serviceURL string) ([]byte, error) {
	// SSRF: User-controlled URL fetched without validation
	resp, err := http.Get(serviceURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Using deprecated ioutil
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// ExecuteHealthCheck runs a health check script
func ExecuteHealthCheck(serviceName string) (string, error) {
	// Command injection: serviceName is not sanitized
	cmd := exec.Command("sh", "-c", fmt.Sprintf("curl -s http://%s/health", serviceName))
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// Legacy redirect handler
func LegacyRedirect(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("redirect_to")
	// Open redirect vulnerability - no validation on target
	http.Redirect(w, r, target, http.StatusFound)
}
