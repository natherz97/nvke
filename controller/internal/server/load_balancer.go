// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"fmt"
	"strings"

	"github.com/dsx-ai-factory/nvke/controller/api"
	"github.com/dsx-ai-factory/nvke/controller/internal/db"
)

// createLoadBalancer returns the load balancer a create request sets, or nil.
func createLoadBalancer(req *api.CreateClusterRequest) *api.LoadBalancer {
	if req == nil || req.ControlPlaneConfig == nil {
		return nil
	}
	return req.ControlPlaneConfig.LoadBalancer
}

// effectiveLoadBalancer returns how a create request provides the API endpoint.
// A request that sets only the deprecated top-level controlPlaneVIP is treated as
// loadBalancer.kubeVIP with that address, because controlPlaneVIP has always
// meant "NVKE runs kube-vip on this VIP". That is the form it is stored in. It
// returns nil when the request names no endpoint.
//
// controlPlaneVIP may also be sent together with loadBalancer.kubeVIP or
// loadBalancer.external when it equals their address, so a cluster read back from
// the API can be sent again unchanged. It rejects a load balancer that does not
// choose exactly one option, a controlPlaneVIP that differs from the load
// balancer's address or goes with firstMachineIP, and an empty address. The checks
// that need the cluster network (IPv4, outside the pod and service CIDRs) run
// with the other network validation.
func effectiveLoadBalancer(req *api.CreateClusterRequest) (*api.LoadBalancer, error) {
	legacy := strings.TrimSpace(req.ControlPlaneVIP)
	lb := createLoadBalancer(req)
	if lb == nil {
		if legacy == "" {
			return nil, nil //nolint:nilnil // nil means the request names no endpoint, which is not an error
		}
		return &api.LoadBalancer{KubeVIP: &api.KubeVIPLoadBalancer{Vip: legacy}}, nil
	}

	selected := 0
	if lb.External != nil {
		selected++
	}
	if lb.KubeVIP != nil {
		selected++
	}
	if lb.FirstMachineIP != nil {
		selected++
	}
	if selected != 1 {
		return nil, errors.New("controlPlaneConfig.loadBalancer must set exactly one of external, kubeVIP or firstMachineIP")
	}

	out := &api.LoadBalancer{}
	address := ""
	switch {
	case lb.External != nil:
		host, err := requireAddress("controlPlaneConfig.loadBalancer.external.host", lb.External.Host)
		if err != nil {
			return nil, err
		}
		out.External = &api.ExternalLoadBalancer{Host: host}
		address = host
	case lb.KubeVIP != nil:
		vip, err := requireAddress("controlPlaneConfig.loadBalancer.kubeVIP.vip", lb.KubeVIP.Vip)
		if err != nil {
			return nil, err
		}
		out.KubeVIP = &api.KubeVIPLoadBalancer{Vip: vip}
		address = vip
	default:
		out.FirstMachineIP = &api.FirstMachineIPLoadBalancer{}
	}

	if legacy != "" {
		if out.FirstMachineIP != nil {
			return nil, errors.New("controlPlaneVIP cannot be combined with controlPlaneConfig.loadBalancer.firstMachineIP")
		}
		if legacy != address {
			return nil, fmt.Errorf(
				"controlPlaneVIP %q must equal the address in controlPlaneConfig.loadBalancer (%q), or be omitted: "+
					"controlPlaneVIP is deprecated, use controlPlaneConfig.loadBalancer instead",
				legacy, address,
			)
		}
	}
	return out, nil
}

// controlPlaneEndpoint returns the API endpoint address a create request names.
// It is empty for firstMachineIP and when the request names none.
func controlPlaneEndpoint(req *api.CreateClusterRequest) (string, error) {
	lb, err := effectiveLoadBalancer(req)
	if err != nil {
		return "", err
	}
	return dbLoadBalancerFromAPI(lb).Endpoint(), nil
}

func requireAddress(field, value string) (string, error) {
	address := strings.TrimSpace(value)
	if address == "" {
		return "", fmt.Errorf("%s must not be empty", field)
	}
	return address, nil
}

func dbLoadBalancerFromAPI(lb *api.LoadBalancer) *db.LoadBalancer {
	if lb == nil {
		return nil
	}
	out := &db.LoadBalancer{}
	if lb.External != nil {
		out.External = &db.ExternalLoadBalancer{Host: strings.TrimSpace(lb.External.Host)}
	}
	if lb.KubeVIP != nil {
		out.KubeVIP = &db.KubeVIPLoadBalancer{VIP: strings.TrimSpace(lb.KubeVIP.Vip)}
	}
	if lb.FirstMachineIP != nil {
		out.FirstMachineIP = &db.FirstMachineIPLoadBalancer{}
	}
	return out
}

func apiLoadBalancerFromDB(lb *db.LoadBalancer) *api.LoadBalancer {
	if lb == nil {
		return nil
	}
	out := &api.LoadBalancer{}
	if lb.External != nil {
		out.External = &api.ExternalLoadBalancer{Host: lb.External.Host}
	}
	if lb.KubeVIP != nil {
		out.KubeVIP = &api.KubeVIPLoadBalancer{Vip: lb.KubeVIP.VIP}
	}
	if lb.FirstMachineIP != nil {
		out.FirstMachineIP = &api.FirstMachineIPLoadBalancer{}
	}
	return out
}
