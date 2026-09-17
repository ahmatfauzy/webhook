package delivery

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ValidateURL checks scheme and blocks private/metadata IPs when allowPrivate is false.
// allowlist CIDRs bypass blocking.
func ValidateURL(raw string, allowPrivate bool, allowlist []string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing hostname")
	}
	// localhost always considered private
	if strings.EqualFold(host, "localhost") {
		if !allowPrivate && !isAllowlisted(host, allowlist) {
			return fmt.Errorf("blocked private host: %s", host)
		}
		return nil
	}
	// try parse IP
	ip := net.ParseIP(host)
	if ip != nil {
		if !allowPrivate && !isAllowlisted(host, allowlist) {
			if isBlockedIP(ip) {
				return fmt.Errorf("blocked private IP: %s", host)
			}
		}
		return nil
	}
	// hostname not IP — for MVP, allow hostname but block known metadata hostnames
	if !allowPrivate {
		lower := strings.ToLower(host)
		if lower == "metadata.google.internal" || strings.HasSuffix(lower, ".metadata.google.internal") {
			return fmt.Errorf("blocked metadata host: %s", host)
		}
		// DNS rebinding check is done at send-time by resolving and re-validating IPs
	}
	return nil
}

func isAllowlisted(host string, cidrs []string) bool {
	if len(cidrs) == 0 {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range cidrs {
		_, net, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			continue
		}
		if net.Contains(ip) {
			return true
		}
	}
	return false
}

func isBlockedIP(ip net.IP) bool {
	// metadata
	if ip.Equal(net.ParseIP("169.254.169.254")) {
		return true
	}
	// loopback
	if ip.IsLoopback() {
		return true
	}
	// link-local
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	// private
	if ip.IsPrivate() {
		return true
	}
	// unspecified
	if ip.IsUnspecified() {
		return true
	}
	// 0.0.0.0/8
	if ip4 := ip.To4(); ip4 != nil {
		// check 0.0.0.0/8
		if ip4[0] == 0 {
			return true
		}
	}
	// IPv6 unique local fc00::/7
	// IsPrivate already covers, but explicitly
	return false
}
