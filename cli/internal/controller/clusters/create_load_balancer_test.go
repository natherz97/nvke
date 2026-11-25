// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package clusters

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// loadBalancerCreateSpec returns a create-cluster spec whose
// controlPlaneConfig.loadBalancer is the given block, indented to sit under
// "loadBalancer:".
func loadBalancerCreateSpec(block string) string {
	return `
name: my-cluster
controlPlaneKubernetesVersion: "1.30.0"
cniPlugin: calico
etcdEncrypted: false
controlPlaneConfig:
  desiredCount: 3
  loadBalancer:
` + block
}

func TestCreateClusterCarriesTheLoadBalancer(t *testing.T) {
	tests := []struct {
		name  string
		block string
		want  map[string]any
	}{
		{
			name:  "external",
			block: "    external:\n      host: 10.0.0.101\n",
			want:  map[string]any{"external": map[string]any{"host": "10.0.0.101"}},
		},
		{
			name:  "kubeVIP",
			block: "    kubeVIP:\n      vip: 10.0.0.102\n",
			want:  map[string]any{"kubeVIP": map[string]any{"vip": "10.0.0.102"}},
		},
		{
			name:  "firstMachineIP",
			block: "    firstMachineIP: {}\n",
			want:  map[string]any{"firstMachineIP": map[string]any{}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake, clientFn := newFakeCWE(t)

			cmd := NewCreateCmd(clientFn)
			cmd.SetIn(strings.NewReader(loadBalancerCreateSpec(tc.block)))
			if _, err := run(t, cmd, "-f", "-"); err != nil {
				t.Fatalf("create cluster: %v", err)
			}
			if seen := fake.seen(); len(seen) != 1 || seen[0] != "POST /api/clusters" {
				t.Fatalf("requests = %v, want one POST /api/clusters", seen)
			}

			// Assert the wire body: the fake keeps only a few cluster fields.
			var body map[string]any
			if err := json.Unmarshal(fake.lastRequestBody(), &body); err != nil {
				t.Fatalf("decoding the create body: %v", err)
			}
			config, ok := body["controlPlaneConfig"].(map[string]any)
			if !ok {
				t.Fatalf("controlPlaneConfig missing from the body: %v", body)
			}
			if got := config["loadBalancer"]; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("controlPlaneConfig.loadBalancer = %v, want %v", got, tc.want)
			}
			if got, present := body["controlPlaneVIP"]; present {
				t.Errorf("the body carries controlPlaneVIP %v, which the spec did not set", got)
			}
		})
	}
}

// TestCreateClusterStillCarriesTheDeprecatedControlPlaneVIP checks that a spec
// written before loadBalancer existed posts the same body as it always did.
func TestCreateClusterStillCarriesTheDeprecatedControlPlaneVIP(t *testing.T) {
	fake, clientFn := newFakeCWE(t)

	cmd := NewCreateCmd(clientFn)
	cmd.SetIn(strings.NewReader(`
name: my-cluster
controlPlaneKubernetesVersion: "1.30.0"
cniPlugin: calico
etcdEncrypted: false
controlPlaneVIP: 10.0.0.100
controlPlaneConfig:
  desiredCount: 3
`))
	if _, err := run(t, cmd, "-f", "-"); err != nil {
		t.Fatalf("create cluster: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal(fake.lastRequestBody(), &body); err != nil {
		t.Fatalf("decoding the create body: %v", err)
	}
	if body["controlPlaneVIP"] != "10.0.0.100" {
		t.Errorf(`body["controlPlaneVIP"] = %v, want 10.0.0.100`, body["controlPlaneVIP"])
	}
	config, ok := body["controlPlaneConfig"].(map[string]any)
	if !ok {
		t.Fatalf("controlPlaneConfig missing from the body: %v", body)
	}
	if _, present := config["loadBalancer"]; present {
		t.Errorf("controlPlaneConfig carries a loadBalancer the spec did not set: %v", config["loadBalancer"])
	}
}

// TestCreateClusterRejectsUnknownLoadBalancerKeys checks that a misspelled key
// inside the loadBalancer fails before any request is sent, as other unknown
// fields do.
func TestCreateClusterRejectsUnknownLoadBalancerKeys(t *testing.T) {
	tests := []struct {
		name    string
		block   string
		wantKey string
	}{
		{
			name:    "under external",
			block:   "    external:\n      host: 10.0.0.101\n      port: 6443\n",
			wantKey: "port",
		},
		{
			name:    "under kubeVIP",
			block:   "    kubeVIP:\n      vip: 10.0.0.102\n      vipp: 10.0.0.103\n",
			wantKey: "vipp",
		},
		{
			name:    "an unknown option",
			block:   "    typo:\n      host: 10.0.0.101\n",
			wantKey: "typo",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake, clientFn := newFakeCWE(t)

			cmd := NewCreateCmd(clientFn)
			cmd.SetIn(strings.NewReader(loadBalancerCreateSpec(tc.block)))
			_, err := run(t, cmd, "-f", "-")
			if err == nil {
				t.Fatal("create cluster accepted an unknown loadBalancer key; want error")
			}
			if !strings.Contains(err.Error(), tc.wantKey) {
				t.Errorf("error %q does not name the unknown key %q", err, tc.wantKey)
			}
			if seen := fake.seen(); len(seen) != 0 {
				t.Errorf("requests = %v, want none: the spec must fail before reaching the API", seen)
			}
		})
	}
}
