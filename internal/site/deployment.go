package site

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Zero ports select the documented defaults. Origin remains the canonical
// control authority; changing a physical candidate never changes the issuer.
type Deployment struct {
	Mode           string
	LocalRelayPort int
	LocalSTUNPort  int
}

func validPort(port int) bool { return port > 0 && port <= 65535 }
func portOr(port, fallback int) int {
	if port == 0 {
		return fallback
	}
	return port
}

func (d Deployment) Validate(origin string) error {
	if d.Mode != "" && d.Mode != "diagnostic" && d.Mode != "443" {
		return errors.New("deployment mode must be diagnostic or 443")
	}
	if !validPort(portOr(d.LocalRelayPort, 8443)) || !validPort(portOr(d.LocalSTUNPort, 3478)) {
		return errors.New("invalid local Relay/STUN port")
	}
	if d.Mode == "443" {
		u, err := url.Parse(origin)
		if err != nil || (u.Port() != "" && u.Port() != "443") || (d.LocalRelayPort != 0 && d.LocalRelayPort != 443) {
			return errors.New("443 deployment requires control and public Relay port 443; use diagnostic for custom ports")
		}
	}
	return nil
}

func (a Auxiliary) PublicControlPort() int {
	if a.Mode == "443" {
		return portOr(a.ControlPort, 443)
	}
	return portOr(a.ControlPort, 8443)
}
func (a Auxiliary) PublicTunnelPort() int {
	if a.Mode == "443" {
		return portOr(a.TunnelPort, 443)
	}
	return portOr(a.TunnelPort, 7000)
}

func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, ":/\\\r\n\t \";@") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (a Auxiliary) Validate() error {
	if !ValidID(a.ID) || !validHostname(a.Host) || !validHostname(a.TunnelHost) || a.TokenFile == "" || (a.Mode != "diagnostic" && a.Mode != "443") {
		return errors.New("invalid auxiliary identity, host, secret path or mode")
	}
	r, err := url.Parse(a.Relay)
	if err != nil || r.Scheme != "rels" || !validHostname(r.Hostname()) || r.User != nil || r.Path != "" || r.RawQuery != "" || r.Fragment != "" {
		return errors.New("auxiliary Relay must be rels://host:port")
	}
	relayPort, err := strconv.Atoi(r.Port())
	if err != nil || !validPort(relayPort) || !validPort(a.PublicControlPort()) || !validPort(a.PublicTunnelPort()) {
		return errors.New("invalid auxiliary port")
	}
	host, port, err := net.SplitHostPort(strings.TrimPrefix(a.STUN, "stun:"))
	stunPort, pe := strconv.Atoi(port)
	if !strings.HasPrefix(a.STUN, "stun:") || err != nil || pe != nil || !validPort(stunPort) || !strings.EqualFold(host, r.Hostname()) {
		return errors.New("auxiliary STUN must be stun:relay-host:port")
	}
	if a.Mode == "443" {
		if relayPort != 443 || a.PublicControlPort() != 443 || a.PublicTunnelPort() != 443 {
			return errors.New("443 auxiliary requires public control, Relay and tunnel port 443")
		}
		if strings.EqualFold(r.Hostname(), a.TunnelHost) {
			return errors.New("Relay and tunnel SNI must differ")
		}
	} else if relayPort == a.PublicControlPort() || relayPort == a.PublicTunnelPort() || a.PublicControlPort() == a.PublicTunnelPort() {
		return errors.New("diagnostic auxiliary TCP listeners must use distinct ports")
	}
	if stunPort == relayPort {
		return errors.New("STUN and Relay QUIC UDP ports conflict")
	}
	return nil
}
