// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package sitecluster

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/dsx-ai-factory/nvke/cli/internal/bootstrap/sitespec"
	api "github.com/dsx-ai-factory/nvke/controller/api"
)

// requestBody marshals a create request the way the CLI sends it and decodes it
// into a generic map, so a test sees the wire shape and not the Go struct.
func requestBody(t *testing.T, req api.CreateClusterJSONRequestBody) map[string]any {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshalling the request: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decoding the request body: %v", err)
	}
	return body
}

// controlPlaneConfigBody returns the controlPlaneConfig object of a wire body.
func controlPlaneConfigBody(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	config, ok := body["controlPlaneConfig"].(map[string]any)
	if !ok {
		t.Fatalf("controlPlaneConfig is missing or not an object in %v", body)
	}
	return config
}

func TestClusterRequest_APIEndpoint(t *testing.T) {
	infrastructures := []struct {
		name string
		spec func() *sitespec.SiteSpec
	}{
		{name: "provisioned", spec: testSiteSpec},
		{name: "nico", spec: nicoSiteSpec},
	}

	for _, infra := range infrastructures {
		t.Run(infra.name, func(t *testing.T) {
			t.Run("no endpoint set", func(t *testing.T) {
				req, err := ClusterRequest(infra.spec())
				if err != nil {
					t.Fatalf("ClusterRequest: %v", err)
				}
				if req.ControlPlaneVIP != "" {
					t.Errorf("ControlPlaneVIP = %q, want empty", req.ControlPlaneVIP)
				}
				if req.ControlPlaneConfig.LoadBalancer != nil {
					t.Errorf("LoadBalancer = %+v, want nil", req.ControlPlaneConfig.LoadBalancer)
				}
				body := requestBody(t, req)
				if _, present := body["controlPlaneVIP"]; present {
					t.Errorf("an unset VIP must be left out of the body, got %v", body["controlPlaneVIP"])
				}
				if _, present := controlPlaneConfigBody(t, body)["loadBalancer"]; present {
					t.Error("an unset loadBalancer must be left out of controlPlaneConfig")
				}
			})

			t.Run("deprecated controlPlaneVIP only", func(t *testing.T) {
				inv := infra.spec()
				inv.Cluster.ControlPlaneVIP = "10.0.0.100"
				req, err := ClusterRequest(inv)
				if err != nil {
					t.Fatalf("ClusterRequest: %v", err)
				}
				if req.ControlPlaneVIP != "10.0.0.100" {
					t.Errorf("ControlPlaneVIP = %q, want the deprecated field passed through", req.ControlPlaneVIP)
				}
				if req.ControlPlaneConfig.LoadBalancer != nil {
					t.Errorf("LoadBalancer = %+v, want nil: the site spec did not set one", req.ControlPlaneConfig.LoadBalancer)
				}
				body := requestBody(t, req)
				if body["controlPlaneVIP"] != "10.0.0.100" {
					t.Errorf(`body["controlPlaneVIP"] = %v, want 10.0.0.100`, body["controlPlaneVIP"])
				}
				if _, present := controlPlaneConfigBody(t, body)["loadBalancer"]; present {
					t.Error("controlPlaneConfig carries a loadBalancer for a spec that only sets controlPlaneVIP")
				}
			})

			loadBalancers := []struct {
				name     string
				lb       *sitespec.LoadBalancer
				wantAPI  api.LoadBalancer
				wantBody map[string]any
			}{
				{
					name:     "external",
					lb:       &sitespec.LoadBalancer{External: &sitespec.ExternalLoadBalancer{Host: "10.0.0.101"}},
					wantAPI:  api.LoadBalancer{External: &api.ExternalLoadBalancer{Host: "10.0.0.101"}},
					wantBody: map[string]any{"external": map[string]any{"host": "10.0.0.101"}},
				},
				{
					name:     "kubeVIP",
					lb:       &sitespec.LoadBalancer{KubeVIP: &sitespec.KubeVIPLoadBalancer{VIP: "10.0.0.102"}},
					wantAPI:  api.LoadBalancer{KubeVIP: &api.KubeVIPLoadBalancer{Vip: "10.0.0.102"}},
					wantBody: map[string]any{"kubeVIP": map[string]any{"vip": "10.0.0.102"}},
				},
				{
					name:     "firstMachineIP",
					lb:       &sitespec.LoadBalancer{FirstMachineIP: &sitespec.FirstMachineIPLoadBalancer{}},
					wantAPI:  api.LoadBalancer{FirstMachineIP: &api.FirstMachineIPLoadBalancer{}},
					wantBody: map[string]any{"firstMachineIP": map[string]any{}},
				},
			}
			for _, tc := range loadBalancers {
				t.Run("loadBalancer "+tc.name, func(t *testing.T) {
					inv := infra.spec()
					inv.Cluster.LoadBalancer = tc.lb
					req, err := ClusterRequest(inv)
					if err != nil {
						t.Fatalf("ClusterRequest: %v", err)
					}
					if req.ControlPlaneVIP != "" {
						t.Errorf("ControlPlaneVIP = %q, want empty when a loadBalancer is set", req.ControlPlaneVIP)
					}
					got := req.ControlPlaneConfig.LoadBalancer
					if got == nil {
						t.Fatal("ControlPlaneConfig.LoadBalancer is nil")
					}
					if !reflect.DeepEqual(*got, tc.wantAPI) {
						t.Errorf("LoadBalancer = %+v, want %+v", *got, tc.wantAPI)
					}

					body := requestBody(t, req)
					if _, present := body["controlPlaneVIP"]; present {
						t.Errorf("the body carries controlPlaneVIP %v alongside a loadBalancer", body["controlPlaneVIP"])
					}
					if lb := controlPlaneConfigBody(t, body)["loadBalancer"]; !reflect.DeepEqual(lb, tc.wantBody) {
						t.Errorf("controlPlaneConfig.loadBalancer = %v, want %v", lb, tc.wantBody)
					}
				})
			}
		})
	}
}

// TestClusterRequest_LoadBalancerKeepsTheRestOfTheControlPlaneConfig checks
// that setting the loadBalancer adds to the control-plane block rather than
// replacing what the builder already put there.
func TestClusterRequest_LoadBalancerKeepsTheRestOfTheControlPlaneConfig(t *testing.T) {
	plain, err := ClusterRequest(testSiteSpec())
	if err != nil {
		t.Fatalf("ClusterRequest: %v", err)
	}

	inv := testSiteSpec()
	inv.Cluster.LoadBalancer = &sitespec.LoadBalancer{KubeVIP: &sitespec.KubeVIPLoadBalancer{VIP: "10.0.0.102"}}
	withLB, err := ClusterRequest(inv)
	if err != nil {
		t.Fatalf("ClusterRequest: %v", err)
	}

	got := withLB.ControlPlaneConfig
	got.LoadBalancer = nil
	if !reflect.DeepEqual(got, plain.ControlPlaneConfig) {
		t.Errorf("controlPlaneConfig changed apart from loadBalancer:\n got %+v\nwant %+v", got, plain.ControlPlaneConfig)
	}
}
