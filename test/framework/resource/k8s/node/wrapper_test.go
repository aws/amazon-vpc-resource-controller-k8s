// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package node

import (
	"testing"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testResource = "vpc.amazonaws.com/pod-eni"

func node(name string, ready bool, deleting bool, capacity string) v1.Node {
	n := v1.Node{}
	n.Name = name
	if deleting {
		now := metav1.NewTime(time.Now())
		n.DeletionTimestamp = &now
	}
	status := v1.ConditionFalse
	if ready {
		status = v1.ConditionTrue
	}
	n.Status.Conditions = []v1.NodeCondition{{Type: v1.NodeReady, Status: status}}
	if capacity != "" {
		n.Status.Allocatable = v1.ResourceList{v1.ResourceName(testResource): resource.MustParse(capacity)}
	}
	return n
}

func TestReadyNodesWithResource(t *testing.T) {
	nodes := &v1.NodeList{Items: []v1.Node{
		node("ready", true, false, "18"),          // kept
		node("not-ready", false, false, "18"),     // excluded: NotReady
		node("deleting", true, true, "18"),        // excluded: being deleted
		node("zero-capacity", true, false, "0"),   // excluded: no positive capacity
		node("missing-resource", true, false, ""), // excluded: resource absent
	}}

	got := readyNodesWithResource(nodes, testResource)
	if len(got.Items) != 1 {
		t.Fatalf("expected exactly 1 ready node, got %d: %v", len(got.Items), nodeNames(got))
	}
	if got.Items[0].Name != "ready" {
		t.Fatalf("expected node %q, got %q", "ready", got.Items[0].Name)
	}
}

func TestReadyNodesWithResource_PartialFleet(t *testing.T) {
	// Only one replacement is ready; helper must surface just that subset.
	nodes := &v1.NodeList{Items: []v1.Node{
		node("replacement-1", true, false, "18"),
		node("replacement-2", false, false, ""),
	}}
	if got := readyNodesWithResource(nodes, testResource); len(got.Items) != 1 {
		t.Fatalf("expected 1 ready node during partial registration, got %d", len(got.Items))
	}
}

func TestIsNodeReady(t *testing.T) {
	cases := map[string]struct {
		node v1.Node
		want bool
	}{
		"ready":         {node("n", true, false, "1"), true},
		"not-ready":     {node("n", false, false, "1"), false},
		"no-conditions": {v1.Node{}, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := isNodeReady(&tc.node); got != tc.want {
				t.Fatalf("isNodeReady=%v, want %v", got, tc.want)
			}
		})
	}
}

func nodeNames(list *v1.NodeList) []string {
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	return names
}
