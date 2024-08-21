package ctrclient

import (
	"context"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
)

func NewCtrClient(ctx context.Context, namespace string, address string, opts ...containerd.ClientOpt) (*containerd.Client, context.Context, context.CancelFunc, error) {
	ctx = namespaces.WithNamespace(ctx, namespace)
	client, err := containerd.New(address, opts...)
	if err != nil {
		return nil, nil, nil, err
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithCancel(ctx)
	return client, ctx, cancel, nil
}

const (
	PodUid        = "io.kubernetes.pod.uid"
	PodNamespace  = "io.kubernetes.pod.namespace"
	PodName       = "io.kubernetes.pod.name"
	ContainerName = "io.kubernetes.container.name"

	K8sLabelsContainerMetadata = "io.cri-containerd.container.metadata"
	K8sLabelsSandboxMetadata   = "io.cri-containerd.sandbox.metadata"
	ContainerType              = "io.cri-containerd.kind"
)
