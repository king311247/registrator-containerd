package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/containerd/containerd"
	"log"
	"net/url"
	"os"
	"regexp"
	"registrator-containerd/pkg/ctrclient"
	"strconv"
	"strings"
	"sync"
)

var Hostname string

func init() {
	// It's ok for Hostname to ultimately be an empty string
	// An empty string will fall back to trying to make a best guess
	Hostname, _ = os.Hostname()
}

var serviceIDPattern = regexp.MustCompile(`^(.+?):([a-zA-Z0-9][a-zA-Z0-9_.-]+):[0-9]+(?::udp)?$`)

type Bridge struct {
	sync.Mutex
	registry       RegistryAdapter
	ctrClient      *containerd.Client
	services       map[string][]*Service
	deadContainers map[string]*DeadContainer
	containerList  map[string]*containerd.Container
	ctx            context.Context
	config         Config
	agentId        string
}

func New(ctrClient *containerd.Client, adapterUri string, config Config, ctx context.Context) (*Bridge, error) {
	uri, err := url.Parse(adapterUri)
	if err != nil {
		return nil, errors.New("bad adapter uri: " + adapterUri)
	}
	factory, found := AdapterFactories.Lookup(uri.Scheme)
	if !found {
		return nil, errors.New("unrecognized adapter: " + adapterUri)
	}
	log.Println("Using", uri.Scheme, "adapter:", uri)
	return &Bridge{
		ctrClient:      ctrClient,
		config:         config,
		registry:       factory.New(uri),
		services:       make(map[string][]*Service),
		deadContainers: make(map[string]*DeadContainer),
		ctx:            ctx,
	}, nil
}

func (b *Bridge) Ping() error {
	err := b.registry.Ping(b.agentId)
	if err != nil {
		return err
	}
	agentId, err := b.registry.RegisterAgentNode(b.config.DataCenterId, b.config.HostIp)
	if err != nil {
		return err
	}
	b.agentId = agentId

	return nil
}

func (b *Bridge) Add(containerId string) {
	b.Lock()
	defer b.Unlock()
	b.add(containerId, false)
}

func (b *Bridge) Remove(containerId string) {
	b.remove(containerId, true)
}

func (b *Bridge) RemoveOnExit(containerId string) {
	b.remove(containerId, b.shouldRemove(containerId))
}

func (b *Bridge) Refresh() {
	b.Lock()
	defer b.Unlock()

	for containerId, deadContainer := range b.deadContainers {
		deadContainer.TTL -= b.config.RefreshInterval
		if deadContainer.TTL <= 0 {
			delete(b.deadContainers, containerId)
		}
	}

	for containerId, services := range b.services {
		for _, service := range services {
			err := b.registry.Refresh(service)
			if err != nil {
				log.Println("refresh failed:", service.ID, err)
				continue
			}
			log.Println("refreshed:", containerId, service.ID)
		}
	}
}

func (b *Bridge) Sync(quiet bool) {
	b.Lock()
	defer b.Unlock()

	containerList, err := b.ctrClient.Containers(b.ctx)
	if err != nil && quiet {
		log.Println("error container containerList, skipping sync")
		return
	} else if err != nil && !quiet {
		log.Fatal(err)
	}

	for _, container := range containerList {
		services := b.services[container.ID()]

		if services == nil {
			b.add(container.ID(), quiet)
		} else {
			for _, service := range services {
				if err != nil {
					log.Println("sync register failed:", service, err)
				}

				err = b.registry.Register(service)
				if err != nil {
					log.Println("sync register failed:", service, err)
				}
			}
		}
	}

	if b.config.Cleanup {
		nonExitedContainers := make(map[string]containerd.Container)
		for _, container := range containerList {
			containerStatus, _ := ctrclient.GetContainerStatus(b.ctx, b.ctrClient, container.ID())
			if containerStatus.Status == containerd.Running {
				nonExitedContainers[container.ID()] = container
			}
		}

		for listingId, _ := range b.services {
			found := false
			for _, container := range nonExitedContainers {
				if listingId == container.ID() {
					found = true
					break
				}
			}
			// This is a container that does not exist
			if !found {
				log.Printf("stale: Removing service %s because it does not exist", listingId)
				go b.RemoveOnExit(listingId)
			}
		}

		log.Println("Cleaning up dangling services")
		extServices, err := b.registry.Services(b.agentId)
		if err != nil {
			log.Println("cleanup failed:", err)
			return
		}

	Outer:
		for _, extService := range extServices {
			matches := serviceIDPattern.FindStringSubmatch(extService.ID)
			if len(matches) != 3 {
				// There's no way this was registered by us, so leave it
				continue
			}
			serviceHostname := matches[1]

			if serviceHostname != Hostname {
				// ignore because registered on a different host
				continue
			}
			serviceContainerName := matches[2]
			for _, listing := range b.services {
				for _, service := range listing {
					if service.Name == extService.Name && serviceContainerName == service.Origin.container.Name {
						continue Outer
					}
				}
			}
			log.Println("dangling:", extService.ID)
			err := b.registry.Deregister(extService)
			if err != nil {
				log.Println("deregister failed:", extService.ID, err)
				continue
			}
			log.Println(extService.ID, "removed")
		}
	}
}

func (b *Bridge) add(containerId string, quiet bool) {
	if d := b.deadContainers[containerId]; d != nil {
		b.services[containerId] = d.Services
		delete(b.deadContainers, containerId)
	}

	if b.services[containerId] != nil {
		log.Println("container ", containerId, ", already exists, ignoring")
		return
	}

	k8SScheduleContainer, err := b.getK8SScheduleContainer(containerId)
	if err != nil {
		log.Println("get k8s schedule container failed:", containerId, err)
		return
	}

	if k8SScheduleContainer == nil {
		log.Println("container kind is sandbox or container not exist:", containerId)
		return
	}

	ports := make(map[string]ServicePort)
	b.extractK8SSchedulePorts(k8SScheduleContainer, ports)

	if len(ports) == 0 && !quiet {
		log.Println("ignored:", containerId, "no published ports")
		return
	}

	isGroup := len(ports) > 1
	for _, port := range ports {
		service := b.newService(port, isGroup)
		if service == nil {
			if !quiet {
				log.Println("ignored:", k8SScheduleContainer.ID, "service on port", port.ExposedPort)
			}
			continue
		}
		json2, _ := json.Marshal(service)
		log.Println("register service:", string(json2))

		err := b.registry.Register(service)
		if err != nil {
			log.Println("register failed:", service, err)
			continue
		}
		b.services[k8SScheduleContainer.ID] = append(b.services[k8SScheduleContainer.ID], service)
		log.Println("added:", k8SScheduleContainer.ID, service.ID)
	}
}

func (b *Bridge) getK8SScheduleContainer(containerId string) (*K8SScheduleContainer, error) {
	container, err := b.ctrClient.ContainerService().Get(b.ctx, containerId)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("get container info failed %s", containerId), err)
	}

	podId := container.Labels[ctrclient.PodUid]
	if podId == "" {
		return nil, nil
	}

	kind := container.Labels["io.cri-containerd.kind"]
	if kind != "container" {
		return nil, nil
	}

	containerMetaData := container.Extensions[ctrclient.K8sLabelsContainerMetadata]
	if containerMetaData == nil || containerMetaData.GetValue() == nil {
		return nil, nil
	}
	var containerMeta ctrclient.ContainerMetadata
	err = containerMeta.Unmarshal(containerMetaData)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("unmarshal ContainerMetadata error %s", container.ID), err)
	}

	sandboxId := containerMeta.Metadata.SandBoxID

	sandboxContainerInfo, err := b.ctrClient.ContainerService().Get(b.ctx, sandboxId)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("get container sandbox failed %s", sandboxId), err)
	}

	sandBoxMetaData := sandboxContainerInfo.Extensions[ctrclient.K8sLabelsSandboxMetadata]

	var sandboxMeta ctrclient.ContainerMetadata
	err = sandboxMeta.Unmarshal(sandBoxMetaData)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("unmarshal SandboxMetadata failed %s", sandboxId), err)
	}

	var containerSpec ctrclient.ContainerSpec
	err = containerSpec.Unmarshal(container.Spec)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("unmarshal containerSpec failed %s", container.ID), err)
	}

	var k8sContainer K8SScheduleContainer
	k8sContainer.ID = containerId
	k8sContainer.Container = &container
	k8sContainer.ContainerMetadata = &containerMeta
	k8sContainer.ContainerSpec = &containerSpec
	k8sContainer.SandBoxContainer = &sandboxContainerInfo
	k8sContainer.SandBoxMetadata = &sandboxMeta
	k8sContainer.Name = k8sContainer.ContainerMetadata.Metadata.Name

	return &k8sContainer, nil
}

func (b *Bridge) shouldRemove(containerId string) bool {
	if b.config.DeregisterCheck == "always" {
		return true
	}

	status, err := ctrclient.GetContainerStatus(b.ctx, b.ctrClient, containerId)
	if err != nil {
		return false
	}
	if status.Status == containerd.Running {
		return false
	}
	return true
}

func (b *Bridge) extractK8SSchedulePorts(k8SScheduleContainer *K8SScheduleContainer, ports map[string]ServicePort) {
	if k8SScheduleContainer == nil || k8SScheduleContainer.ContainerMetadata == nil || k8SScheduleContainer.SandBoxMetadata == nil {
		return
	}

	containerMeta := k8SScheduleContainer.ContainerMetadata
	sandboxMeta := k8SScheduleContainer.SandBoxMetadata
	containerSpec := k8SScheduleContainer.ContainerSpec

	ignoreEnv := k8SScheduleContainer.ContainerSpec.GetEnv("SERVICE_IGNORE")
	if ignoreEnv != "" {
		log.Println("ignored:", k8SScheduleContainer.ID, "ignore env set")
		return
	}

	portsJSON := containerMeta.Metadata.Config.Annotations["io.kubernetes.container.ports"]
	if len(portsJSON) <= 0 {
		return
	}

	portMappings, err := containerMeta.GetPortMapping()
	if err != nil {
		log.Println("Get container port mapping error", k8SScheduleContainer.ID, err)
		return
	}
	for _, portMapping := range portMappings {
		servicePort := ServicePort{
			ContainerID:       k8SScheduleContainer.ID,
			PortType:          strings.ToLower(portMapping.Protocol),
			ContainerName:     containerMeta.Metadata.Name,
			ExposedPort:       strconv.Itoa(portMapping.ContainerPort),
			ExposedIP:         sandboxMeta.Metadata.IP,
			ContainerHostname: containerSpec.GetEnv("HOSTNAME"),
			container:         k8SScheduleContainer,
		}

		ports[servicePort.ExposedPort] = servicePort
	}
}

func (b *Bridge) newService(port ServicePort, isGroup bool) *Service {
	container := port.container

	metadata, metadataFromPort := serviceMetaData(port.container, port.ExposedPort)

	ignore := mapDefault(metadata, "ignore", "")
	if ignore != "" {
		return nil
	}

	serviceName := mapDefault(metadata, "name", "")
	if serviceName == "" {
		log.Println("ignored:", container.ID, "service name not set")
		return nil
	}

	if isGroup && !metadataFromPort["name"] {
		serviceName += "-" + port.ExposedPort
	}

	p, err := strconv.Atoi(port.ExposedPort)
	if err != nil {
		log.Println("Parse ExposedPort port error", port.ExposedPort, err)
		return nil
	}

	nodeHostname := Hostname

	delete(metadata, "id")
	delete(metadata, "tags")
	delete(metadata, "name")

	service := new(Service)
	service.Origin = port
	service.ID = nodeHostname + ":" + container.Name + ":" + port.ExposedPort
	service.Name = serviceName
	service.Port = p
	service.IP = port.ExposedIP
	service.Attrs = metadata
	service.TTL = b.config.RefreshTtl

	if port.PortType == "udp" {
		service.Tags = combineTags(
			mapDefault(metadata, "tags", ""), b.config.ForceTags, "udp")
		service.ID = service.ID + ":udp"
	} else {
		service.Tags = combineTags(
			mapDefault(metadata, "tags", ""), b.config.ForceTags)
	}

	id := mapDefault(metadata, "id", "")
	if id != "" {
		service.ID = id
	}

	return service
}

func (b *Bridge) remove(containerId string, deregister bool) {
	b.Lock()
	defer b.Unlock()

	if deregister {
		deregisterAll := func(services []*Service) {
			for _, service := range services {
				err := b.registry.Deregister(service)
				if err != nil {
					log.Println("deregister failed:", service.ID, err)
					continue
				}
				log.Println("removed:", containerId, service.ID)
			}
		}
		deregisterAll(b.services[containerId])
		if d := b.deadContainers[containerId]; d != nil {
			deregisterAll(d.Services)
			delete(b.deadContainers, containerId)
		}
	} else if b.config.RefreshTtl != 0 && b.services[containerId] != nil {
		// need to stop the refreshing, but can't delete it yet
		b.deadContainers[containerId] = &DeadContainer{b.config.RefreshTtl, b.services[containerId]}
	}
	delete(b.services, containerId)
}

func serviceMetaData(container *K8SScheduleContainer, port string) (map[string]string, map[string]bool) {
	meta := container.ContainerMetadata.Metadata.Config.Envs
	//for k, v := range container.Labels {
	//	meta = append(meta, k+"="+v)
	//}
	metadata := make(map[string]string)
	metadataFromPort := make(map[string]bool)
	for _, kv := range meta {
		if strings.HasPrefix(kv.Key, "SERVICE_") {
			key := strings.ToLower(strings.TrimPrefix(kv.Key, "SERVICE_"))
			if metadataFromPort[key] {
				continue
			}
			portkey := strings.SplitN(key, "_", 2)
			_, err := strconv.Atoi(portkey[0])
			if err == nil && len(portkey) > 1 {
				if portkey[0] != port {
					continue
				}
				metadata[portkey[1]] = kv.Value
				metadataFromPort[portkey[1]] = true
			} else {
				metadata[key] = kv.Value
			}
		}
	}
	return metadata, metadataFromPort
}
