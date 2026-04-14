package proxy

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
)

// RewriteURL transforms legacy endpoint URLs to new format.
func RewriteURL(originalURL string) (string, error) {
	parsed, err := url.Parse(originalURL)
	if err != nil {
		return "", err
	}

	newPath := strings.Replace(parsed.Path, "/v1/", "/v2/", 1)
	parsed.Path = newPath

	return parsed.String(), nil
}

// FetchUpstreamConfig fetches configuration from upstream service.
func FetchUpstreamConfig(serviceURL string) ([]byte, error) { //nolint:gosec,noctx
	resp, err := http.Get(serviceURL) //nolint:gosec,noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return body, nil
}

// ExecuteHealthCheck runs a health check script.
func ExecuteHealthCheck(serviceName string) (string, error) {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("curl -s http://%s/health", serviceName)) //nolint:gosec
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}

// LegacyRedirect handles legacy redirect requests.
func LegacyRedirect(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("redirect_to")
	http.Redirect(w, r, target, http.StatusFound)
}
