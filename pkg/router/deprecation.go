/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package router

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
)

// API versioning + deprecation. The REST surface is `/api/v1`. When a breaking
// change forces `/api/v2`, the superseded v1 endpoints are flagged here and the
// middleware stamps the standard signalling headers so clients can react
// generically:
//
//   - Deprecation: true                       (draft Deprecation header)
//   - Sunset: <HTTP-date>                      (RFC 9745 — when v1 goes away)
//   - Link: <successor>; rel="successor-version"
//
// The SDK and CLI watch for the Deprecation header and warn the user once.
// See docs/docs/api-versioning.md for the policy. The registry below is EMPTY
// today — nothing is deprecated yet, so the middleware is a no-op until the
// first /api/v2 endpoint ships.

// deprecatedEndpoint describes a deprecated route.
type deprecatedEndpoint struct {
	// Sunset is when the endpoint will be removed. Zero = no firm date yet.
	Sunset time.Time
	// Successor is the replacement path (e.g. "/api/v2/sandboxes"), surfaced
	// via a Link rel="successor-version" header. Optional.
	Successor string
}

// deprecatedRoutes maps a gorilla/mux path template (e.g.
// "/api/v1/sandboxes/{id}") to its deprecation metadata. Add an entry the day
// an endpoint is deprecated; until then this is empty and the middleware adds
// no overhead beyond a map lookup.
var deprecatedRoutes = map[string]deprecatedEndpoint{}

// deprecationMiddleware stamps Deprecation/Sunset/Link headers on responses for
// any route registered in deprecatedRoutes. A no-op when the registry is empty.
func (s *Server) deprecationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(deprecatedRoutes) > 0 {
			if tmpl := routePathTemplate(r); tmpl != "" {
				if dep, ok := deprecatedRoutes[tmpl]; ok {
					stampDeprecation(w.Header(), dep)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// stampDeprecation writes the deprecation signalling headers.
func stampDeprecation(h http.Header, dep deprecatedEndpoint) {
	h.Set("Deprecation", "true")
	if !dep.Sunset.IsZero() {
		h.Set("Sunset", dep.Sunset.UTC().Format(http.TimeFormat))
	}
	if dep.Successor != "" {
		h.Set("Link", fmt.Sprintf("<%s>; rel=\"successor-version\"", dep.Successor))
	}
}

// routePathTemplate returns the matched mux route's path template, or "" if it
// can't be determined (the middleware runs after routing via router.Use, so
// the current route is normally available).
func routePathTemplate(r *http.Request) string {
	if route := mux.CurrentRoute(r); route != nil {
		if tmpl, err := route.GetPathTemplate(); err == nil {
			return tmpl
		}
	}
	return ""
}
