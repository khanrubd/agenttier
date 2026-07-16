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

package portforward

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// Proxy returns an http.Handler that reverse-proxies requests to the
// cluster-internal Service for a given sandbox + port. Authentication and
// authorization are expected to have run in upstream middleware.
//
// The incoming request path is expected to look like
// `/api/v1/sandboxes/{id}/preview/{port}/<upstream-path>`. The prefix up to
// and including `/preview/{port}` is stripped before forwarding so the
// upstream sees `<upstream-path>`.
func (m *Manager) Proxy(sandboxName, namespace string, port int32, stripPrefix string) http.Handler {
	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s.svc.cluster.local:%d", serviceName(sandboxName, port), namespace, port),
	}
	rp := httputil.NewSingleHostReverseProxy(target)

	originalDirector := rp.Director
	rp.Director = func(req *http.Request) {
		originalDirector(req)
		// Strip our Router-specific prefix before upstream sees the request.
		if stripPrefix != "" && strings.HasPrefix(req.URL.Path, stripPrefix) {
			req.URL.Path = "/" + strings.TrimPrefix(strings.TrimPrefix(req.URL.Path, stripPrefix), "/")
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
		}
		// Preserve the Host header so upstreams that virtual-host on Host
		// still see a stable value.
		req.Host = target.Host
	}

	rp.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "port forward upstream unreachable: "+err.Error(), http.StatusBadGateway)
	}

	return rp
}
