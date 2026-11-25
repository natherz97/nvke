// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"strings"
)

const (
	controlPlaneVIPRequiredMessage = "an API endpoint is required to increase desired control-plane capacity above one; " +
		"recreate the cluster with controlPlaneConfig.loadBalancer.kubeVIP or controlPlaneConfig.loadBalancer.external configured"
	controlPlaneVIPUpgradeMessage = "an API endpoint is required to upgrade the control plane; " +
		"recreate the cluster with controlPlaneConfig.loadBalancer.kubeVIP or controlPlaneConfig.loadBalancer.external configured"
	controlPlaneVIPRolloutMessage = "an API endpoint is required to replace control-plane nodes; " +
		"recreate the cluster with controlPlaneConfig.loadBalancer.kubeVIP or controlPlaneConfig.loadBalancer.external configured " +
		"before changing rollout-triggering control-plane configuration"
)

// validateControlPlaneCapacityChange lets existing no-VIP clusters keep or reduce capacity,
// but rejects growth beyond one control-plane node unless the dev override is enabled.
func (s *clusterService) validateControlPlaneCapacityChange(controlPlaneVIP string, current, desired int) error {
	if !s.allowControlPlaneWithoutVIP && strings.TrimSpace(controlPlaneVIP) == "" && desired > 1 && desired > current {
		return newBadRequestErrorf("%s", controlPlaneVIPRequiredMessage)
	}
	return nil
}

func (s *clusterService) validateControlPlaneUpgrade(controlPlaneVIP string) error {
	if !s.allowControlPlaneWithoutVIP && strings.TrimSpace(controlPlaneVIP) == "" {
		return newBadRequestErrorf("%s", controlPlaneVIPUpgradeMessage)
	}
	return nil
}

func (s *clusterService) validateControlPlaneRollout(controlPlaneVIP string, rolloutRequired bool) error {
	if !s.allowControlPlaneWithoutVIP && strings.TrimSpace(controlPlaneVIP) == "" && rolloutRequired {
		return newBadRequestErrorf("%s", controlPlaneVIPRolloutMessage)
	}
	return nil
}
