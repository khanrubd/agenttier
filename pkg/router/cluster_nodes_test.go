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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func capacityNode(name, instanceType, nodeGroup, cpu, mem string, ready corev1.ConditionStatus) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"node.kubernetes.io/instance-type": instanceType,
				"eks.amazonaws.com/nodegroup":      nodeGroup,
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
			},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready}},
		},
	}
}

func podOnNode(name, node, cpuReq, memReq string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agenttier"},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name: "c",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse(cpuReq),
						corev1.ResourceMemory: resource.MustParse(memReq),
					},
				},
			}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func TestComputeClusterNodes_AllocatableRequestsAndSaturation(t *testing.T) {
	objs := []client.Object{
		capacityNode("ng-node-1", "t3.large", "agentloft-e2e", "2", "8Gi", corev1.ConditionTrue),
		capacityNode("ng-node-2", "t3.large", "agentloft-e2e", "2", "8Gi", corev1.ConditionTrue),
		// node-1 carries 1 vCPU + 2Gi of requests across two pods.
		podOnNode("p1", "ng-node-1", "500m", "1Gi"),
		podOnNode("p2", "ng-node-1", "500m", "1Gi"),
		// A terminal pod must not count toward requests.
		func() *corev1.Pod {
			p := podOnNode("done", "ng-node-1", "4", "4Gi")
			p.Status.Phase = corev1.PodSucceeded
			return p
		}(),
	}

	s := pickExecutorFixture(t, objs...)
	out, err := s.computeClusterNodes(context.Background())
	if err != nil {
		t.Fatalf("computeClusterNodes: %v", err)
	}

	if out.Summary.Total != 2 || out.Summary.Ready != 2 {
		t.Fatalf("summary total/ready = %d/%d, want 2/2", out.Summary.Total, out.Summary.Ready)
	}
	// Total allocatable CPU = 4000m; requests = 1000m → 25% saturation.
	if out.Summary.Allocatable.CPUMillis != 4000 {
		t.Errorf("alloc cpu = %d, want 4000", out.Summary.Allocatable.CPUMillis)
	}
	if out.Summary.Requests.CPUMillis != 1000 {
		t.Errorf("req cpu = %d, want 1000 (terminal pod must be excluded)", out.Summary.Requests.CPUMillis)
	}
	if out.Summary.CPUSaturationPct != 25 {
		t.Errorf("cpu saturation = %v, want 25", out.Summary.CPUSaturationPct)
	}
	// Memory: 16Gi allocatable, 2Gi requested → 12.5%.
	if out.Summary.MemSaturationPct != 12.5 {
		t.Errorf("mem saturation = %v, want 12.5", out.Summary.MemSaturationPct)
	}

	// Per-node detail surfaces instance type + node group.
	var n1 *NodeCapacity
	for i := range out.Nodes {
		if out.Nodes[i].Name == "ng-node-1" {
			n1 = &out.Nodes[i]
		}
	}
	if n1 == nil {
		t.Fatal("ng-node-1 missing from response")
	}
	if n1.InstanceType != "t3.large" || n1.NodeGroup != "agentloft-e2e" {
		t.Errorf("node-1 instanceType/nodeGroup = %q/%q, want t3.large/agentloft-e2e", n1.InstanceType, n1.NodeGroup)
	}
	if n1.Requests.CPUMillis != 1000 {
		t.Errorf("node-1 req cpu = %d, want 1000", n1.Requests.CPUMillis)
	}
}
