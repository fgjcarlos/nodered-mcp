package config

import (
	"net"
	"strings"
)

// isLoopbackAddr reports whether a listen address reaches only this machine.
//
// It drives the rule that an exposed HTTP transport must carry a token. The
// distinction people get wrong is ":8090" — it reads as local but binds every
// interface, so the server is reachable from the whole network.
//
// Anything unparseable is reported as not loopback. Treating an address we do
// not understand as safe would disable the guard precisely where the operator's
// intent is least clear.
func isLoopbackAddr(addr string) bool {
	if addr == "" {
		return false
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = strings.TrimSpace(host)
	// A bare port, or an explicit wildcard, means every interface.
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A hostname other than localhost: it could resolve anywhere.
		return false
	}
	return ip.IsLoopback()
}
