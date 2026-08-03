package vial

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// ClientIP returns the direct peer unless it is an explicitly trusted proxy.
// Trusted proxy chains are evaluated from the application outward, stopping at
// the first untrusted address.
func (context *Context) ClientIP() (netip.Addr, error) {
	peer, err := parseRequestAddress(context.request.RemoteAddr)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("client IP remote address: %w", err)
	}
	if !context.app.isTrustedProxy(peer) {
		return peer, nil
	}

	chain, err := forwardedChain(context.request)
	if err != nil {
		return netip.Addr{}, err
	}
	client := peer
	for index := len(chain) - 1; index >= 0 && context.app.isTrustedProxy(client); index-- {
		client = chain[index]
	}
	return client, nil
}

func (app *App) isTrustedProxy(address netip.Addr) bool {
	address = address.Unmap()
	for _, prefix := range app.config.trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func forwardedChain(request *http.Request) ([]netip.Addr, error) {
	if value := strings.Join(request.Header.Values("Forwarded"), ","); value != "" {
		return parseForwarded(value)
	}
	if value := strings.Join(request.Header.Values("X-Forwarded-For"), ","); value != "" {
		return parseAddressList(value, "X-Forwarded-For")
	}
	if value := strings.TrimSpace(request.Header.Get("X-Real-IP")); value != "" {
		address, err := parseRequestAddress(value)
		if err != nil {
			return nil, fmt.Errorf("X-Real-IP: %w", err)
		}
		return []netip.Addr{address}, nil
	}
	return nil, nil
}

func parseForwarded(value string) ([]netip.Addr, error) {
	addresses := make([]netip.Addr, 0, strings.Count(value, ",")+1)
	for _, element := range strings.Split(value, ",") {
		found := false
		for _, parameter := range strings.Split(element, ";") {
			name, raw, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(name, "for") {
				continue
			}
			raw = strings.TrimSpace(raw)
			if strings.HasPrefix(raw, "\"") {
				unquoted, err := strconv.Unquote(raw)
				if err != nil {
					return nil, fmt.Errorf("forwarded: invalid quoted for value")
				}
				raw = unquoted
			}
			address, err := parseRequestAddress(raw)
			if err != nil {
				return nil, fmt.Errorf("forwarded for=%q: %w", raw, err)
			}
			addresses = append(addresses, address)
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("forwarded: element has no valid for parameter")
		}
	}
	return addresses, nil
}

func parseAddressList(value, header string) ([]netip.Addr, error) {
	parts := strings.Split(value, ",")
	addresses := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address, err := parseRequestAddress(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", header, err)
		}
		addresses = append(addresses, address)
	}
	return addresses, nil
}

func parseRequestAddress(value string) (netip.Addr, error) {
	value = strings.TrimSpace(value)
	if addressPort, err := netip.ParseAddrPort(value); err == nil {
		return addressPort.Addr().Unmap(), nil
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("invalid address %q", value)
	}
	return address.Unmap(), nil
}
