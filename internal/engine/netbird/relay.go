package netbird

import (
	"net/url"
	"strings"
)

func relayMatches(actual, expected string) bool {
	parse := func(value string) (*url.URL, error) {
		if !strings.Contains(value, "://") {
			value = "//" + value
		}
		return url.Parse(value)
	}
	a, err := parse(actual)
	e, other := parse(expected)
	if err != nil || other != nil || a.Hostname() == "" || e.Hostname() == "" ||
		a.User != nil || e.User != nil || a.RawQuery != "" || a.Fragment != "" ||
		(a.Path != "" && a.Path != "/") {
		return false
	}
	return strings.EqualFold(a.Hostname(), e.Hostname()) && (e.Port() == "" || a.Port() == e.Port())
}
