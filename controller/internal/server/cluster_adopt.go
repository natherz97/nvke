// SPDX-FileCopyrightText: Copyright (c) 2026 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/nvke/controller/api"
	"github.com/dsx-ai-factory/nvke/controller/internal/db"
	"github.com/dsx-ai-factory/nvke/controller/internal/kube"
)

const (
	// controlPlaneRoleLabel marks a kubeadm control-plane Node.
	controlPlaneRoleLabel = "node-role.kubernetes.io/control-plane"
	// lookupNodesTimeout bounds the reads of the adopted cluster.
	lookupNodesTimeout = 30 * time.Second
	// adoptionKubeconfigKey is the credentials Secret field holding the site kubeconfig.
	adoptionKubeconfigKey = "kubeconfig"
	// kubeadmConfigMapName holds the ClusterConfiguration kubeadm upgrades from.
	kubeadmConfigMapName = "kubeadm-config"
)

// adoptedNode is a control-plane node found in the cluster being adopted.
type adoptedNode struct {
	Name string
	IP   string
	// KubeletVersion is the node's reported kubelet version, without a leading "v".
	KubeletVersion string
}

// adoptedCluster is what the cluster being adopted reports about itself.
type adoptedCluster struct {
	// kubernetesVersion, podSubnet and serviceSubnet come from kubeadm-config.
	kubernetesVersion string
	podSubnet         string
	serviceSubnet     string
}

// adoption is what AdoptCluster read from the credentials Secret and the cluster.
type adoption struct {
	nodes []adoptedNode
	live  adoptedCluster
}

// AdoptCluster records the cluster the controller runs on, without running
// kubeadm init. Its operator keeps managing the control plane: NVKE never
// upgrades, replaces or deletes an adopted control-plane node. It shares
// createCluster with CreateCluster; the differences are the credentials Secret,
// the checks against the running cluster, the adopted node rows, and the
// control-plane build ProvisionCluster skips.
func (s *clusterService) AdoptCluster(ctx context.Context, req api.AdoptClusterRequestObject) (api.AdoptClusterResponseObject, error) {
	if !s.adoptionEnabled {
		return api.AdoptCluster404JSONResponse{Message: "cluster adoption is not enabled on this controller"}, nil
	}
	adopt, err := s.readAdoption(ctx, &req.Body.Cluster, req.Body.Adoption)
	var created *db.Cluster
	if err == nil {
		created, err = s.createCluster(ctx, &req.Body.Cluster, adopt)
	}
	var badRequestErr *badRequestError
	if errors.As(err, &badRequestErr) {
		return api.AdoptCluster400JSONResponse{Message: badRequestErr.Error()}, nil
	}
	if err != nil {
		return nil, err
	}
	s.logger.Info("adopted cluster", "clusterID", created.ID, "clusterName", created.Name, "controlPlaneNodes", len(adopt.nodes))
	return api.AdoptCluster201JSONResponse(APIClusterFromDB(created)), nil
}

// readAdoption reads the credentials Secret, and reads the listed control-plane
// nodes and the cluster's kubeadm configuration with its kubeconfig. A request
// that does not fit the running cluster is a *badRequestError.
func (s *clusterService) readAdoption(ctx context.Context, body *api.CreateClusterRequest, spec api.ClusterAdoption) (*adoption, error) {
	if len(spec.Nodes) == 0 {
		return nil, newBadRequestErrorf("adoption.nodes must name at least one control-plane node")
	}
	// A gap makes scale-up build control-plane nodes through the provider.
	if body.ControlPlaneConfig == nil || body.ControlPlaneConfig.DesiredCount != len(spec.Nodes) {
		return nil, newBadRequestErrorf("cluster.controlPlaneConfig.desiredCount must equal the number of adoption.nodes (%d)", len(spec.Nodes))
	}
	if s.k8sClient == nil {
		return nil, errors.New("adopting a cluster needs a Kubernetes client")
	}

	credentials, err := s.readSecret(ctx, spec.CredentialsSecretRef)
	if err != nil {
		return nil, err
	}
	kubeconfig := credentials[adoptionKubeconfigKey]
	if len(kubeconfig) == 0 {
		return nil, newBadRequestErrorf("adoption.credentialsSecretRef Secret has no %q", adoptionKubeconfigKey)
	}

	// ProvisionCluster force-applies the Cluster API scaffold under the cluster's
	// name. Over an existing Cluster (one moved here by a pivot), that would take
	// over its infrastructure and unpause it, so Cluster API could act on the
	// control plane again.
	existing := &clusterv1.Cluster{}
	if err := s.k8sClient.Get(ctx, ctrlclient.ObjectKey{Namespace: s.managementNamespace, Name: body.Name}, existing); err == nil {
		return nil, newBadRequestErrorf("a Cluster API Cluster %s/%s already exists; adopt a cluster that no Cluster API object manages", s.managementNamespace, body.Name)
	} else if !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("checking for Cluster API Cluster %s/%s: %w", s.managementNamespace, body.Name, err)
	}

	newClient := s.adoptedClusterClient
	if newClient == nil {
		newClient = func(kubeconfig []byte, clusterName string) (ctrlclient.Client, error) {
			return kube.NewClientFromKubeconfig(kubeconfig, clusterName)
		}
	}
	client, err := newClient(kubeconfig, body.Name)
	if err != nil {
		return nil, newBadRequestErrorf("kubeconfig in the credentials Secret: %v", err)
	}
	ctx, cancel := context.WithTimeout(ctx, lookupNodesTimeout)
	defer cancel()
	nodes, err := lookupControlPlaneNodes(ctx, client, spec.Nodes)
	if err != nil {
		return nil, err
	}
	live, err := readAdoptedCluster(ctx, client)
	if err != nil {
		return nil, err
	}
	return &adoption{nodes: nodes, live: live}, nil
}

func (s *clusterService) readSecret(ctx context.Context, ref api.SecretReference) (map[string][]byte, error) {
	namespace := ref.Namespace
	if namespace == "" {
		namespace = s.podNamespace
	}
	secret := &corev1.Secret{}
	if err := s.k8sClient.Get(ctx, ctrlclient.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, newBadRequestErrorf("Secret %s/%s not found", namespace, ref.Name)
		}
		return nil, fmt.Errorf("reading Secret %s/%s: %w", namespace, ref.Name, err)
	}
	return secret.Data, nil
}

// lookupControlPlaneNodes requires names to be exactly the cluster's
// control-plane Nodes, each with an InternalIP: NVKE reaches a node's agent by
// IP. Listing every one keeps desiredCount equal to the control-plane nodes on
// record. A client error is a bad request too: the kubeconfig is the operator's input.
func lookupControlPlaneNodes(ctx context.Context, client ctrlclient.Client, names []string) ([]adoptedNode, error) {
	var controlPlane corev1.NodeList
	if err := client.List(ctx, &controlPlane, ctrlclient.HasLabels{controlPlaneRoleLabel}); err != nil {
		return nil, newBadRequestErrorf("listing nodes with the credentials Secret's kubeconfig: %v", err)
	}
	byName := map[string]corev1.Node{}
	for _, n := range controlPlane.Items {
		byName[n.Name] = n
	}
	nodes := make([]adoptedNode, 0, len(names))
	for _, name := range names {
		n, ok := byName[name]
		if !ok {
			return nil, newBadRequestErrorf("adoption.nodes: node %s is not a control-plane node of the cluster", name)
		}
		ip := ""
		for _, addr := range n.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				ip = addr.Address
				break
			}
		}
		if ip == "" {
			return nil, newBadRequestErrorf("adoption.nodes: node %s has no InternalIP", name)
		}
		nodes = append(nodes, adoptedNode{Name: name, IP: ip, KubeletVersion: strings.TrimPrefix(n.Status.NodeInfo.KubeletVersion, "v")})
	}
	for name := range byName {
		if !slices.Contains(names, name) {
			return nil, newBadRequestErrorf("adoption.nodes must list every control-plane node; %s is missing", name)
		}
	}
	return nodes, nil
}

// readAdoptedCluster reads the version and networks kubeadm upgrades the cluster
// with.
func readAdoptedCluster(ctx context.Context, client ctrlclient.Client) (adoptedCluster, error) {
	var cm corev1.ConfigMap
	if err := client.Get(ctx, ctrlclient.ObjectKey{Namespace: metav1.NamespaceSystem, Name: kubeadmConfigMapName}, &cm); err != nil {
		return adoptedCluster{}, newBadRequestErrorf("reading kube-system/%s with the credentials Secret's kubeconfig: %v", kubeadmConfigMapName, err)
	}
	var cfg struct {
		KubernetesVersion string `json:"kubernetesVersion"`
		Networking        struct {
			PodSubnet     string `json:"podSubnet"`
			ServiceSubnet string `json:"serviceSubnet"`
		} `json:"networking"`
	}
	if err := yaml.Unmarshal([]byte(cm.Data["ClusterConfiguration"]), &cfg); err != nil {
		return adoptedCluster{}, newBadRequestErrorf("parsing kube-system/%s ClusterConfiguration: %v", kubeadmConfigMapName, err)
	}
	return adoptedCluster{
		kubernetesVersion: strings.TrimPrefix(cfg.KubernetesVersion, "v"),
		podSubnet:         cfg.Networking.PodSubnet,
		serviceSubnet:     cfg.Networking.ServiceSubnet,
	}, nil
}

// mismatch returns a *badRequestError saying why the cluster record would not
// describe the running cluster, or nil. Nodes NVKE adds install exactly the version the
// record names, so a record that differs builds nodes that do not fit.
func (a *adoption) mismatch(cluster db.Cluster) error {
	for _, n := range a.nodes {
		if n.KubeletVersion != cluster.KubernetesVersion {
			return newBadRequestErrorf("cluster.controlPlaneKubernetesVersion is %s, but node %s runs kubelet %s", cluster.KubernetesVersion, n.Name, n.KubeletVersion)
		}
	}
	if a.live.kubernetesVersion != cluster.KubernetesVersion {
		return newBadRequestErrorf("cluster.controlPlaneKubernetesVersion is %s, but kubeadm-config says %s", cluster.KubernetesVersion, a.live.kubernetesVersion)
	}
	if !sameCIDRs(a.live.podSubnet, cluster.PodCIDR) {
		return newBadRequestErrorf("cluster.network.podCIDR is %s, but the cluster's pod subnet is %s", cluster.PodCIDR, a.live.podSubnet)
	}
	if !sameCIDRs(a.live.serviceSubnet, cluster.ServiceCIDR) {
		return newBadRequestErrorf("cluster.network.serviceCIDR is %s, but the cluster's service subnet is %s", cluster.ServiceCIDR, a.live.serviceSubnet)
	}
	return nil
}

// sameCIDRs compares comma-separated CIDR lists as sets of canonical prefixes.
func sameCIDRs(a, b string) bool {
	canon := func(s string) []string {
		var out []string
		for _, part := range strings.Split(s, ",") {
			p, err := netip.ParsePrefix(strings.TrimSpace(part))
			if err != nil {
				return []string{s}
			}
			out = append(out, p.Masked().String())
		}
		slices.Sort(out)
		return out
	}
	return slices.Equal(canon(a), canon(b))
}

// createAdoptedNodes records the listed control-plane nodes as adopted. They count
// toward desiredCount, and NVKE never changes them: the operator keeps managing
// the control plane. NVKE only reads through their agents (the cluster's
// kubeconfig and kubeadm join strings), which answer once the operator moves each
// node's SPIRE agent to this site and registers host/<node ID>.
func (s *clusterService) createAdoptedNodes(ctx context.Context, tx db.Tx, cluster db.Cluster, adopted []adoptedNode) error {
	for _, n := range adopted {
		node := db.Node{
			ID:                          uuid.New(),
			ClusterID:                   cluster.ID,
			ControlPlane:                true,
			ControlPlaneConfigurationID: cluster.ControlPlaneConfig.ConfigurationID,
			Name:                        n.Name,
			IP:                          n.IP,
			Conditions: db.NodeConditions{
				Pending:         db.NewCondition(false, "Adopted"),
				Provisioning:    db.NewCondition(false, "Adopted"),
				ReadyToJoin:     db.NewCondition(true, "Adopted"),
				Joined:          db.NewCondition(true, "Adopted: joined before NVKE managed the cluster"),
				Provisioned:     db.NewCondition(true, "Adopted"),
				Deleting:        db.NewCondition(false, "Node is not being deleted"),
				KubernetesReady: db.NewCondition(true, "Adopted"),
				NodeAgentReady:  db.NewCondition(false, "Adopted: waiting for the node agent"),
				Adopted:         db.NewCondition(true, "Ran before NVKE adopted the cluster"),
			},
			// The settings every node records at creation, so the agent is reached the same way.
			SpiffeEnabled:                s.nodeAgentProfile.SpiffeEnabled,
			SpireAttestationMethod:       s.nodeAgentProfile.SpireAttestationMethod,
			NodeAgentExternalMTLSEnabled: s.nodeAgentProfile.NodeAgentExternalMTLSEnabled,
			NodeAgentTLSJWTEnabled:       s.nodeAgentProfile.NodeAgentTLSJWTEnabled,
		}
		if _, err := tx.CreateNode(ctx, &node); err != nil {
			return fmt.Errorf("recording adopted node %s: %w", n.Name, err)
		}
	}
	return nil
}
