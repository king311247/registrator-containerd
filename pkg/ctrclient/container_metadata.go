package ctrclient

import (
	"encoding/json"
	"fmt"
	"github.com/containerd/typeurl/v2"
)

type ContainerMetadata struct {
	Version  string
	Metadata internalContainerMetadata
}

type internalContainerMetadata struct {
	ID        string
	Name      string
	SandBoxID string
	IP        string
	LogPath   string
	Config    metadataConfig
}

type metadataConfig struct {
	Metadata    map[string]string
	HostName    string
	Labels      map[string]string
	Annotations map[string]string
	Envs        []envMetadata
}

type envMetadata struct {
	Key   string
	Value string
}

type portMapping struct {
	Name          string
	HostPort      int
	ContainerPort int
	Protocol      string
}

func (m *ContainerMetadata) Unmarshal(metaData typeurl.Any) error {
	if metaData == nil || metaData.GetValue() == nil {
		return fmt.Errorf("metadata is nill ")
	}

	return json.Unmarshal(metaData.GetValue(), m)
}

func (m *ContainerMetadata) GetPortMapping() ([]portMapping, error) {
	var portMappings []portMapping
	if m.Metadata.Config.Annotations == nil {
		return portMappings, nil
	}
	portsJSON := m.Metadata.Config.Annotations["io.kubernetes.container.ports"]
	if len(portsJSON) <= 0 {
		return portMappings, nil
	}
	err := json.Unmarshal([]byte(portsJSON), &portMappings)
	if err != nil {
		return portMappings, err
	}
	return portMappings, nil
}
