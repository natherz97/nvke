// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package db_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/nvke/controller/internal/db"
	"github.com/dsx-ai-factory/nvke/controller/internal/testutil"
)

func TestControlPlaneConfigSpecProviderSettersMirrorFlatFields(t *testing.T) {
	instanceTypeID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	subnetID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	vpcPrefixID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	sshKeyGroupID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	machineTemplate := json.RawMessage(`{"kind":"ForgeMachineTemplate"}`)

	var nicoCfg db.ControlPlaneConfigSpec
	nicoCfg.SetNico(db.NicoControlPlaneProvider{
		InstanceTypePreference: []uuid.UUID{instanceTypeID},
		SubnetIDs:              []uuid.UUID{subnetID},
		VpcPrefixIDs:           []uuid.UUID{vpcPrefixID},
		Release:                "nke-260318",
		DiskSelectionStrategy:  db.DiskSelectionStrategyLargest,
		ConfigProfile:          "gpu",
		SSHKeyGroupIDs:         []uuid.UUID{sshKeyGroupID},
	})

	require.NotNil(t, nicoCfg.Nico)
	require.Nil(t, nicoCfg.Capi)
	assert.Equal(t, []uuid.UUID{instanceTypeID}, nicoCfg.Nico.InstanceTypePreference)
	assert.Equal(t, []uuid.UUID{subnetID}, nicoCfg.Nico.SubnetIDs)
	assert.Equal(t, []uuid.UUID{vpcPrefixID}, nicoCfg.Nico.VpcPrefixIDs)
	assert.Equal(t, "nke-260318", nicoCfg.Nico.Release)
	assert.Equal(t, db.DiskSelectionStrategyLargest, nicoCfg.Nico.DiskSelectionStrategy)
	assert.Equal(t, "gpu", nicoCfg.Nico.ConfigProfile)
	assert.Equal(t, []uuid.UUID{sshKeyGroupID}, nicoCfg.Nico.SSHKeyGroupIDs)

	var capiCfg db.ControlPlaneConfigSpec
	capiCfg.SetCapi(db.CAPIControlPlaneProvider{
		InfraMachineTemplatePreferences: []json.RawMessage{machineTemplate},
		SpireAttestationMethod:          db.SpireAttestationMethodJoinToken,
		BootstrapFiles:                  []db.CloudInitFile{{Path: "/etc/test", Content: "ok"}},
		PreKubeadmCommands:              []string{"echo pre"},
	})

	require.Nil(t, capiCfg.Nico)
	require.NotNil(t, capiCfg.Capi)
	require.Len(t, capiCfg.Capi.InfraMachineTemplatePreferences, 1)
	assert.JSONEq(t, string(machineTemplate), string(capiCfg.Capi.InfraMachineTemplatePreferences[0]))
	assert.Equal(t, db.SpireAttestationMethodJoinToken, capiCfg.Capi.SpireAttestationMethod)
	assert.Equal(t, []db.CloudInitFile{{Path: "/etc/test", Content: "ok"}}, capiCfg.Capi.BootstrapFiles)
	assert.Equal(t, []string{"echo pre"}, capiCfg.Capi.PreKubeadmCommands)
}

func TestControlPlaneConfigSpecProviderAccessors(t *testing.T) {
	instanceTypeID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	subnetID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	machineTemplate := json.RawMessage(`{"kind":"ForgeMachineTemplate"}`)
	cfg := db.ControlPlaneConfigSpec{
		Nico: &db.NicoControlPlaneProvider{
			InstanceTypePreference: []uuid.UUID{instanceTypeID},
			SubnetIDs:              []uuid.UUID{subnetID},
		},
		Capi: &db.CAPIControlPlaneProvider{
			InfraMachineTemplatePreferences: []json.RawMessage{machineTemplate},
			PreKubeadmCommands:              []string{"echo pre"},
		},
	}

	nico, err := cfg.NicoProvider()
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{instanceTypeID}, nico.InstanceTypePreference)
	nico.InstanceTypePreference[0] = uuid.New()
	assert.Equal(t, []uuid.UUID{instanceTypeID}, cfg.Nico.InstanceTypePreference)

	capi, err := cfg.CAPIProvider()
	require.NoError(t, err)
	require.Len(t, capi.InfraMachineTemplatePreferences, 1)
	assert.JSONEq(t, string(machineTemplate), string(capi.InfraMachineTemplatePreferences[0]))
	capi.PreKubeadmCommands[0] = "mutated"
	assert.Equal(t, []string{"echo pre"}, cfg.Capi.PreKubeadmCommands)
}

func TestCreateControlPlaneConfigRowIfChanged_StoresProviderPayload(t *testing.T) {
	ctx := context.Background()
	bunDB := testutil.SetupDB(ctx, t, (*db.Cluster)(nil), (*db.ControlPlaneConfiguration)(nil))
	dbImpl := db.NewDB(bunDB)
	cluster := newControlPlaneConfigTestCluster()
	createdCluster, err := dbImpl.CreateCluster(ctx, &cluster)
	require.NoError(t, err)

	instanceTypeID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	subnetID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	snapshotCfg := db.ControlPlaneConfigSpec{
		MachineAnnotations: map[string]string{"example.com/setup": "gpu,network"},
		MachineLabels:      map[string]string{"example.com/pool": "control-plane"},
	}
	snapshotCfg.SetNico(db.NicoControlPlaneProvider{
		InstanceTypePreference: []uuid.UUID{instanceTypeID},
		SubnetIDs:              []uuid.UUID{subnetID},
		Release:                "nke-260318",
		ConfigProfile:          "gpu",
	})

	require.NoError(t, db.CreateControlPlaneConfigRowIfChanged(ctx, dbImpl, &createdCluster, snapshotCfg, db.CreateControlPlaneConfigRowOpts{
		ChangeReason: "test provider sync",
	}))

	row, err := dbImpl.GetControlPlaneConfiguration(ctx, createdCluster.ControlPlaneConfig.ConfigurationID)
	require.NoError(t, err)
	require.Equal(t, snapshotCfg.MachineAnnotations, row.Config.MachineAnnotations)
	require.Equal(t, snapshotCfg.MachineLabels, row.Config.MachineLabels)
	require.NotNil(t, row.Config.Nico)
	require.Nil(t, row.Config.Capi)
	assert.Equal(t, []uuid.UUID{instanceTypeID}, row.Config.Nico.InstanceTypePreference)
	assert.Equal(t, []uuid.UUID{subnetID}, row.Config.Nico.SubnetIDs)
	assert.Equal(t, "nke-260318", row.Config.Nico.Release)
	assert.Equal(t, "gpu", row.Config.Nico.ConfigProfile)
}

// withNicoProfiles returns spec with the nico payload's OS and config profiles
// replaced, which is where the rollout diff reads them from.
func withNicoProfiles(spec db.ControlPlaneConfigSpec, release, configProfile string) db.ControlPlaneConfigSpec {
	spec.SetNico(db.NicoControlPlaneProvider{Release: release, ConfigProfile: configProfile})
	return spec
}

func TestIsControlPlaneRolloutRequired(t *testing.T) {
	base := withNicoProfiles(db.ControlPlaneConfigSpec{KubernetesVersion: "1.34.2"}, "os-v1", "cfg-v1")

	tests := []struct {
		name string
		mut  func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec
		want bool
	}{
		{
			name: "identical",
			mut:  func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec { return s },
			want: false,
		},
		{
			name: "kubernetes version differs",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				s.KubernetesVersion = "1.34.3"
				return s
			},
			want: true,
		},
		{
			name: "release differs",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				return withNicoProfiles(s, "os-v2", "cfg-v1")
			},
			want: true,
		},
		{
			name: "config profile differs",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				return withNicoProfiles(s, "os-v1", "cfg-v2")
			},
			want: true,
		},
		{
			name: "non-rollout field differs is ignored",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				s.SystemReservedMemory = "32Gi"
				s.TeleportEnabled = true
				return s
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := db.IsControlPlaneRolloutRequired(base, tt.mut(base))
			assert.Equal(t, tt.want, got)
			// The predicate is symmetric.
			assert.Equal(t, tt.want, db.IsControlPlaneRolloutRequired(tt.mut(base), base))
		})
	}
}

func TestComputeControlPlaneRolloutChanges(t *testing.T) {
	base := withNicoProfiles(db.ControlPlaneConfigSpec{KubernetesVersion: "1.34.2"}, "os-v1", "cfg-v1")

	tests := []struct {
		name string
		mut  func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec
		want []db.ControlPlaneRolloutChange
	}{
		{
			name: "identical returns nil",
			mut:  func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec { return s },
			want: nil,
		},
		{
			name: "kubernetes version differs",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				s.KubernetesVersion = "1.34.3"
				return s
			},
			want: []db.ControlPlaneRolloutChange{{Field: "kubernetesVersion", From: "1.34.2", To: "1.34.3"}},
		},
		{
			name: "release differs",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				return withNicoProfiles(s, "os-v2", "cfg-v1")
			},
			want: []db.ControlPlaneRolloutChange{{Field: "release", From: "os-v1", To: "os-v2"}},
		},
		{
			name: "config profile differs",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				return withNicoProfiles(s, "os-v1", "cfg-v2")
			},
			want: []db.ControlPlaneRolloutChange{{Field: "configProfile", From: "cfg-v1", To: "cfg-v2"}},
		},
		{
			name: "all rollout fields differ, in stable order",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				s.KubernetesVersion = "1.34.3"
				return withNicoProfiles(s, "os-v2", "cfg-v2")
			},
			want: []db.ControlPlaneRolloutChange{
				{Field: "kubernetesVersion", From: "1.34.2", To: "1.34.3"},
				{Field: "release", From: "os-v1", To: "os-v2"},
				{Field: "configProfile", From: "cfg-v1", To: "cfg-v2"},
			},
		},
		{
			name: "non-rollout field differs is ignored",
			mut: func(s db.ControlPlaneConfigSpec) db.ControlPlaneConfigSpec {
				s.SystemReservedMemory = "32Gi"
				s.TeleportEnabled = true
				return s
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := db.ComputeControlPlaneRolloutChanges(base, tt.mut(base))
			assert.Equal(t, tt.want, got)
			// IsControlPlaneRolloutRequired is a thin predicate over the diff and must agree.
			assert.Equal(t, len(tt.want) > 0, db.IsControlPlaneRolloutRequired(base, tt.mut(base)))
		})
	}
}

func TestComputeControlPlaneRolloutChanges_CAPIBootstrapFileChange(t *testing.T) {
	pinned := db.ControlPlaneConfigSpec{KubernetesVersion: "1.34.8"}
	pinned.SetCapi(db.CAPIControlPlaneProvider{
		InfraMachineTemplatePreferences: []json.RawMessage{json.RawMessage(`{"kind":"DockerMachineTemplate","spec":{"template":{"spec":{}}}}`)},
		BootstrapFiles:                  []db.CloudInitFile{{Path: "/etc/nvke-release", Content: "v1"}},
		PreKubeadmCommands:              []string{"echo ready"},
	})
	desiredProvider, err := pinned.CAPIProvider()
	require.NoError(t, err)
	desiredProvider.BootstrapFiles[0].Content = "v2"
	desired := pinned
	desired.SetCapi(desiredProvider)

	assert.Equal(t, []db.ControlPlaneRolloutChange{{
		Field: "infra.bootstrap",
		From:  "previous bootstrap",
		To:    "updated bootstrap",
	}}, db.ComputeControlPlaneRolloutChanges(pinned, desired))
	assert.True(t, db.IsControlPlaneRolloutRequired(pinned, desired))
}

func newControlPlaneConfigTestCluster() db.Cluster {
	now := time.Now()
	cluster := db.Cluster{
		ID:                uuid.New(),
		Name:              fmt.Sprintf("cluster-%s", uuid.New().String()),
		KubernetesVersion: "1.29",
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	cluster.SetNicoProvider(db.NicoClusterProvider{
		OrgID:  "org1",
		SiteID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		VpcID:  uuid.New(),
	})
	return cluster
}

func TestControlPlaneConfigSpec_MachineMetadataDefaults(t *testing.T) {
	empty := db.ControlPlaneConfigSpec{}
	explicit := db.ControlPlaneConfigSpec{MachineAnnotations: map[string]string{}, MachineLabels: map[string]string{}}
	require.True(t, empty.Equal(explicit))
	configured := db.ControlPlaneConfigSpec{MachineAnnotations: map[string]string{"example.com/setup": "gpu"}, MachineLabels: map[string]string{"example.com/pool": "cp"}}
	require.False(t, empty.Equal(configured))
	require.False(t, db.IsControlPlaneRolloutRequired(empty, configured))
}

func TestControlPlaneConfigSpec_KubeVIPManaged(t *testing.T) {
	tests := []struct {
		name         string
		loadBalancer *db.LoadBalancer
		want         bool
	}{
		{name: "no load balancer", want: false},
		{name: "kubeVIP", loadBalancer: &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "10.0.0.10"}}, want: true},
		{name: "external", loadBalancer: &db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.10"}}, want: false},
		{name: "firstMachineIP", loadBalancer: &db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := db.ControlPlaneConfigSpec{LoadBalancer: tt.loadBalancer}
			assert.Equal(t, tt.want, spec.KubeVIPManaged())
		})
	}
}

func TestControlPlaneConfigSpec_APIEndpoint(t *testing.T) {
	assert.Empty(t, db.ControlPlaneConfigSpec{}.APIEndpoint(), "no load balancer means no endpoint")
	assert.Equal(t, "10.0.0.11",
		db.ControlPlaneConfigSpec{LoadBalancer: &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "10.0.0.11"}}}.APIEndpoint())
	assert.Equal(t, "10.0.0.20",
		db.ControlPlaneConfigSpec{LoadBalancer: &db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.20"}}}.APIEndpoint())
	assert.Empty(t,
		db.ControlPlaneConfigSpec{LoadBalancer: &db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}}}.APIEndpoint())
}

// The controller reads the endpoint from the stored load balancer alone. The
// deprecated control_plane_vip column is written for an older controller after a
// rollback and is never read, so a cluster with only that column has no endpoint.
func TestCluster_LoadBalancerAccessors(t *testing.T) {
	cluster := func(legacyVIP string, lb *db.LoadBalancer) db.Cluster {
		return db.Cluster{
			LegacyControlPlaneVIP: legacyVIP,
			ControlPlaneConfig:    db.ControlPlaneConfig{ControlPlaneConfigSpec: db.ControlPlaneConfigSpec{LoadBalancer: lb}},
		}
	}

	t.Run("kubeVIP", func(t *testing.T) {
		c := cluster("10.0.0.10", &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "10.0.0.10"}})
		assert.Equal(t, "10.0.0.10", c.APIEndpoint())
		assert.True(t, c.KubeVIPManaged())
	})

	t.Run("external reads its host, and its empty column does not matter", func(t *testing.T) {
		c := cluster("", &db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.20"}})
		assert.Equal(t, "10.0.0.20", c.APIEndpoint())
		assert.False(t, c.KubeVIPManaged())
	})

	t.Run("the stored load balancer wins over a different column", func(t *testing.T) {
		c := cluster("10.0.0.10", &db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.20"}})
		assert.Equal(t, "10.0.0.20", c.APIEndpoint())
		assert.False(t, c.KubeVIPManaged())
	})

	t.Run("a cluster with only the deprecated column has no endpoint", func(t *testing.T) {
		c := cluster("10.0.0.10", nil)
		assert.Empty(t, c.APIEndpoint())
		assert.False(t, c.KubeVIPManaged())
	})

	t.Run("no endpoint", func(t *testing.T) {
		c := cluster("", nil)
		assert.Empty(t, c.APIEndpoint())
		assert.False(t, c.KubeVIPManaged())
	})
}

// The deprecated column mirrors kube-vip only. An older controller reads a value
// there as "NVKE runs kube-vip", so no other kind may be written into it.
func TestLoadBalancer_LegacyControlPlaneVIP(t *testing.T) {
	var nilLoadBalancer *db.LoadBalancer
	assert.Empty(t, nilLoadBalancer.LegacyControlPlaneVIP())

	assert.Equal(t, "10.0.0.11",
		(&db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "10.0.0.11"}}).LegacyControlPlaneVIP())
	assert.Empty(t,
		(&db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.10"}}).LegacyControlPlaneVIP(),
		"an external endpoint is not mirrored")
	assert.Empty(t,
		(&db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}}).LegacyControlPlaneVIP())
	assert.Empty(t, (&db.LoadBalancer{}).LegacyControlPlaneVIP())
}

func TestLoadBalancer_Endpoint(t *testing.T) {
	var nilLoadBalancer *db.LoadBalancer
	assert.Empty(t, nilLoadBalancer.Endpoint(), "a nil load balancer names no endpoint")

	assert.Equal(t, "10.0.0.10",
		(&db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.10"}}).Endpoint())
	assert.Equal(t, "10.0.0.11",
		(&db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "10.0.0.11"}}).Endpoint())
	assert.Empty(t,
		(&db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}}).Endpoint(),
		"firstMachineIP uses the first node's IP, so it names no address")
	assert.Empty(t, (&db.LoadBalancer{}).Endpoint(), "a load balancer with no option names no endpoint")
}

func TestControlPlaneConfigSpec_EqualLoadBalancer(t *testing.T) {
	base := db.ControlPlaneConfigSpec{KubernetesVersion: "1.30.0"}
	withLoadBalancer := func(lb *db.LoadBalancer) db.ControlPlaneConfigSpec {
		spec := base
		spec.LoadBalancer = lb
		return spec
	}
	kubeVIP := func(vip string) *db.LoadBalancer {
		return &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: vip}}
	}

	assert.True(t, withLoadBalancer(nil).Equal(withLoadBalancer(nil)), "two specs without a load balancer are equal")
	assert.True(t, withLoadBalancer(kubeVIP("10.0.0.10")).Equal(withLoadBalancer(kubeVIP("10.0.0.10"))),
		"equal load balancers in separate allocations are equal")
	assert.False(t, withLoadBalancer(kubeVIP("10.0.0.10")).Equal(withLoadBalancer(kubeVIP("10.0.0.11"))),
		"a different address differs")
	assert.False(t,
		withLoadBalancer(kubeVIP("10.0.0.10")).Equal(withLoadBalancer(
			&db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.10"}},
		)),
		"a different option with the same address differs")
	assert.False(t, withLoadBalancer(nil).Equal(withLoadBalancer(&db.LoadBalancer{})),
		"no load balancer differs from an empty one that is set")
	assert.False(t,
		withLoadBalancer(nil).Equal(withLoadBalancer(
			&db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}},
		)),
		"no load balancer differs from firstMachineIP")
}

func TestControlPlaneConfigSpec_LoadBalancerJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		lb       *db.LoadBalancer
		wantJSON string
	}{
		{
			name:     "external",
			lb:       &db.LoadBalancer{External: &db.ExternalLoadBalancer{Host: "10.0.0.10"}},
			wantJSON: `{"external":{"host":"10.0.0.10"}}`,
		},
		{
			name:     "kubeVIP",
			lb:       &db.LoadBalancer{KubeVIP: &db.KubeVIPLoadBalancer{VIP: "10.0.0.11"}},
			wantJSON: `{"kube_vip":{"vip":"10.0.0.11"}}`,
		},
		{
			name:     "firstMachineIP",
			lb:       &db.LoadBalancer{FirstMachineIP: &db.FirstMachineIPLoadBalancer{}},
			wantJSON: `{"first_machine_ip":{}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(db.ControlPlaneConfigSpec{KubernetesVersion: "1.30.0", LoadBalancer: tt.lb})
			require.NoError(t, err)

			var stored map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(raw, &stored))
			require.Contains(t, stored, "load_balancer")
			assert.JSONEq(t, tt.wantJSON, string(stored["load_balancer"]))

			var got db.ControlPlaneConfigSpec
			require.NoError(t, json.Unmarshal(raw, &got))
			require.NotNil(t, got.LoadBalancer)
			assert.Equal(t, tt.lb, got.LoadBalancer)
			if tt.lb.FirstMachineIP != nil {
				assert.NotNil(t, got.LoadBalancer.FirstMachineIP, "firstMachineIP must survive as a non-nil pointer")
			}
		})
	}
}

func TestControlPlaneConfigSpec_LoadBalancerAbsentInStoredJSON(t *testing.T) {
	// A config row written before load balancers existed has no load_balancer key.
	var spec db.ControlPlaneConfigSpec
	require.NoError(t, json.Unmarshal([]byte(`{"kubernetes_version":"1.30.0","teleport_enabled":true}`), &spec))

	assert.Nil(t, spec.LoadBalancer)
	assert.False(t, spec.KubeVIPManaged(), "no stored load balancer means kube-vip is not managed")
	assert.Empty(t, spec.APIEndpoint())

	raw, err := json.Marshal(spec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "load_balancer", "a nil load balancer is omitted when written back")
}
