package factory

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
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
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || !linkHost(u) ||
		u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(base, "#") {
		return fmt.Errorf("dashboard-url must be an absolute HTTP(S) base whose host is a DNS name or an IP address, without credentials, query or fragment")
	}
	return nil
}

// hostLabel is one name of a host's: letters, digits and hyphens, with
// neither end a hyphen.
var hostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// linkHost says whether a URL names a host a link can name: an IP address,
// or a DNS name, with a port in range if it gives one. Parsing takes more in
// a host, such as a parenthesis or a quote, and a link writes its host as it
// was given, where either would end or change the Markdown link of every
// compact comment. An address on this machine or network is still a host.
func linkHost(u *url.URL) bool {
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return false
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return false
	}
	host := u.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !hostLabel.MatchString(label) {
			return false
		}
	}
	return true
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
