package ctrclient

import (
	"encoding/json"
	"fmt"
	"github.com/containerd/typeurl/v2"
	"strings"
)

type ContainerSpec struct {
	OciVersion  string
	Process     Process
	Annotations map[string]string
}

type Process struct {
	Env  []string
	Args []string
}

func (spec *ContainerSpec) Unmarshal(specData typeurl.Any) error {
	if specData == nil || specData.GetValue() == nil {
		return fmt.Errorf("specdata is nill ")
	}

	return json.Unmarshal(specData.GetValue(), spec)
}

func (spec *ContainerSpec) GetEnv(envKey string) string {
	if spec.Process.Env == nil {
		return ""
	}

	for _, envData := range spec.Process.Env {
		idx := strings.Index(envData, "=")
		if idx < 0 {
			continue
		}
		key := envData[:idx]
		if key == envKey {
			return envData[idx+1:]
		}
	}

	return ""
}
