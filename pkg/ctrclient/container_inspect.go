package ctrclient

import (
	"context"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/api/services/tasks/v1"
	"github.com/containerd/errdefs"
	"strings"
)

func GetContainerStatus(ctx context.Context, client *containerd.Client, containerId string) (containerd.Status, error) {
	response, err := client.TaskService().Get(ctx, &tasks.GetRequest{
		ContainerID: containerId,
	})

	if err != nil {
		return containerd.Status{}, errdefs.FromGRPC(err)
	}
	return containerd.Status{
		Status:     containerd.ProcessStatus(strings.ToLower(response.Process.Status.String())),
		ExitStatus: response.Process.ExitStatus,
	}, nil
}
