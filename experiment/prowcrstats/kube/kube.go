package kube

import (
	"context"
	"fmt"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
	"k8s.io/client-go/tools/clientcmd"
	ctrlruntimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	prowjobv1 "k8s.io/test-infra/prow/apis/prowjobs/v1"
)

func NewClients(configPath, clusterName string) (ctrlruntimeclient.Client, error) {
	var loader clientcmd.ClientConfigLoader
	if configPath != "" {
		loader = &clientcmd.ClientConfigLoadingRules{ExplicitPath: configPath}
	} else {
		loader = clientcmd.NewDefaultClientConfigLoadingRules()
	}

	overrides := clientcmd.ConfigOverrides{}
	// Override the cluster name if provided.
	if clusterName != "" {
		overrides.Context.Cluster = clusterName
		overrides.CurrentContext = clusterName
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loader, &overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed create rest config: %v", err)
	}
	return ctrlruntimeclient.New(cfg, ctrlruntimeclient.Options{})
}

func ListProwjobs(ctx context.Context, client ctrlruntimeclient.Client, namespace string) (*prowjobv1.ProwJobList, error) {
	pjs := &prowjobv1.ProwJobList{}
	err := client.List(ctx, pjs, &ctrlruntimeclient.ListOptions{
		Namespace: namespace,
	})
	return pjs, err
}
