// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package migrations

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/uptrace/bun"
)

const (
	// backfillLoadBalancerMigration identifies this migration in its logs.
	backfillLoadBalancerMigration = "20261124120000_backfill_load_balancer"
)

// Backfills load_balancer in the control-plane configuration of every cluster that
// has only the deprecated clusters.control_plane_vip, so the controller can read
// the API endpoint from load_balancer alone. A VIP there has always meant that NVKE
// runs kube-vip on it, so the backfilled value is kube_vip.
//
// Every configuration version of the cluster is updated, not only the current one,
// because a node reads the version it was created with. The VIP is fixed at cluster
// creation, so it is the same in all of them.
//
// The column is kept, and is still written for kube-vip clusters so an older
// controller keeps working after a rollback. Dropping it is a later release.
//
// Idempotent: a configuration that already has a load_balancer object is left alone.
//
//nolint:gochecknoinits // Bun registers migrations at package initialization.
func init() {
	migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		res, err := db.ExecContext(ctx, `
			UPDATE control_plane_configurations AS c
			SET config = jsonb_set(
				c.config,
				'{load_balancer}',
				jsonb_build_object('kube_vip', jsonb_build_object('vip', cl.control_plane_vip::text))
			)
			FROM clusters AS cl
			WHERE c.cluster_id = cl.id
				AND COALESCE(cl.control_plane_vip, '') <> ''
				AND COALESCE(jsonb_typeof(c.config -> 'load_balancer'), '') <> 'object'
		`)
		if err != nil {
			return fmt.Errorf("backfill load_balancer: %w", err)
		}
		rows, _ := res.RowsAffected()
		slog.Info("backfilled load_balancer from clusters.control_plane_vip",
			"migration", backfillLoadBalancerMigration, "configurations", rows)
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		// Removes only what the backfill wrote: a kube_vip with the cluster's column
		// as its address and nothing else. Any other load_balancer was written by the
		// API, and an older controller ignores it.
		if _, err := db.ExecContext(ctx, `
			UPDATE control_plane_configurations AS c
			SET config = c.config - 'load_balancer'
			FROM clusters AS cl
			WHERE c.cluster_id = cl.id
				AND COALESCE(cl.control_plane_vip, '') <> ''
				AND c.config -> 'load_balancer' =
					jsonb_build_object('kube_vip', jsonb_build_object('vip', cl.control_plane_vip::text))
		`); err != nil {
			return fmt.Errorf("remove backfilled load_balancer: %w", err)
		}
		return nil
	})
}
