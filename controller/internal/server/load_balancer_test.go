// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/nvke/controller/api"
	"github.com/dsx-ai-factory/nvke/controller/internal/db"
	"github.com/dsx-ai-factory/nvke/controller/internal/testutil"
)

func externalLB(host string) *api.LoadBalancer {
	return &api.LoadBalancer{External: &api.ExternalLoadBalancer{Host: host}}
}

func kubeVIPLB(vip string) *api.LoadBalancer {
	return &api.LoadBalancer{KubeVIP: &api.KubeVIPLoadBalancer{Vip: vip}}
}

func firstMachineIPLB() *api.LoadBalancer {
	return &api.LoadBalancer{FirstMachineIP: &api.FirstMachineIPLoadBalancer{}}
}

// lbRequest builds a create request that carries only the endpoint fields.
func lbRequest(lb *api.LoadBalancer, legacyVIP string) *api.CreateClusterRequest {
	return &api.CreateClusterRequest{
		ControlPlaneVIP:    legacyVIP,
		ControlPlaneConfig: &api.CreateClusterControlPlaneConfig{DesiredCount: 1, LoadBalancer: lb},
	}
}

// lbForgeRequest builds a valid Forge create request with three control-plane
// nodes and the given endpoint fields.
func lbForgeRequest(lb *api.LoadBalancer, legacyVIP string) *api.CreateClusterRequest {
	req := validForgeCreateClusterRequest()
	req.ControlPlaneConfig = validForgeControlPlaneConfig()
	req.ControlPlaneConfig.LoadBalancer = lb
	req.ControlPlaneVIP = legacyVIP
	return req
}

func TestControlPlaneEndpoint(t *testing.T) {
	tests := []struct {
		name    string
		req     *api.CreateClusterRequest
		want    string
		wantErr string
	}{
		{name: "legacy controlPlaneVIP is trimmed", req: lbRequest(nil, " 172.31.0.10 "), want: "172.31.0.10"},
		{name: "no endpoint", req: lbRequest(nil, "")},
		{name: "no controlPlaneConfig uses the legacy field", req: &api.CreateClusterRequest{ControlPlaneVIP: "172.31.0.10"}, want: "172.31.0.10"},
		{name: "external", req: lbRequest(externalLB("172.31.0.11"), ""), want: "172.31.0.11"},
		{name: "kubeVIP", req: lbRequest(kubeVIPLB("172.31.0.12"), ""), want: "172.31.0.12"},
		{name: "firstMachineIP has no address", req: lbRequest(firstMachineIPLB(), "")},
		{name: "external host is trimmed", req: lbRequest(externalLB(" 172.31.0.11 "), ""), want: "172.31.0.11"},
		{name: "kubeVIP vip is trimmed", req: lbRequest(kubeVIPLB(" 172.31.0.12 "), ""), want: "172.31.0.12"},
		{name: "empty loadBalancer", req: lbRequest(&api.LoadBalancer{}, ""), wantErr: "exactly one of external, kubeVIP or firstMachineIP"},
		{
			name:    "external and kubeVIP",
			req:     lbRequest(&api.LoadBalancer{External: &api.ExternalLoadBalancer{Host: "172.31.0.11"}, KubeVIP: &api.KubeVIPLoadBalancer{Vip: "172.31.0.12"}}, ""),
			wantErr: "exactly one",
		},
		{
			name: "all three options",
			req: lbRequest(&api.LoadBalancer{
				External:       &api.ExternalLoadBalancer{Host: "172.31.0.11"},
				KubeVIP:        &api.KubeVIPLoadBalancer{Vip: "172.31.0.12"},
				FirstMachineIP: &api.FirstMachineIPLoadBalancer{},
			}, ""),
			wantErr: "exactly one",
		},
		{
			name: "kubeVIP with a matching controlPlaneVIP",
			req:  lbRequest(kubeVIPLB("172.31.0.10"), "172.31.0.10"),
			want: "172.31.0.10",
		},
		{
			name: "external with a matching controlPlaneVIP",
			req:  lbRequest(externalLB("172.31.0.11"), " 172.31.0.11 "),
			want: "172.31.0.11",
		},
		{
			name:    "loadBalancer with a different controlPlaneVIP",
			req:     lbRequest(kubeVIPLB("172.31.0.12"), "172.31.0.10"),
			wantErr: `controlPlaneVIP "172.31.0.10" must equal the address in controlPlaneConfig.loadBalancer ("172.31.0.12")`,
		},
		{
			name:    "firstMachineIP with controlPlaneVIP",
			req:     lbRequest(firstMachineIPLB(), "172.31.0.10"),
			wantErr: "controlPlaneVIP cannot be combined with controlPlaneConfig.loadBalancer.firstMachineIP",
		},
		{name: "empty external host", req: lbRequest(externalLB(""), ""), wantErr: "controlPlaneConfig.loadBalancer.external.host must not be empty"},
		{name: "whitespace-only kubeVIP vip", req: lbRequest(kubeVIPLB("  "), ""), wantErr: "controlPlaneConfig.loadBalancer.kubeVIP.vip must not be empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := controlPlaneEndpoint(tt.req)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadBalancerMapping_RoundTrip(t *testing.T) {
	for name, lb := range map[string]*api.LoadBalancer{
		"external":       externalLB("172.31.0.11"),
		"kubeVIP":        kubeVIPLB("172.31.0.12"),
		"firstMachineIP": firstMachineIPLB(),
	} {
		t.Run(name, func(t *testing.T) {
			stored := dbLoadBalancerFromAPI(lb)
			require.NotNil(t, stored)
			assert.Equal(t, lb, apiLoadBalancerFromDB(stored))
		})
	}

	t.Run("nil in, nil out", func(t *testing.T) {
		assert.Nil(t, dbLoadBalancerFromAPI(nil))
		assert.Nil(t, apiLoadBalancerFromDB(nil))
	})

	t.Run("stored form", func(t *testing.T) {
		external := dbLoadBalancerFromAPI(externalLB(" 172.31.0.11 "))
		require.NotNil(t, external.External)
		assert.Equal(t, "172.31.0.11", external.External.Host, "the address is trimmed when stored")
		assert.Nil(t, external.KubeVIP)
		assert.Nil(t, external.FirstMachineIP)

		kubeVIP := dbLoadBalancerFromAPI(kubeVIPLB("172.31.0.12"))
		require.NotNil(t, kubeVIP.KubeVIP)
		assert.Equal(t, "172.31.0.12", kubeVIP.KubeVIP.VIP)
		assert.Nil(t, kubeVIP.External)

		firstMachineIP := dbLoadBalancerFromAPI(firstMachineIPLB())
		assert.NotNil(t, firstMachineIP.FirstMachineIP)
		assert.Nil(t, firstMachineIP.External)
		assert.Nil(t, firstMachineIP.KubeVIP)
	})
}

func TestLoadBalancerFirstMachineIP_JSON(t *testing.T) {
	out, err := json.Marshal(firstMachineIPLB())
	require.NoError(t, err)
	assert.JSONEq(t, `{"firstMachineIP":{}}`, string(out))

	var decoded api.LoadBalancer
	require.NoError(t, json.Unmarshal(out, &decoded))
	require.NotNil(t, decoded.FirstMachineIP, "an empty object must still select firstMachineIP")
	assert.Nil(t, decoded.External)
	assert.Nil(t, decoded.KubeVIP)

	// The stored spec is JSON in the config row, so the choice must survive it.
	spec := db.ControlPlaneConfigSpec{LoadBalancer: dbLoadBalancerFromAPI(firstMachineIPLB())}
	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	var stored db.ControlPlaneConfigSpec
	require.NoError(t, json.Unmarshal(raw, &stored))
	require.NotNil(t, stored.LoadBalancer)
	assert.NotNil(t, stored.LoadBalancer.FirstMachineIP)
}

func TestDBClusterFromAPI_ControlPlaneVIPColumn(t *testing.T) {
	tests := []struct {
		name    string
		req     *api.CreateClusterRequest
		wantVIP string
		wantErr string
	}{
		// The column mirrors kube-vip only: an older controller reads a value as "NVKE
		// runs kube-vip", which is wrong for an external endpoint.
		{name: "external leaves the column empty", req: lbForgeRequest(externalLB("172.31.0.11"), "")},
		{name: "kubeVIP vip", req: lbForgeRequest(kubeVIPLB("172.31.0.12"), ""), wantVIP: "172.31.0.12"},
		{name: "firstMachineIP leaves the column empty", req: lbForgeRequest(firstMachineIPLB(), "")},
		{name: "legacy top-level field", req: lbForgeRequest(nil, "172.31.0.10"), wantVIP: "172.31.0.10"},
		{name: "no endpoint", req: lbForgeRequest(nil, "")},
		{name: "kubeVIP with a matching controlPlaneVIP", req: lbForgeRequest(kubeVIPLB("172.31.0.12"), "172.31.0.12"), wantVIP: "172.31.0.12"},
		{name: "external with a matching controlPlaneVIP leaves the column empty", req: lbForgeRequest(externalLB("172.31.0.11"), "172.31.0.11")},
		{name: "both set and different", req: lbForgeRequest(kubeVIPLB("172.31.0.12"), "172.31.0.10"), wantErr: "must equal the address"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster, err := DBClusterFromAPI(tt.req)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantVIP, cluster.LegacyControlPlaneVIP)
		})
	}
}

func TestValidateCreateClusterRequestFields_LoadBalancer(t *testing.T) {
	tests := []struct {
		name       string
		lb         *api.LoadBalancer
		legacyVIP  string
		allowNoVIP bool
		wantErr    string
	}{
		{name: "external counts as a VIP for three control planes", lb: externalLB("172.31.0.10")},
		{name: "kubeVIP counts as a VIP for three control planes", lb: kubeVIPLB("172.31.0.10")},
		{name: "legacy controlPlaneVIP still passes the gate", legacyVIP: "172.31.0.10"},
		{name: "firstMachineIP with three control planes needs a VIP", lb: firstMachineIPLB(), wantErr: controlPlaneVIPRequiredMessage},
		{name: "firstMachineIP is allowed with the development override", lb: firstMachineIPLB(), allowNoVIP: true},
		{name: "loadBalancer with a matching controlPlaneVIP", lb: kubeVIPLB("172.31.0.10"), legacyVIP: "172.31.0.10"},
		{name: "loadBalancer with a different controlPlaneVIP", lb: kubeVIPLB("172.31.0.10"), legacyVIP: "172.31.0.11", wantErr: "must equal the address"},
		{name: "loadBalancer with no option", lb: &api.LoadBalancer{}, wantErr: "exactly one"},
		{
			name:    "loadBalancer with two options",
			lb:      &api.LoadBalancer{External: &api.ExternalLoadBalancer{Host: "172.31.0.10"}, KubeVIP: &api.KubeVIPLoadBalancer{Vip: "172.31.0.11"}},
			wantErr: "exactly one",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := clusterService{allowControlPlaneWithoutVIP: tt.allowNoVIP}
			err := svc.ValidateCreateClusterRequestFields(lbForgeRequest(tt.lb, tt.legacyVIP))
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestPreflightCheckCreateClusterRequest_LoadBalancerAddress(t *testing.T) {
	tests := []struct {
		name string
		lb   *api.LoadBalancer
		want string
	}{
		{name: "external inside the pod CIDR", lb: externalLB("10.232.64.10"), want: "network.podCIDR"},
		{name: "kubeVIP inside the pod CIDR", lb: kubeVIPLB("10.232.64.10"), want: "network.podCIDR"},
		{name: "external inside the service CIDR", lb: externalLB("10.233.0.10"), want: "network.serviceCIDR"},
		{name: "kubeVIP inside the service CIDR", lb: kubeVIPLB("10.233.0.10"), want: "network.serviceCIDR"},
		{name: "IPv6 address", lb: externalLB("2001:db8::1"), want: "must be an IPv4 address"},
		{name: "not an address", lb: kubeVIPLB("not-an-ip"), want: "invalid control-plane endpoint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := lbForgeRequest(tt.lb, "")
			req.Network = &api.ClusterNetworkCreateConfig{PodCIDR: "10.232.64.0/18", ServiceCIDR: "10.233.0.0/18"}

			svc := clusterService{}
			_, err := svc.preflightCheckCreateClusterRequest(context.Background(), req)

			var badRequest *badRequestError
			require.ErrorAs(t, err, &badRequest, "expected a 400")
			assert.Contains(t, err.Error(), "control-plane endpoint")
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestAPIControlPlaneConfigFromDB_LoadBalancer(t *testing.T) {
	configWith := func(lb *db.LoadBalancer) db.ControlPlaneConfig {
		return db.ControlPlaneConfig{DesiredCount: 1, ControlPlaneConfigSpec: db.ControlPlaneConfigSpec{LoadBalancer: lb}}
	}
	tests := []struct {
		name string
		lb   *db.LoadBalancer
		want *api.LoadBalancer
	}{
		{name: "external", lb: &db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "172.31.0.11"}}, want: externalLB("172.31.0.11")},
		{name: "kubeVIP", lb: &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "172.31.0.12"}}, want: kubeVIPLB("172.31.0.12")},
		{name: "firstMachineIP", lb: &db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}}, want: firstMachineIPLB()},
		{name: "none", lb: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apiControlPlaneConfigFromDB(configWith(tt.lb))
			assert.Equal(t, tt.want, got.LoadBalancer)
		})
	}
}

func TestAPIClusterFromDB_EndpointFields(t *testing.T) {
	newCluster := func(vip string, lb *db.LoadBalancer) *db.Cluster {
		return &db.Cluster{
			ID:                    uuid.New(),
			ProviderType:          db.ProviderTypeCAPI,
			LegacyControlPlaneVIP: vip,
			ControlPlaneConfig: db.ControlPlaneConfig{
				DesiredCount:           1,
				ControlPlaneConfigSpec: db.ControlPlaneConfigSpec{LoadBalancer: lb},
			},
		}
	}

	t.Run("kubeVIP cluster returns both", func(t *testing.T) {
		got := APIClusterFromDB(newCluster("172.31.0.12", &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "172.31.0.12"}}))
		require.NotNil(t, got.ControlPlaneVIP)
		assert.Equal(t, "172.31.0.12", *got.ControlPlaneVIP)
		require.NotNil(t, got.ControlPlaneConfig)
		assert.Equal(t, kubeVIPLB("172.31.0.12"), got.ControlPlaneConfig.LoadBalancer)
	})

	t.Run("external cluster returns only loadBalancer", func(t *testing.T) {
		got := APIClusterFromDB(newCluster("", &db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "172.31.0.11"}}))
		assert.Nil(t, got.ControlPlaneVIP, "controlPlaneVIP means a kube-vip VIP")
		assert.Equal(t, externalLB("172.31.0.11"), got.ControlPlaneConfig.LoadBalancer)
	})

	t.Run("firstMachineIP cluster returns only loadBalancer", func(t *testing.T) {
		got := APIClusterFromDB(newCluster("", &db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}}))
		assert.Nil(t, got.ControlPlaneVIP)
		assert.Equal(t, firstMachineIPLB(), got.ControlPlaneConfig.LoadBalancer)
	})

	t.Run("the deprecated column is never reported", func(t *testing.T) {
		got := APIClusterFromDB(newCluster("172.31.0.10", nil))
		assert.Nil(t, got.ControlPlaneVIP, "a cluster with only the column has no stored endpoint")
		assert.Nil(t, got.ControlPlaneConfig.LoadBalancer)
	})

	t.Run("a cluster with no endpoint reports neither", func(t *testing.T) {
		got := APIClusterFromDB(newCluster("", nil))
		assert.Nil(t, got.ControlPlaneVIP)
		assert.Nil(t, got.ControlPlaneConfig.LoadBalancer)
	})
}

func TestApplyClusterControlPlaneConfigUpdate_CarriesLoadBalancerForward(t *testing.T) {
	oldConfigID := uuid.New()
	newConfigID := uuid.New()
	loadBalancer := &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "172.31.0.10"}}
	currentSpec := db.ControlPlaneConfigSpec{KubernetesVersion: "1.34.2", LoadBalancer: loadBalancer}
	currentSpec.SetNico(db.NicoControlPlaneProvider{
		InstanceTypePreference: []uuid.UUID{uuid.New()},
		Release:                "os-v1",
		ConfigProfile:          "cfg-v1",
	})
	cluster := &db.Cluster{
		ID:                    uuid.New(),
		ProviderType:          db.ProviderTypeNico,
		Nico:                  &db.NicoClusterProvider{},
		KubernetesVersion:     "1.34.2",
		LegacyControlPlaneVIP: "172.31.0.10",
		ControlPlaneConfig: db.ControlPlaneConfig{
			ConfigurationID:        oldConfigID,
			DesiredCount:           1,
			ControlPlaneConfigSpec: currentSpec,
		},
	}

	var created *db.ControlPlaneConfiguration
	tx := &testutil.MockTx{}
	tx.On("GetControlPlaneConfiguration", mock.Anything, oldConfigID).Return(db.ControlPlaneConfiguration{
		ID: oldConfigID, ClusterID: cluster.ID, Config: currentSpec,
	}, nil).Once()
	tx.On("CreateControlPlaneConfiguration", mock.Anything, mock.MatchedBy(func(row *db.ControlPlaneConfiguration) bool {
		created = row
		return true
	})).Return(db.ControlPlaneConfiguration{ID: newConfigID, ClusterID: cluster.ID}, nil).Once()

	newProfile := "cfg-v2"
	svc := clusterService{}
	err := svc.applyClusterControlPlaneConfigUpdate(context.Background(), tx, cluster, &api.UpdateClusterControlPlaneConfig{
		Nico: &api.NicoControlPlaneProviderUpdate{ConfigProfile: &newProfile},
	})
	require.NoError(t, err)
	require.NotNil(t, created, "the changed config profile should create a new config row")

	assert.Equal(t, loadBalancer, created.Config.LoadBalancer, "the new row carries the stored load balancer unchanged")
	assert.Equal(t, loadBalancer, cluster.ControlPlaneConfig.LoadBalancer)
	assert.Equal(t, "172.31.0.10", cluster.LegacyControlPlaneVIP)
}
