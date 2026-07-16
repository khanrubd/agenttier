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
	"fmt"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

func TestResolveTimeouts_SandboxValueOnly(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{
			Timeout:     &metav1.Duration{Duration: 8 * time.Hour},
			IdleTimeout: &metav1.Duration{Duration: 1 * time.Hour},
		},
	}

	config := ResolveTimeouts(sandbox, 0, 0, true)

	if config.MaxRuntime != 8*time.Hour {
		t.Errorf("expected MaxRuntime=8h, got %v", config.MaxRuntime)
	}
	if config.IdleTimeout != 1*time.Hour {
		t.Errorf("expected IdleTimeout=1h, got %v", config.IdleTimeout)
	}
}

func TestResolveTimeouts_GovernanceCaps(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{
			Timeout:     &metav1.Duration{Duration: 24 * time.Hour},
			IdleTimeout: &metav1.Duration{Duration: 8 * time.Hour},
		},
	}

	// Governance limits are lower than sandbox requests
	config := ResolveTimeouts(sandbox, 12*time.Hour, 4*time.Hour, false)

	if config.MaxRuntime != 12*time.Hour {
		t.Errorf("expected MaxRuntime capped at 12h, got %v", config.MaxRuntime)
	}
	if config.IdleTimeout != 4*time.Hour {
		t.Errorf("expected IdleTimeout capped at 4h, got %v", config.IdleTimeout)
	}
}

func TestResolveTimeouts_InfiniteBlocked(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{
			// nil timeout = infinite
		},
	}

	// Governance does NOT allow infinite, has a max of 24h
	config := ResolveTimeouts(sandbox, 24*time.Hour, 4*time.Hour, false)

	if config.MaxRuntime != 24*time.Hour {
		t.Errorf("expected MaxRuntime=24h (governance cap), got %v", config.MaxRuntime)
	}
	if config.IdleTimeout != 4*time.Hour {
		t.Errorf("expected IdleTimeout=4h (governance cap), got %v", config.IdleTimeout)
	}
}

func TestResolveTimeouts_InfiniteAllowed(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{
			// nil timeout = infinite
		},
	}

	// Governance allows infinite
	config := ResolveTimeouts(sandbox, 0, 0, true)

	if config.MaxRuntime != 0 {
		t.Errorf("expected MaxRuntime=0 (infinite), got %v", config.MaxRuntime)
	}
	if config.IdleTimeout != 0 {
		t.Errorf("expected IdleTimeout=0 (infinite), got %v", config.IdleTimeout)
	}
}

func TestResolveTimeouts_SandboxLowerThanGovernance(t *testing.T) {
	sandbox := &agenttierv1alpha1.Sandbox{
		Spec: agenttierv1alpha1.SandboxSpec{
			Timeout:     &metav1.Duration{Duration: 2 * time.Hour},
			IdleTimeout: &metav1.Duration{Duration: 30 * time.Minute},
		},
	}

	// Governance max is higher — sandbox value should be used
	config := ResolveTimeouts(sandbox, 24*time.Hour, 4*time.Hour, true)

	if config.MaxRuntime != 2*time.Hour {
		t.Errorf("expected MaxRuntime=2h (sandbox value), got %v", config.MaxRuntime)
	}
	if config.IdleTimeout != 30*time.Minute {
		t.Errorf("expected IdleTimeout=30m (sandbox value), got %v", config.IdleTimeout)
	}
}

func TestIsIdle(t *testing.T) {
	now := metav1.Now()
	past := metav1.NewTime(time.Now().Add(-2 * time.Hour))

	tests := []struct {
		name         string
		lastActivity *metav1.Time
		timeout      time.Duration
		expected     bool
	}{
		{"infinite timeout", &past, 0, false},
		{"nil activity", nil, time.Hour, false},
		{"not idle yet", &now, time.Hour, false},
		{"idle exceeded", &past, time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsIdle(tt.lastActivity, tt.timeout)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestIsExpired(t *testing.T) {
	now := metav1.Now()
	past := metav1.NewTime(time.Now().Add(-10 * time.Hour))

	tests := []struct {
		name      string
		startedAt *metav1.Time
		timeout   time.Duration
		expected  bool
	}{
		{"infinite timeout", &past, 0, false},
		{"nil startedAt", nil, time.Hour, false},
		{"not expired", &now, 24 * time.Hour, false},
		{"expired", &past, 8 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsExpired(tt.startedAt, tt.timeout)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestTimeUntilIdle(t *testing.T) {
	now := metav1.Now()
	tenMinAgo := metav1.NewTime(time.Now().Add(-10 * time.Minute))

	tests := []struct {
		name         string
		lastActivity *metav1.Time
		timeout      time.Duration
		wantZero     bool
	}{
		{"infinite timeout returns zero", &now, 0, true},
		{"nil activity returns zero", nil, time.Hour, true},
		{"remaining time positive", &now, time.Hour, false},
		{"already idle returns zero", &tenMinAgo, 5 * time.Minute, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TimeUntilIdle(tt.lastActivity, tt.timeout)
			if tt.wantZero && got != 0 {
				t.Errorf("TimeUntilIdle() = %v, want 0", got)
			}
			if !tt.wantZero && got <= 0 {
				t.Errorf("TimeUntilIdle() = %v, want positive remaining duration", got)
			}
		})
	}
}

func TestTimeUntilExpiry(t *testing.T) {
	now := metav1.Now()
	tenHoursAgo := metav1.NewTime(time.Now().Add(-10 * time.Hour))

	tests := []struct {
		name      string
		startedAt *metav1.Time
		timeout   time.Duration
		wantZero  bool
	}{
		{"infinite timeout returns zero", &now, 0, true},
		{"nil startedAt returns zero", nil, time.Hour, true},
		{"remaining time positive", &now, 24 * time.Hour, false},
		{"already expired returns zero", &tenHoursAgo, 8 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TimeUntilExpiry(tt.startedAt, tt.timeout)
			if tt.wantZero && got != 0 {
				t.Errorf("TimeUntilExpiry() = %v, want 0", got)
			}
			if !tt.wantZero && got <= 0 {
				t.Errorf("TimeUntilExpiry() = %v, want positive remaining duration", got)
			}
		})
	}
}

func TestNextRequeueDelay_DefaultWhenNoTimeouts(t *testing.T) {
	config := &TimeoutConfig{IdleTimeout: 0, MaxRuntime: 0}
	got := NextRequeueDelay(config, nil, nil)
	want := DefaultRequeueDelay + time.Second
	if got != want {
		t.Errorf("NextRequeueDelay() = %v, want %v (default + buffer) when no timeouts configured", got, want)
	}
}

func TestNextRequeueDelay_UsesEarliestIdleTimeout(t *testing.T) {
	now := metav1.Now()
	config := &TimeoutConfig{IdleTimeout: 5 * time.Second, MaxRuntime: 0}

	got := NextRequeueDelay(config, &now, nil)
	// idleRemaining ~5s < DefaultRequeueDelay(30s), so minDelay should track
	// the idle timeout, plus the 1s buffer — always < DefaultRequeueDelay.
	if got >= DefaultRequeueDelay {
		t.Errorf("NextRequeueDelay() = %v, want less than DefaultRequeueDelay when idle timeout is imminent", got)
	}
}

func TestNextRequeueDelay_UsesEarliestMaxRuntime(t *testing.T) {
	now := metav1.Now()
	config := &TimeoutConfig{IdleTimeout: 0, MaxRuntime: 3 * time.Second}

	got := NextRequeueDelay(config, nil, &now)
	if got >= DefaultRequeueDelay {
		t.Errorf("NextRequeueDelay() = %v, want less than DefaultRequeueDelay when max runtime is imminent", got)
	}
}

func TestCalculateBackoffDelay(t *testing.T) {
	tests := []struct {
		restartCount int
		expected     time.Duration
	}{
		{0, 10 * time.Second},
		{1, 20 * time.Second},
		{2, 40 * time.Second},
		{3, 80 * time.Second},
		{4, 160 * time.Second},
		{5, 160 * time.Second},  // Capped
		{10, 160 * time.Second}, // Capped
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("restart_%d", tt.restartCount), func(t *testing.T) {
			result := calculateBackoffDelay(tt.restartCount)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
