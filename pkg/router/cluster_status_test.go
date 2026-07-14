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
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// computeClusterStatus is the workhorse for /api/v1/cluster/status. Tests
// use pickExecutorFixture (defined in exec_dispatch_test.go) to build a
// Server with a fake k8s client so we can plant Nodes + Pods and assert
// the bucket counts are right.

func TestComputeClusterStatus_CountsReadyVsTotalNodes(t *testing.T) {
	readyNode := nodeWithCondition("node-ready", corev1.ConditionTrue)
	notReadyNode := nodeWithCondition("node-notready", corev1.ConditionFalse)
	noConditionNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-blank"}}

	s := pickExecutorFixture(t, readyNode, notReadyNode, noConditionNode)
	status, err := s.computeClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("computeClusterStatus: %v", err)
	}
	if status.Nodes != 3 {
		t.Errorf("Nodes = %d, want 3", status.Nodes)
	}
	if status.NodesReady != 1 {
		t.Errorf("NodesReady = %d, want 1", status.NodesReady)
	}
}

func TestComputeClusterStatus_BucketsPodsByComponent(t *testing.T) {
	// Three sandboxes Running, one headroom Running, one CAS Running, one
	// random Running, plus one Pending that should be ignored entirely.
	objs := []client.Object{
		nodeWithCondition("n", corev1.ConditionTrue),
		runningPodWith("sb-1", "sandbox"),
		runningPodWith("sb-2", "sandbox"),
		runningPodWith("sb-3", "sandbox"),
		runningPodWith("hr-1", "headroom"),
		runningPodWith("ca-1", "cluster-autoscaler"),
		runningPodWith("misc", ""), // counts toward Pods, no bucket
	}
	pending := runningPodWith("sb-pending", "sandbox")
	pending.Status.Phase = corev1.PodPending
	objs = append(objs, pending)

	s := pickExecutorFixture(t, objs...)
	status, err := s.computeClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("computeClusterStatus: %v", err)
	}

	if status.Pods != 6 {
		t.Errorf("Pods = %d, want 6 (Pending sb-pending should be excluded)", status.Pods)
	}
	if status.SandboxPods != 3 {
		t.Errorf("SandboxPods = %d, want 3", status.SandboxPods)
	}
	if status.HeadroomReady != 1 {
		t.Errorf("HeadroomReady = %d, want 1", status.HeadroomReady)
	}
	if !status.AutoscalerEnabled {
		t.Error("AutoscalerEnabled = false, want true (cluster-autoscaler pod is Running)")
	}
}

func TestComputeClusterStatus_AutoscalerOffWhenNoPod(t *testing.T) {
	s := pickExecutorFixture(t, runningPodWith("sb-1", "sandbox"))
	status, err := s.computeClusterStatus(context.Background())
	if err != nil {
		t.Fatalf("computeClusterStatus: %v", err)
	}
	if status.AutoscalerEnabled {
		t.Error("AutoscalerEnabled = true, want false (no CAS pod planted)")
	}
}

// --- test helpers ---

func nodeWithCondition(name string, ready corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: ready},
			},
		},
	}
}

func runningPodWith(name, component string) *corev1.Pod {
	labels := map[string]string{}
	if component != "" {
		labels["app.kubernetes.io/component"] = component
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "agenttier",
			Labels:    labels,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}
