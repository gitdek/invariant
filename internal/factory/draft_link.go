package factory

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ValidateDashboardURL accepts an optional installation-specific HTTP base.
// It checks syntax only; the operator selects a dashboard for this repository.
func ValidateDashboardURL(base string) error {
	if base == "" {
		return nil
	}
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
		u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") {
		return fmt.Errorf("dashboard-url must be an absolute HTTP(S) base without credentials, query or fragment")
	}
	return nil
}

// URL names the exact saved record through the dashboard's durable locator.
// Escape components independently, retaining any deployment path prefix.
func (r DraftReference) URL(base string) (string, error) {
	if err := ValidateDashboardURL(base); err != nil {
		return "", err
	}
	if base == "" {
		return "", fmt.Errorf("dashboard-url is not configured")
	}
	parts := strings.Split(r.Repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || r.Version != 1 || r.Issue <= 0 ||
		!draftCommitRE.MatchString(r.Commit) || !draftDigestRE.MatchString(r.SHA256) {
		return "", fmt.Errorf("invalid saved-draft locator")
	}
	u, _ := url.Parse(base)
	path := strings.TrimRight(u.EscapedPath(), "/") + "/draft/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) +
		"/" + strconv.Itoa(r.Issue) + "/" + url.PathEscape(r.Commit) + "/" + strings.TrimPrefix(r.SHA256, "sha256:")
	// Parentheses are valid URL path text but delimit Markdown destinations.
	path = strings.NewReplacer("(", "%28", ")", "%29").Replace(path)
	return u.Scheme + "://" + u.Host + path, nil
}
