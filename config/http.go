package config

import (
	"net"
	"strconv"
)

const (
	defaultHTTPHost = "127.0.0.1"
	defaultHTTPPort = uint16(8080)
)

// HTTP configures the application's HTTP listener.
type HTTP struct {
	Host string `json:"host" env:"VIAL_HTTP_HOST"`
	Port uint16 `json:"port" env:"VIAL_HTTP_PORT"`
}

// Address returns the net.Listen address. Empty host and zero port use safe
// development defaults.
func (configuration HTTP) Address() string {
	host := configuration.Host
	if host == "" {
		host = defaultHTTPHost
	}
	port := configuration.Port
	if port == 0 {
		port = defaultHTTPPort
	}
	return net.JoinHostPort(host, strconv.FormatUint(uint64(port), 10))
}
