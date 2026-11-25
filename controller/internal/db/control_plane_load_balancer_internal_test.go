// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCloneLoadBalancer(t *testing.T) {
	assert.Nil(t, cloneLoadBalancer(nil))

	original := &LoadBalancer{
		External:       &ExternalLoadBalancer{Host: "10.0.0.10"},
		KubeVIP:        &KubeVIPLoadBalancer{VIP: "10.0.0.11"},
		FirstMachineIP: &FirstMachineIPLoadBalancer{},
	}
	clone := cloneLoadBalancer(original)

	assert.Equal(t, original, clone)
	require.NotSame(t, original, clone)
	require.NotSame(t, original.External, clone.External)
	require.NotSame(t, original.KubeVIP, clone.KubeVIP)
	require.NotNil(t, clone.FirstMachineIP, "a set firstMachineIP stays set in the copy")

	clone.External.Host = "changed"
	clone.KubeVIP.VIP = "changed"
	assert.Equal(t, "10.0.0.10", original.External.Host, "mutating the copy must not change the original")
	assert.Equal(t, "10.0.0.11", original.KubeVIP.VIP, "mutating the copy must not change the original")
}

func TestCloneLoadBalancer_OnlySetOptionsAreCopied(t *testing.T) {
	clone := cloneLoadBalancer(&LoadBalancer{KubeVIP: &KubeVIPLoadBalancer{VIP: "10.0.0.11"}})

	require.NotNil(t, clone.KubeVIP)
	assert.Nil(t, clone.External)
	assert.Nil(t, clone.FirstMachineIP)

	empty := cloneLoadBalancer(&LoadBalancer{})
	require.NotNil(t, empty, "an empty but set load balancer stays set")
	assert.Nil(t, empty.External)
	assert.Nil(t, empty.KubeVIP)
	assert.Nil(t, empty.FirstMachineIP)
}

// A sync from the control-plane node pools rebuilds the config, and node pools do
// not carry a load balancer, so the sync has to keep the cluster's own.
func TestPreserveClusterControlPlaneOverrides_KeepsLoadBalancer(t *testing.T) {
	for name, loadBalancer := range map[string]*LoadBalancer{
		"kubeVIP":        {KubeVIP: &KubeVIPLoadBalancer{VIP: "10.0.0.11"}},
		"external":       {External: &ExternalLoadBalancer{Host: "10.0.0.10"}},
		"firstMachineIP": {FirstMachineIP: &FirstMachineIPLoadBalancer{}},
		"none":           nil,
	} {
		t.Run(name, func(t *testing.T) {
			existing := Cluster{ControlPlaneConfig: ControlPlaneConfig{
				ControlPlaneConfigSpec: ControlPlaneConfigSpec{LoadBalancer: loadBalancer},
			}}
			merged := &ControlPlaneConfig{}

			preserveClusterControlPlaneOverrides(existing, merged)

			assert.Equal(t, loadBalancer, merged.LoadBalancer)
		})
	}
}

func TestNormalizeConfigSpec_DoesNotAliasLoadBalancer(t *testing.T) {
	spec := ControlPlaneConfigSpec{
		LoadBalancer: &LoadBalancer{KubeVIP: &KubeVIPLoadBalancer{VIP: "10.0.0.11"}},
	}
	normalized := normalizeConfigSpec(spec)

	require.NotNil(t, normalized.LoadBalancer)
	normalized.LoadBalancer.KubeVIP.VIP = "changed"
	assert.Equal(t, "10.0.0.11", spec.LoadBalancer.KubeVIP.VIP, "normalizing must not share the load balancer with the input")
}
