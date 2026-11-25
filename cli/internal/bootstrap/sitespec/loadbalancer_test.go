// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sitespec

import (
	"fmt"
	"strings"
	"testing"
)

// loadBalancerSpecYAML renders a provisioned site spec with nodeCount
// control-plane nodes and the given cluster.loadBalancer block. The block is
// already indented to sit under "cluster:".
func loadBalancerSpecYAML(nodeCount int, block string) string {
	var b strings.Builder
	b.WriteString("clusterName: nvke-site\ncluster:\n  nodes:\n")
	for i := 1; i <= nodeCount; i++ {
		fmt.Fprintf(&b, "    - name: node%d\n      ip: 10.0.0.%d\n", i, i)
	}
	b.WriteString("  k8sVersion: \"1.32.0\"\n")
	b.WriteString("  cniName: ovn-kubernetes\n")
	b.WriteString("  podCidr: \"10.100.0.0/16\"\n")
	b.WriteString("  serviceCidr: \"10.200.0.0/16\"\n")
	b.WriteString(block)
	return b.String()
}

func externalLB(host string) *LoadBalancer {
	return &LoadBalancer{External: &ExternalLoadBalancer{Host: host}}
}

func kubeVIPLB(vip string) *LoadBalancer {
	return &LoadBalancer{KubeVIP: &KubeVIPLoadBalancer{VIP: vip}}
}

func firstMachineIPLB() *LoadBalancer {
	return &LoadBalancer{FirstMachineIP: &FirstMachineIPLoadBalancer{}}
}

func TestLoadLoadBalancer(t *testing.T) {
	tests := []struct {
		name  string
		nodes int
		block string
		check func(t *testing.T, lb *LoadBalancer)
	}{
		{
			name:  "external",
			nodes: 3,
			block: "  loadBalancer:\n    external:\n      host: \"10.0.0.100\"\n",
			check: func(t *testing.T, lb *LoadBalancer) {
				if lb.External == nil || lb.External.Host != "10.0.0.100" {
					t.Errorf("External = %+v, want host 10.0.0.100", lb.External)
				}
				if lb.KubeVIP != nil || lb.FirstMachineIP != nil {
					t.Errorf("only external should be set, got %+v", lb)
				}
			},
		},
		{
			name:  "kubeVIP",
			nodes: 3,
			block: "  loadBalancer:\n    kubeVIP:\n      vip: \"10.0.0.101\"\n",
			check: func(t *testing.T, lb *LoadBalancer) {
				if lb.KubeVIP == nil || lb.KubeVIP.VIP != "10.0.0.101" {
					t.Errorf("KubeVIP = %+v, want vip 10.0.0.101", lb.KubeVIP)
				}
				if lb.External != nil || lb.FirstMachineIP != nil {
					t.Errorf("only kubeVIP should be set, got %+v", lb)
				}
			},
		},
		{
			name:  "firstMachineIP",
			nodes: 1,
			block: "  loadBalancer:\n    firstMachineIP: {}\n",
			check: func(t *testing.T, lb *LoadBalancer) {
				if lb.FirstMachineIP == nil {
					t.Error("FirstMachineIP is nil; an empty mapping must still select the option")
				}
				if lb.External != nil || lb.KubeVIP != nil {
					t.Errorf("only firstMachineIP should be set, got %+v", lb)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv, err := Load(writeTemp(t, loadBalancerSpecYAML(tc.nodes, tc.block)))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if inv.Cluster.LoadBalancer == nil {
				t.Fatal("Cluster.LoadBalancer is nil after loading a spec that sets it")
			}
			if inv.Cluster.ControlPlaneVIP != "" {
				t.Errorf("ControlPlaneVIP = %q, want empty: nothing set the deprecated field", inv.Cluster.ControlPlaneVIP)
			}
			tc.check(t, inv.Cluster.LoadBalancer)
		})
	}
}

func TestLoadLoadBalancer_AbsentWhenNotSet(t *testing.T) {
	inv, err := Load(writeTemp(t, validYAML))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if inv.Cluster.LoadBalancer != nil {
		t.Errorf("LoadBalancer = %+v, want nil for a spec that only sets controlPlaneVIP", inv.Cluster.LoadBalancer)
	}
	if inv.Cluster.ControlPlaneVIP != "10.0.0.100" {
		t.Errorf("ControlPlaneVIP = %q, want the legacy value kept", inv.Cluster.ControlPlaneVIP)
	}
}

func TestLoadLoadBalancer_RejectsUnknownKeys(t *testing.T) {
	tests := []struct {
		name    string
		block   string
		wantKey string
	}{
		{
			name:    "unknown option",
			block:   "  loadBalancer:\n    typo: x\n",
			wantKey: "typo",
		},
		{
			name:    "unknown key under external",
			block:   "  loadBalancer:\n    external:\n      host: \"10.0.0.100\"\n      port: 6443\n",
			wantKey: "port",
		},
		{
			name:    "unknown key under kubeVIP",
			block:   "  loadBalancer:\n    kubeVIP:\n      vip: \"10.0.0.100\"\n      vipp: \"10.0.0.101\"\n",
			wantKey: "vipp",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTemp(t, loadBalancerSpecYAML(3, tc.block)))
			if err == nil {
				t.Fatal("Load accepted a loadBalancer with an unknown key; want error")
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("error %q does not name the unknown key %q", err, tc.wantKey)
			}
		})
	}
}

func TestValidate_LoadBalancer(t *testing.T) {
	tests := []struct {
		name      string
		legacyVIP string
		lb        *LoadBalancer
		nodes     int // keep the first n nodes of minimalValid; 0 keeps all three
		wantErr   []string
	}{
		// Valid shapes.
		{name: "external", lb: externalLB("10.0.0.100")},
		{name: "external public address", lb: externalLB("198.51.100.10")},
		{name: "kubeVIP", lb: kubeVIPLB("10.0.0.100")},
		{name: "kubeVIP equal to a node IP", lb: kubeVIPLB("10.0.0.1")},
		{name: "firstMachineIP with one node", lb: firstMachineIPLB(), nodes: 1},

		// Exactly one option.
		{name: "nothing selected", lb: &LoadBalancer{}, wantErr: []string{pathLoadBalancer, "exactly one"}},
		{
			name:    "external and kubeVIP",
			lb:      &LoadBalancer{External: &ExternalLoadBalancer{Host: "10.0.0.100"}, KubeVIP: &KubeVIPLoadBalancer{VIP: "10.0.0.101"}},
			wantErr: []string{pathLoadBalancer, "exactly one"},
		},
		{
			name:    "external and firstMachineIP",
			lb:      &LoadBalancer{External: &ExternalLoadBalancer{Host: "10.0.0.100"}, FirstMachineIP: &FirstMachineIPLoadBalancer{}},
			wantErr: []string{pathLoadBalancer, "exactly one"},
		},
		{
			name: "all three",
			lb: &LoadBalancer{
				External:       &ExternalLoadBalancer{Host: "10.0.0.100"},
				KubeVIP:        &KubeVIPLoadBalancer{VIP: "10.0.0.101"},
				FirstMachineIP: &FirstMachineIPLoadBalancer{},
			},
			wantErr: []string{pathLoadBalancer, "exactly one"},
		},

		// The deprecated field cannot be combined with loadBalancer.
		{
			name:      "kubeVIP with the deprecated controlPlaneVIP",
			legacyVIP: "10.0.0.100",
			lb:        kubeVIPLB("10.0.0.100"),
			wantErr:   []string{pathControlPlaneVIP, pathLoadBalancer, "deprecated"},
		},
		{
			name:      "external with the deprecated controlPlaneVIP",
			legacyVIP: "10.0.0.100",
			lb:        externalLB("10.0.0.101"),
			wantErr:   []string{pathControlPlaneVIP, pathLoadBalancer, "deprecated"},
		},
		{
			name:      "firstMachineIP with the deprecated controlPlaneVIP",
			legacyVIP: "10.0.0.100",
			lb:        firstMachineIPLB(),
			nodes:     1,
			wantErr:   []string{pathControlPlaneVIP, pathLoadBalancer, "deprecated"},
		},

		// Empty addresses name the field that holds them.
		{name: "empty external host", lb: externalLB(""), wantErr: []string{pathLoadBalancerExternalHost + " must not be empty"}},
		{name: "empty external host", lb: externalLB(""), wantErr: []string{pathLoadBalancerExternalHost + " must not be empty"}},
		{name: "empty kubeVIP vip", lb: kubeVIPLB(""), wantErr: []string{pathLoadBalancerKubeVIPVIP + " must not be empty"}},

		// The first node's IP is the endpoint, so only one node can use it.
		{
			name: "firstMachineIP with several nodes",
			lb:   firstMachineIPLB(),
			wantErr: []string{
				pathLoadBalancerFirstMachineIP, "is not allowed when the cluster has more than one control-plane node",
				pathLoadBalancerKubeVIPVIP, pathLoadBalancerExternalHost,
			},
		},
	}

	// The address checks that apply to controlPlaneVIP apply to both addresses
	// a loadBalancer can carry, and the error names the field that holds it.
	for _, holder := range []struct {
		name string
		path string
		lb   func(address string) *LoadBalancer
	}{
		{name: "external", path: pathLoadBalancerExternalHost, lb: externalLB},
		{name: "kubeVIP", path: pathLoadBalancerKubeVIPVIP, lb: kubeVIPLB},
	} {
		for _, bad := range []struct {
			name    string
			address string
			want    []string
		}{
			{"not an IP address", "not-an-ip", []string{"is not a valid IP address"}},
			{"IPv6 address", "2001:db8::1", []string{"must be an IPv4 address"}},
			{"loopback address", "127.0.0.1", []string{"routable unicast"}},
			{"unspecified address", "0.0.0.0", []string{"routable unicast"}},
			{"inside the pod CIDR", "10.100.0.10", []string{"is inside", pathPodCidr}},
			{"inside the service CIDR", "10.200.0.10", []string{"is inside", pathServiceCidr}},
		} {
			tests = append(tests, struct {
				name      string
				legacyVIP string
				lb        *LoadBalancer
				nodes     int
				wantErr   []string
			}{
				name:    holder.name + " " + bad.name,
				lb:      holder.lb(bad.address),
				wantErr: append([]string{holder.path}, bad.want...),
			})
		}
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv := minimalValid()
			inv.Cluster.ControlPlaneVIP = tc.legacyVIP
			inv.Cluster.LoadBalancer = tc.lb
			if tc.nodes > 0 {
				inv.Cluster.Nodes = inv.Cluster.Nodes[:tc.nodes]
			}
			err := inv.Validate()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Validate rejected a valid loadBalancer: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate accepted an invalid loadBalancer; want an error containing %q", tc.wantErr)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// TestValidate_LoadBalancerAddressErrorsNameTheirOwnPath checks that an
// address problem under one option does not name the other option's path or the
// deprecated field.
func TestValidate_LoadBalancerAddressErrorsNameTheirOwnPath(t *testing.T) {
	inv := minimalValid()
	inv.Cluster.ControlPlaneVIP = ""
	inv.Cluster.LoadBalancer = kubeVIPLB("not-an-ip")
	err := inv.Validate()
	if err == nil {
		t.Fatal("Validate accepted a malformed kubeVIP address")
	}
	if !strings.Contains(err.Error(), pathLoadBalancerKubeVIPVIP) {
		t.Errorf("error %q does not name %s", err, pathLoadBalancerKubeVIPVIP)
	}
	for _, other := range []string{pathLoadBalancerExternalHost, pathControlPlaneVIP} {
		if strings.Contains(err.Error(), other) {
			t.Errorf("error %q names %s, which the spec did not set", err, other)
		}
	}
}

// TestValidate_LegacyControlPlaneVIPErrorsNameTheirPath guards the path that
// the address checks report: a spec that only sets the deprecated field still
// gets errors naming cluster.controlPlaneVIP, and never cluster.loadBalancer.
func TestValidate_LegacyControlPlaneVIPErrorsNameTheirPath(t *testing.T) {
	for _, tc := range []struct{ vip, want string }{
		{"not-an-ip", "is not a valid IP address"},
		{"2001:db8::1", "must be an IPv4 address"},
		{"127.0.0.1", "routable unicast"},
		{"10.100.0.10", "is inside"},
		{"10.200.0.10", "is inside"},
	} {
		t.Run(tc.vip, func(t *testing.T) {
			inv := minimalValid()
			inv.Cluster.ControlPlaneVIP = tc.vip
			err := inv.Validate()
			if err == nil {
				t.Fatalf("Validate accepted %q", tc.vip)
			}
			if !strings.Contains(err.Error(), pathControlPlaneVIP) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name %s and say %q", err, pathControlPlaneVIP, tc.want)
			}
			if strings.Contains(err.Error(), pathLoadBalancer) {
				t.Errorf("error %q mentions %s for a spec that does not use it", err, pathLoadBalancer)
			}
		})
	}
}

// firstMachineIP has no fields, so it is written as an empty mapping. Written
// with no value, blank or null, viper cannot tell it from an unset key, and the
// spec would load as if no load balancer was chosen.
func TestLoad_FirstMachineIPWithoutValueIsRefused(t *testing.T) {
	for name, block := range map[string]string{
		"blank": "  loadBalancer:\n    firstMachineIP:\n",
		"null":  "  loadBalancer:\n    firstMachineIP: ~\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(writeTemp(t, loadBalancerSpecYAML(1, block)))
			if err == nil {
				t.Fatal("Load accepted a firstMachineIP with no value; want error")
			}
			for _, want := range []string{pathLoadBalancerFirstMachineIP, "has no value", "firstMachineIP: {}"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}

	if _, err := Load(writeTemp(t, loadBalancerSpecYAML(1, "  loadBalancer:\n    firstMachineIP: {}\n"))); err != nil {
		t.Errorf("Load refused firstMachineIP: {} on one node: %v", err)
	}
}

func TestLoad_FirstMachineIPWithSeveralNodes(t *testing.T) {
	path := writeTemp(t, loadBalancerSpecYAML(3, "  loadBalancer:\n    firstMachineIP: {}\n"))

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted firstMachineIP with three control-plane nodes; want error")
	}
	for _, want := range []string{
		pathLoadBalancerFirstMachineIP,
		"is not allowed when the cluster has more than one control-plane node",
		pathLoadBalancerKubeVIPVIP,
		pathLoadBalancerExternalHost,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "is required") {
		t.Errorf("error %q is the legacy %s requirement, not the firstMachineIP message", err, pathControlPlaneVIP)
	}

	// With the development override the spec loads, and it loads as the
	// option the file selected rather than as a spec with no loadBalancer.
	inv, err := Load(path, WithAllowControlPlaneWithoutVIP(true))
	if err != nil {
		t.Fatalf("Load with the development override: %v", err)
	}
	if inv.Cluster.LoadBalancer == nil || inv.Cluster.LoadBalancer.FirstMachineIP == nil {
		t.Fatalf("LoadBalancer = %+v, want firstMachineIP selected", inv.Cluster.LoadBalancer)
	}
	if inv.ControlPlaneCount() != 3 {
		t.Errorf("ControlPlaneCount() = %d, want 3", inv.ControlPlaneCount())
	}
	if endpoint := inv.Cluster.ControlPlaneEndpoint(); endpoint != "" {
		t.Errorf("ControlPlaneEndpoint() = %q, want empty for firstMachineIP", endpoint)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("an override load changed later default validation")
	}
}

func TestLoad_FirstMachineIPCombinedWithAnotherOption(t *testing.T) {
	tests := []struct {
		name  string
		block string
	}{
		{
			name:  "with kubeVIP",
			block: "  loadBalancer:\n    firstMachineIP: {}\n    kubeVIP:\n      vip: \"10.0.0.100\"\n",
		},
		{
			name:  "with external",
			block: "  loadBalancer:\n    firstMachineIP: {}\n    external:\n      host: \"10.0.0.100\"\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeTemp(t, loadBalancerSpecYAML(1, tc.block)))
			if err == nil {
				t.Fatal("Load accepted firstMachineIP combined with another option; want error")
			}
			for _, want := range []string{pathLoadBalancer, "exactly one"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestValidate_LoadBalancerNico(t *testing.T) {
	tests := []struct {
		name    string
		lb      *LoadBalancer
		count   int
		wantErr string
	}{
		{name: "kubeVIP with three nodes", lb: kubeVIPLB("10.0.0.100"), count: 3},
		{name: "external with three nodes", lb: externalLB("10.0.0.100"), count: 3},
		{name: "firstMachineIP with one node", lb: firstMachineIPLB(), count: 1},
		{name: "firstMachineIP with three nodes", lb: firstMachineIPLB(), count: 3, wantErr: "is not allowed when the cluster has more than one control-plane node"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			inv := minimalValid()
			makeNico(inv)
			inv.Infra.Nico.ControlPlane.Count = tc.count
			inv.Cluster.ControlPlaneVIP = ""
			inv.Cluster.LoadBalancer = tc.lb
			err := inv.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestClusterControlPlaneEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		cluster  Cluster
		wantAddr string
		wantPath string
	}{
		{name: "nothing set", cluster: Cluster{}, wantPath: pathControlPlaneVIP},
		{name: "deprecated controlPlaneVIP", cluster: Cluster{ControlPlaneVIP: "10.0.0.100"}, wantAddr: "10.0.0.100", wantPath: pathControlPlaneVIP},
		{name: "external", cluster: Cluster{LoadBalancer: externalLB("10.0.0.101")}, wantAddr: "10.0.0.101", wantPath: pathLoadBalancerExternalHost},
		{name: "kubeVIP", cluster: Cluster{LoadBalancer: kubeVIPLB("10.0.0.102")}, wantAddr: "10.0.0.102", wantPath: pathLoadBalancerKubeVIPVIP},
		{name: "firstMachineIP has no address", cluster: Cluster{LoadBalancer: firstMachineIPLB()}, wantPath: pathControlPlaneVIP},
		{name: "empty loadBalancer has no address", cluster: Cluster{LoadBalancer: &LoadBalancer{}}, wantPath: pathControlPlaneVIP},
		{
			name:     "loadBalancer wins over a stale deprecated value",
			cluster:  Cluster{ControlPlaneVIP: "10.0.0.100", LoadBalancer: kubeVIPLB("10.0.0.102")},
			wantAddr: "10.0.0.102",
			wantPath: pathLoadBalancerKubeVIPVIP,
		},
		{
			name:     "firstMachineIP ignores a stale deprecated value",
			cluster:  Cluster{ControlPlaneVIP: "10.0.0.100", LoadBalancer: firstMachineIPLB()},
			wantPath: pathControlPlaneVIP,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cluster.ControlPlaneEndpoint(); got != tc.wantAddr {
				t.Errorf("ControlPlaneEndpoint() = %q, want %q", got, tc.wantAddr)
			}
			if got := tc.cluster.controlPlaneEndpointPath(); got != tc.wantPath {
				t.Errorf("controlPlaneEndpointPath() = %q, want %q", got, tc.wantPath)
			}
		})
	}
}

func TestControllerEndpoint_LoadBalancer(t *testing.T) {
	t.Run("external", func(t *testing.T) {
		inv := minimalValid()
		inv.Cluster.ControlPlaneVIP = ""
		inv.Cluster.LoadBalancer = externalLB("10.0.0.100")
		if ep, err := inv.ControllerEndpoint(); err != nil || ep != "10.0.0.100" {
			t.Errorf("ControllerEndpoint() = %q, %v; want 10.0.0.100", ep, err)
		}
	})
	t.Run("kubeVIP", func(t *testing.T) {
		inv := minimalValid()
		inv.Cluster.ControlPlaneVIP = ""
		inv.Cluster.LoadBalancer = kubeVIPLB("10.0.0.101")
		if ep, err := inv.ControllerEndpoint(); err != nil || ep != "10.0.0.101" {
			t.Errorf("ControllerEndpoint() = %q, %v; want 10.0.0.101", ep, err)
		}
	})
	t.Run("firstMachineIP with one node is that node", func(t *testing.T) {
		inv := minimalValid()
		inv.Cluster.ControlPlaneVIP = ""
		inv.Cluster.Nodes = inv.Cluster.Nodes[:1]
		inv.Cluster.LoadBalancer = firstMachineIPLB()
		if ep, err := inv.ControllerEndpoint(); err != nil || ep != "10.0.0.1" {
			t.Errorf("ControllerEndpoint() = %q, %v; want the node IP 10.0.0.1", ep, err)
		}
	})
	t.Run("nico with kubeVIP", func(t *testing.T) {
		inv := minimalValid()
		makeNico(inv)
		inv.Cluster.ControlPlaneVIP = ""
		inv.Cluster.LoadBalancer = kubeVIPLB("10.0.0.101")
		if ep, err := inv.ControllerEndpoint(); err != nil || ep != "10.0.0.101" {
			t.Errorf("ControllerEndpoint() = %q, %v; want 10.0.0.101", ep, err)
		}
	})
	t.Run("nico with firstMachineIP has no endpoint", func(t *testing.T) {
		inv := minimalValid()
		makeNico(inv)
		inv.Infra.Nico.ControlPlane.Count = 1
		inv.Cluster.ControlPlaneVIP = ""
		inv.Cluster.LoadBalancer = firstMachineIPLB()
		if ep, err := inv.ControllerEndpoint(); err == nil {
			t.Errorf("ControllerEndpoint() = %q; want an error: nico nodes have no address until allocated", ep)
		}
	})
}

func TestNicoVIPWarning_LoadBalancer(t *testing.T) {
	tests := []struct {
		name string
		spec SiteSpec
		want bool
	}{
		{name: "nico with external", spec: SiteSpec{Infra: Infra{Provider: ProviderNico}, Cluster: Cluster{LoadBalancer: externalLB("10.0.0.100")}}},
		{name: "nico with kubeVIP", spec: SiteSpec{Infra: Infra{Provider: ProviderNico}, Cluster: Cluster{LoadBalancer: kubeVIPLB("10.0.0.100")}}},
		{name: "nico with firstMachineIP", spec: SiteSpec{Infra: Infra{Provider: ProviderNico}, Cluster: Cluster{LoadBalancer: firstMachineIPLB()}}, want: true},
		{name: "provisioned with firstMachineIP", spec: SiteSpec{Cluster: Cluster{LoadBalancer: firstMachineIPLB()}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.spec.NicoVIPWarning()
			if tc.want != (got != "") {
				t.Errorf("NicoVIPWarning() = %q, want a warning: %v", got, tc.want)
			}
		})
	}
}
