package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

func (b Bootstrap) PublicBrokerURLs() []string {
	var out []string
	for _, target := range b.Brokers {
		if target.PublicURL != "" {
			out = append(out, strings.TrimRight(target.PublicURL, "/"))
		}
	}
	return out
}

var pinnedImage = regexp.MustCompile(`^(?:sha256:|[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:)[0-9a-f]{64}$`)

func validateRegionalFleet(b Bootstrap) error {
	if b.MemberAgentImage != "" && !pinnedImage.MatchString(b.MemberAgentImage) {
		return fmt.Errorf("bootstrap.member_agent_image requires an immutable sha256 image reference")
	}
	seen := map[string]bool{}
	count := 0
	for _, target := range b.Brokers {
		if target.PublicURL == "" {
			continue
		}
		count++
		u, err := url.Parse(target.PublicURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return fmt.Errorf("bootstrap.brokers.public_url requires an HTTPS origin")
		}
		key := strings.ToLower(u.Host)
		if seen[key] {
			return fmt.Errorf("duplicate regional broker public_url")
		}
		seen[key] = true
	}
	if count > 0 && (count != len(b.Brokers) || b.PublicBrokerQUICAddr != "") {
		return fmt.Errorf("regional broker fleet requires every public_url and no single QUIC address")
	}
	return nil
}
