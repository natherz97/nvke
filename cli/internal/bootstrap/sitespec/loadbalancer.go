// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sitespec

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

const (
	pathLoadBalancer             = "cluster.loadBalancer"
	pathLoadBalancerExternalHost = "cluster.loadBalancer.external.host"
	pathLoadBalancerKubeVIPVIP   = "cluster.loadBalancer.kubeVIP.vip"

	pathLoadBalancerFirstMachineIP = "cluster.loadBalancer.firstMachineIP"
)

// applyEmptyLoadBalancerOptions sets the loadBalancer options that carry no
// fields. Viper drops an empty mapping when it builds the settings the decoder
// reads, so firstMachineIP: {} would otherwise load as if it were unset. A
// firstMachineIP with no value at all, blank or null, is refused: the option
// has no fields, so it has to be written as an empty mapping.
func applyEmptyLoadBalancerOptions(v *viper.Viper, c *Cluster) error {
	// Viper lowercases keys, so the option is looked up as firstmachineip.
	if lb, ok := v.Get(pathLoadBalancer).(map[string]any); ok {
		if value, present := lb[strings.ToLower("firstMachineIP")]; present && value == nil {
			return fmt.Errorf("%s has no value: write it as an empty mapping, firstMachineIP: {}", pathLoadBalancerFirstMachineIP)
		}
	}
	if !v.InConfig(pathLoadBalancerFirstMachineIP) {
		return nil
	}
	if c.LoadBalancer == nil {
		c.LoadBalancer = &LoadBalancer{}
	}
	if c.LoadBalancer.FirstMachineIP == nil {
		c.LoadBalancer.FirstMachineIP = &FirstMachineIPLoadBalancer{}
	}
	return nil
}

// LoadBalancer selects how the site cluster's Kubernetes API endpoint is
// provided. Exactly one field is set. It replaces the deprecated
// Cluster.ControlPlaneVIP and cannot be combined with it.
type LoadBalancer struct {
	// External is an endpoint the operator provides. NVKE does not configure
	// kube-vip.
	External *ExternalLoadBalancer `yaml:"external,omitempty"`
	// KubeVIP is a virtual IP that NVKE configures kube-vip to advertise.
	KubeVIP *KubeVIPLoadBalancer `yaml:"kubeVIP,omitempty"`
	// FirstMachineIP uses the first control-plane node's IP as the endpoint.
	// Development only.
	FirstMachineIP *FirstMachineIPLoadBalancer `yaml:"firstMachineIP,omitempty"`
}

// ExternalLoadBalancer is an operator-provided API endpoint.
type ExternalLoadBalancer struct {
	// Host is the IPv4 address of the endpoint.
	Host string `yaml:"host"`
}

// KubeVIPLoadBalancer is a virtual IP that NVKE configures kube-vip for.
type KubeVIPLoadBalancer struct {
	// VIP is the IPv4 address of the virtual IP.
	VIP string `yaml:"vip"`
}

// FirstMachineIPLoadBalancer selects the first control-plane node's IP. It has
// no fields.
type FirstMachineIPLoadBalancer struct{}

// ControlPlaneEndpoint returns the API endpoint address the cluster names: the
// loadBalancer's external host or kubeVIP address, or the deprecated
// controlPlaneVIP when no loadBalancer is set. It is empty for firstMachineIP
// and when nothing is set.
func (c Cluster) ControlPlaneEndpoint() string {
	if lb := c.LoadBalancer; lb != nil {
		switch {
		case lb.External != nil:
			return lb.External.Host
		case lb.KubeVIP != nil:
			return lb.KubeVIP.VIP
		default:
			return ""
		}
	}
	return c.ControlPlaneVIP
}

// loadBalancerExternalHost returns cluster.loadBalancer.external.host, or "" when unset.
func loadBalancerExternalHost(c Cluster) string {
	if c.LoadBalancer == nil || c.LoadBalancer.External == nil {
		return ""
	}
	return c.LoadBalancer.External.Host
}

// loadBalancerKubeVIP returns cluster.loadBalancer.kubeVIP.vip, or "" when unset.
func loadBalancerKubeVIP(c Cluster) string {
	if c.LoadBalancer == nil || c.LoadBalancer.KubeVIP == nil {
		return ""
	}
	return c.LoadBalancer.KubeVIP.VIP
}

// controlPlaneEndpointPath returns the site spec path that holds the endpoint
// address, for error messages.
func (c Cluster) controlPlaneEndpointPath() string {
	if lb := c.LoadBalancer; lb != nil {
		switch {
		case lb.External != nil:
			return pathLoadBalancerExternalHost
		case lb.KubeVIP != nil:
			return pathLoadBalancerKubeVIPVIP
		}
	}
	return pathControlPlaneVIP
}

// checkLoadBalancer reports a cluster.loadBalancer that does not choose exactly
// one option, one combined with the deprecated cluster.controlPlaneVIP, and an
// empty address. It returns false when the endpoint cannot be checked further.
func (v *validator) checkLoadBalancer(c Cluster) bool {
	lb := c.LoadBalancer
	if lb == nil {
		return true
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
		v.addf("%s must set exactly one of external, kubeVIP or firstMachineIP", pathLoadBalancer)
		return false
	}

	if c.ControlPlaneVIP != "" {
		v.addf("%s is deprecated and cannot be combined with %s: use %s or %s instead",
			pathControlPlaneVIP, pathLoadBalancer, pathLoadBalancerKubeVIPVIP, pathLoadBalancerExternalHost)
		return false
	}

	if (lb.External != nil || lb.KubeVIP != nil) && c.ControlPlaneEndpoint() == "" {
		v.addf("%s must not be empty", c.controlPlaneEndpointPath())
		return false
	}
	return true
}
