package vial

import (
	"net/http"
	"net/url"
)

// Middleware may rewrite requests. Reuse only a lookup for the same routing
// inputs; ServeMux still performs dispatch to populate its path parameters.
type muxMatch struct {
	handler http.Handler
	pattern string
	method  string
	host    string
	url     url.URL
}

func (match *muxMatch) resolve(mux *http.ServeMux, request *http.Request) (http.Handler, string) {
	if match.handler == nil || match.method != request.Method || match.host != request.Host || match.url != *request.URL {
		match.handler, match.pattern = mux.Handler(request)
		match.method, match.host, match.url = request.Method, request.Host, *request.URL
	}
	return match.handler, match.pattern
}
