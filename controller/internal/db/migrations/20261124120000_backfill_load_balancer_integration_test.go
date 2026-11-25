// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package migrations

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun/migrate"

	"github.com/dsx-ai-factory/nvke/controller/internal/db"
)

// TestBackfillLoadBalancer runs against the server named by
// NVKE_TEST_POSTGRES_DSN, in a database it creates and drops.
func TestBackfillLoadBalancer(t *testing.T) {
	ctx := context.Background()
	bunDB := newMigratedDatabase(ctx, t)

	insertCluster := func(name, vip string) uuid.UUID {
		cluster := db.Cluster{
			ID:                    uuid.New(),
			Name:                  name,
			KubernetesVersion:     "1.34.2",
			ProviderType:          db.ProviderTypeNico,
			LegacyControlPlaneVIP: vip,
		}
		cluster.SetNicoProvider(db.NicoClusterProvider{VpcID: uuid.New()})
		_, err := bunDB.NewInsert().Model(&cluster).Exec(ctx)
		require.NoError(t, err)
		return cluster.ID
	}
	insertConfig := func(clusterID uuid.UUID, config string) uuid.UUID {
		id := uuid.New()
		_, err := bunDB.ExecContext(ctx,
			`INSERT INTO control_plane_configurations (id, cluster_id, config) VALUES (?, ?, ?::jsonb)`, id, clusterID, config)
		require.NoError(t, err)
		return id
	}
	loadBalancerOf := func(configID uuid.UUID) string {
		var out *string
		require.NoError(t, bunDB.NewRaw(
			`SELECT (config -> 'load_balancer')::text FROM control_plane_configurations WHERE id = ?`, configID).Scan(ctx, &out))
		if out == nil {
			return ""
		}
		return *out
	}

	// A cluster created with the deprecated controlPlaneVIP, with two config versions.
	legacy := insertCluster("legacy", "10.0.0.10")
	legacyV1 := insertConfig(legacy, `{"kubernetes_version":"1.34.2"}`)
	legacyV2 := insertConfig(legacy, `{"kubernetes_version":"1.34.3","system_reserved_memory":"2Gi"}`)

	// A cluster whose config already has a load_balancer is left alone, even though
	// the column holds a different address.
	current := insertCluster("current", "10.0.0.20")
	currentCfg := insertConfig(current, `{"load_balancer":{"kube_vip":{"vip":"10.0.0.99"}}}`)

	// A cluster with no VIP has nothing to backfill.
	none := insertCluster("none", "")
	noneCfg := insertConfig(none, `{"kubernetes_version":"1.34.2"}`)

	// A cluster with an external endpoint has an empty column.
	external := insertCluster("external", "")
	externalCfg := insertConfig(external, `{"load_balancer":{"external":{"host":"10.0.0.30"}}}`)

	var migration *migrate.Migration
	sorted := migrations.Sorted()
	for i := range sorted {
		if sorted[i].Name == "20261124120000" {
			migration = &sorted[i]
		}
	}
	require.NotNil(t, migration, "the load_balancer backfill is not registered")
	migrator := migrate.NewMigrator(bunDB, migrations)

	// The second run checks that the migration is idempotent.
	for run := 1; run <= 2; run++ {
		require.NoError(t, migration.Up(ctx, migrator, migration), "run %d", run)

		const backfilled = `{"kube_vip": {"vip": "10.0.0.10"}}`
		assert.JSONEq(t, backfilled, loadBalancerOf(legacyV1), "every version is backfilled")
		assert.JSONEq(t, backfilled, loadBalancerOf(legacyV2), "every version is backfilled")
		assert.JSONEq(t, `{"kube_vip": {"vip": "10.0.0.99"}}`, loadBalancerOf(currentCfg), "an existing load_balancer is kept")
		assert.Empty(t, loadBalancerOf(noneCfg), "a cluster with no VIP is not given a load_balancer")
		assert.JSONEq(t, `{"external": {"host": "10.0.0.30"}}`, loadBalancerOf(externalCfg), "an external endpoint is untouched")
	}

	// The controller reads the backfilled value through the Go types, so decode the
	// stored config as it does and check the accessors it calls.
	for _, configID := range []uuid.UUID{legacyV1, legacyV2} {
		var raw string
		require.NoError(t, bunDB.NewRaw(`SELECT config::text FROM control_plane_configurations WHERE id = ?`, configID).Scan(ctx, &raw))
		var spec db.ControlPlaneConfigSpec
		require.NoError(t, json.Unmarshal([]byte(raw), &spec))
		assert.Equal(t, "10.0.0.10", spec.APIEndpoint(), "the controller reads the backfilled address")
		assert.True(t, spec.KubeVIPManaged(), "the backfilled cluster is managed as kube-vip")
	}

	// The backfill adds one key and leaves the rest of the stored config alone.
	var kubernetesVersion, reserved string
	require.NoError(t, bunDB.NewRaw(
		`SELECT config ->> 'kubernetes_version', config ->> 'system_reserved_memory' FROM control_plane_configurations WHERE id = ?`,
		legacyV2).Scan(ctx, &kubernetesVersion, &reserved))
	assert.Equal(t, "1.34.3", kubernetesVersion)
	assert.Equal(t, "2Gi", reserved)

	// down removes what the backfill wrote and nothing the API wrote.
	require.NoError(t, migration.Down(ctx, migrator, migration))
	assert.Empty(t, loadBalancerOf(legacyV1))
	assert.Empty(t, loadBalancerOf(legacyV2))
	assert.JSONEq(t, `{"kube_vip": {"vip": "10.0.0.99"}}`, loadBalancerOf(currentCfg), "an API-written load_balancer survives down")
	assert.JSONEq(t, `{"external": {"host": "10.0.0.30"}}`, loadBalancerOf(externalCfg))
}
