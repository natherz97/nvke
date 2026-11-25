// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"net/url"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// validateLoadBalancerKubeVIP checks a cluster created with
// controlPlaneConfig.loadBalancer.kubeVIP.vip, the replacement for the
// deprecated top-level controlPlaneVIP. The create request is stored as part of
// the control-plane config, and ConfigureKubeVip reads it back, so this covers
// the path a unit test with a mock database does not.
func validateLoadBalancerKubeVIP(test *tenantTest) {
	It("reports the endpoint as loadBalancer.kubeVIP and the legacy controlPlaneVIP", func(ctx context.Context) {
		wantVIP := test.spec.ControlPlane.ControlPlaneVIP
		Expect(wantVIP).NotTo(BeEmpty(), "the harness should have allocated a control-plane VIP")

		By("Reading the cluster back from the API")
		cluster := expectSucceed(test.nvke.GetCluster(ctx, test.cluster.ID))
		Expect(cluster.ControlPlaneVIP).NotTo(BeNil(), "the stored endpoint is still reported as controlPlaneVIP")
		Expect(*cluster.ControlPlaneVIP).To(Equal(wantVIP))

		controlPlane := expectSucceed(test.nvke.GetClusterControlPlane(ctx, test.cluster.ID))
		loadBalancer := controlPlane.Config.LoadBalancer
		Expect(loadBalancer).NotTo(BeNil(), "the control-plane config should report the load balancer it was created with")
		Expect(loadBalancer.KubeVIP).NotTo(BeNil())
		Expect(loadBalancer.KubeVIP.Vip).To(Equal(wantVIP))
		Expect(loadBalancer.External).To(BeNil())
		Expect(loadBalancer.FirstMachineIP).To(BeNil())
	}, Label("lifecycle", "cp-controller", "load-balancer"), NodeTimeout(5*time.Minute))

	It("runs kube-vip on the control-plane node", func(ctx context.Context) {
		tenantClient := expectSucceed(test.tenantClients.GetAdminClient(ctx, test.cluster.Name))

		Eventually(func(ctx context.Context) ([]corev1.Pod, error) {
			var pods corev1.PodList
			err := tenantClient.List(
				ctx,
				&pods,
				client.InNamespace(kubeSystemNamespace),
				client.MatchingLabels{"component": "kube-vip"},
			)
			return pods.Items, err
		}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(HaveLen(1),
			"NVKE should configure kube-vip for a loadBalancer.kubeVIP cluster")
	}, Label("lifecycle", "cp-controller", "load-balancer"), NodeTimeout(5*time.Minute))

	It("publishes a kubeconfig that reaches the API server through the VIP", func(ctx context.Context) {
		vip := test.spec.ControlPlane.ControlPlaneVIP
		restConfig := expectSucceed(test.tenantClients.AdminRESTConfig(ctx, test.cluster.Name))

		// The controller publishes the kubeconfig from a node's admin.conf, whose
		// server is the control-plane endpoint. With an endpoint relay the suite
		// replaces Host with a local address and moves the published hostname to
		// ServerName, so accept the VIP in either place.
		published := expectSucceed(url.Parse(restConfig.Host))
		Expect([]string{published.Hostname(), restConfig.ServerName}).To(ContainElement(vip),
			"the published kubeconfig should point at the VIP")

		By("Calling the API server through that endpoint")
		tenantClient := expectSucceed(test.tenantClients.GetAdminClient(ctx, test.cluster.Name))
		Eventually(func(ctx context.Context) error {
			var namespace corev1.Namespace
			return tenantClient.Get(ctx, client.ObjectKey{Name: kubeSystemNamespace}, &namespace)
		}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(5*time.Second).Should(Succeed(),
			"an API call through the VIP endpoint should succeed")
	}, Label("lifecycle", "cp-controller", "load-balancer"), NodeTimeout(5*time.Minute))

	It("advertises the VIP route to the BGP peer from the control-plane node", func(ctx context.Context) {
		bgpContainer := envOrDefault("E2E_BGP_CONTAINER_NAME", "bgp-containers")
		if err := runDockerCommand(ctx, "inspect", bgpContainer); err != nil {
			Skip(fmt.Sprintf("BGP container %q is not available in this environment: %v", bgpContainer, err))
		}

		tenantClient := expectSucceed(test.tenantClients.GetAdminClient(ctx, test.cluster.Name))
		targets := expectSucceed(controlPlaneRouteTargets(ctx, tenantClient))
		Expect(targets).To(HaveLen(1), "this entry creates a single control-plane node")

		vip := test.spec.ControlPlane.ControlPlaneVIP
		Eventually(func(ctx context.Context) ([]string, error) {
			return bgpNextHops(ctx, bgpContainer, vip)
		}).WithContext(ctx).WithTimeout(2*time.Minute).WithPolling(2*time.Second).
			Should(ConsistOf(targets[0].ip), "the node should advertise the VIP to the BGP peer")
	}, Label("lifecycle", "cp-controller", "load-balancer"), NodeTimeout(5*time.Minute))
}
