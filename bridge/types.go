package bridge

import (
	"github.com/containerd/containerd/containers"
	"net/url"
	"registrator-containerd/pkg/ctrclient"
)

type AdapterFactory interface {
	New(uri *url.URL) RegistryAdapter
}

type RegistryAdapter interface {
	RegisterAgentNode(dataCenterId string, hostIp string) (string, error)
	Ping(agentId string) error
	Register(service *Service) error
	Deregister(service *Service) error
	Refresh(service *Service) error
	Services(agentId string) ([]*Service, error)
}

type Config struct {
	HostIp          string
	Internal        bool
	Explicit        bool
	UseIpFromLabel  string
	ForceTags       string
	RefreshTtl      int
	RefreshInterval int
	DeregisterCheck string
	Cleanup         bool
	DataCenterId    string
}

type Service struct {
	ID      string
	Name    string
	Port    int
	IP      string
	Tags    []string
	Attrs   map[string]string
	TTL     int
	AgentId string
	Origin  ServicePort
}

type ServicePort struct {
	HostPort          string
	HostIP            string
	ExposedPort       string
	ExposedIP         string
	PortType          string
	ContainerHostname string
	ContainerID       string
	ContainerName     string
	container         *K8SScheduleContainer
}

type DeadContainer struct {
	TTL      int
	Services []*Service
}

type K8SScheduleContainer struct {
	ID                string
	Name              string
	Container         *containers.Container
	ContainerMetadata *ctrclient.ContainerMetadata
	ContainerSpec     *ctrclient.ContainerSpec
	SandBoxContainer  *containers.Container
	SandBoxMetadata   *ctrclient.ContainerMetadata
}
