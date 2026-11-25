// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"github.com/dsx-ai-factory/nvke/controller/internal/db"
)

// KubeVIPControlPlaneConfig returns a control-plane config whose stored load
// balancer is kube-vip with the given VIP. An empty vip returns a config with no
// load balancer, which is a cluster with no API endpoint. A test uses it to say
// "this cluster has a VIP", because the controller reads the endpoint from the
// stored load balancer and never from the deprecated control_plane_vip column.
func KubeVIPControlPlaneConfig(vip string) db.ControlPlaneConfig {
	return db.ControlPlaneConfig{ControlPlaneConfigSpec: WithKubeVIP(db.ControlPlaneConfigSpec{}, vip)}
}

// WithKubeVIP returns spec with kube-vip and the given VIP as its stored load
// balancer, for a test that already builds its own spec. An empty vip returns spec
// unchanged.
func WithKubeVIP(spec db.ControlPlaneConfigSpec, vip string) db.ControlPlaneConfigSpec {
	if vip != "" {
		spec.LoadBalancer = &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: vip}}
	}
	return spec
}
