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

package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// TimeoutConfig holds the resolved timeout configuration for a sandbox.
type TimeoutConfig struct {
	// IdleTimeout is the effective idle timeout (min of sandbox + governance).
	// Zero means infinite (no idle timeout).
	IdleTimeout time.Duration

	// MaxRuntime is the effective max runtime (min of sandbox + governance).
	// Zero means infinite (no max runtime).
	MaxRuntime time.Duration

	// GracePeriod is the warning period before auto-stop.
	GracePeriod time.Duration
}

// DefaultGracePeriod is the default warning period before auto-stop.
const DefaultGracePeriod = 5 * time.Minute

// ResolveTimeouts computes the effective timeouts for a sandbox,
// taking into account the sandbox spec, template defaults, and governance limits.
//
// The effective timeout is always: min(sandbox_setting, governance_maximum)
// A value of 0 means "infinite" — but only if governance allows it.
func ResolveTimeouts(sandbox *agenttierv1alpha1.Sandbox, governanceMaxTimeout, governanceMaxIdle time.Duration, governanceAllowsInfinite bool) *TimeoutConfig {
	config := &TimeoutConfig{
		GracePeriod: DefaultGracePeriod,
	}

	// Resolve idle timeout
	sandboxIdle := durationFromSpec(sandbox.Spec.IdleTimeout)
	config.IdleTimeout = resolveEffectiveTimeout(sandboxIdle, governanceMaxIdle, governanceAllowsInfinite)

	// Resolve max runtime
	sandboxTimeout := durationFromSpec(sandbox.Spec.Timeout)
	config.MaxRuntime = resolveEffectiveTimeout(sandboxTimeout, governanceMaxTimeout, governanceAllowsInfinite)

	return config
}

// resolveEffectiveTimeout computes the effective timeout given sandbox and governance values.
// Rules:
//   - If sandbox requests 0 (infinite) and governance allows infinite → 0 (infinite)
//   - If sandbox requests 0 (infinite) and governance has a max → governance max
//   - If sandbox requests a value → min(sandbox, governance max) [if governance has a max]
//   - If governance has no max (0) → sandbox value as-is
func resolveEffectiveTimeout(sandboxValue, governanceMax time.Duration, allowsInfinite bool) time.Duration {
	// Sandbox requests infinite
	if sandboxValue == 0 {
		if allowsInfinite || governanceMax == 0 {
			return 0 // Truly infinite
		}
		return governanceMax // Capped by governance
	}

	// Sandbox requests a specific value
	if governanceMax == 0 {
		return sandboxValue // No governance limit
	}

	// Both have values — take the minimum
	if sandboxValue < governanceMax {
		return sandboxValue
	}
	return governanceMax
}

// durationFromSpec extracts a time.Duration from a metav1.Duration pointer.
// Returns 0 if nil (meaning infinite/unset).
func durationFromSpec(d *metav1.Duration) time.Duration {
	if d == nil {
		return 0
	}
	return d.Duration
}

// IsIdle returns true if the sandbox has been idle longer than the given timeout.
func IsIdle(lastActivity *metav1.Time, timeout time.Duration) bool {
	if timeout == 0 {
		return false // Infinite timeout — never idle
	}
	if lastActivity == nil {
		return false // No activity recorded — can't determine idle
	}
	return time.Since(lastActivity.Time) >= timeout
}

// IsExpired returns true if the sandbox has been running longer than the given timeout.
func IsExpired(startedAt *metav1.Time, timeout time.Duration) bool {
	if timeout == 0 {
		return false // Infinite timeout — never expires
	}
	if startedAt == nil {
		return false // Not started — can't determine expiry
	}
	return time.Since(startedAt.Time) >= timeout
}

// TimeUntilIdle returns the duration until the sandbox becomes idle.
// Returns 0 if already idle or timeout is infinite.
func TimeUntilIdle(lastActivity *metav1.Time, timeout time.Duration) time.Duration {
	if timeout == 0 || lastActivity == nil {
		return 0
	}
	remaining := timeout - time.Since(lastActivity.Time)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// TimeUntilExpiry returns the duration until the sandbox expires.
// Returns 0 if already expired or timeout is infinite.
func TimeUntilExpiry(startedAt *metav1.Time, timeout time.Duration) time.Duration {
	if timeout == 0 || startedAt == nil {
		return 0
	}
	remaining := timeout - time.Since(startedAt.Time)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// NextRequeueDelay calculates the optimal RequeueAfter duration
// based on the earliest upcoming timeout event.
func NextRequeueDelay(config *TimeoutConfig, lastActivity, startedAt *metav1.Time) time.Duration {
	minDelay := DefaultRequeueDelay

	// Check idle timeout
	if config.IdleTimeout > 0 {
		idleRemaining := TimeUntilIdle(lastActivity, config.IdleTimeout)
		if idleRemaining > 0 && idleRemaining < minDelay {
			minDelay = idleRemaining
		}
	}

	// Check max runtime
	if config.MaxRuntime > 0 {
		runtimeRemaining := TimeUntilExpiry(startedAt, config.MaxRuntime)
		if runtimeRemaining > 0 && runtimeRemaining < minDelay {
			minDelay = runtimeRemaining
		}
	}

	// Add a small buffer to avoid tight loops
	return minDelay + time.Second
}
