package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	events2 "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/events"
	"github.com/containerd/typeurl/v2"
	"github.com/gliderlabs/pkg/usage"
	"log"
	"os"
	"registrator-containerd/bridge"
	"registrator-containerd/pkg/ctrclient"
	"strings"
	"time"
	// Register grpc event types
	_ "github.com/containerd/containerd/api/events"
)

var Version string

var versionChecker = usage.NewChecker("registrator", Version)

var hostIp = flag.String("ip", "", "IP for ports mapped to the host")
var internal = flag.Bool("internal", false, "Use internal ports instead of published ones")
var explicit = flag.Bool("explicit", false, "Only register containers which have SERVICE_NAME label set")
var useIpFromLabel = flag.String("useIpFromLabel", "", "Use IP which is stored in a label assigned to the container")
var refreshInterval = flag.Int("ttl-refresh", 0, "Frequency with which service TTLs are refreshed")
var refreshTtl = flag.Int("ttl", 0, "TTL for services (default is no expiry)")
var forceTags = flag.String("tags", "", "Append tags for all registered services")
var resyncInterval = flag.Int("resync", 0, "Frequency with which services are resynchronized")
var deregister = flag.String("deregister", "always", "Deregister exited services \"always\" or \"on-success\"")
var retryAttempts = flag.Int("retry-attempts", 0, "Max retry attempts to establish a connection with the backend. Use -1 for infinite retries")
var retryInterval = flag.Int("retry-interval", 2000, "Interval (in millisecond) between retry-attempts.")
var cleanup = flag.Bool("cleanup", false, "Remove dangling services")
var dataCenterId = flag.String("data-center-id", "", "data center id")

func assert(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		versionChecker.PrintVersion()
		os.Exit(0)
	}
	log.Printf("Starting registrator %s ...", Version)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [options] <registry URI>\n\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() != 1 {
		if flag.NArg() == 0 {
			fmt.Fprint(os.Stderr, "Missing required argument for registry URI.\n\n")
		} else {
			fmt.Fprintln(os.Stderr, "Extra unparsed arguments:")
			fmt.Fprintln(os.Stderr, " ", strings.Join(flag.Args()[1:], " "))
			fmt.Fprint(os.Stderr, "Options should come before the registry URI argument.\n\n")
		}
		flag.Usage()
		os.Exit(2)
	}

	if *hostIp != "" {
		log.Println("Forcing host IP to", *hostIp)
	}

	if (*refreshTtl == 0 && *refreshInterval > 0) || (*refreshTtl > 0 && *refreshInterval == 0) {
		assert(errors.New("-ttl and -ttl-refresh must be specified together or not at all"))
	} else if *refreshTtl > 0 && *refreshTtl <= *refreshInterval {
		assert(errors.New("-ttl must be greater than -ttl-refresh"))
	}

	if *retryInterval <= 0 {
		assert(errors.New("-retry-interval must be greater than 0"))
	}

	containerDHost := os.Getenv("CONTAINERD_HOST")
	if containerDHost == "" {
		containerDHost = "/run/containerd/containerd.sock"
		os.Setenv("CONTAINERD_HOST", containerDHost)
	}

	ctx := context.Background()

	ctrClient, clientCtx, cancel, err := ctrclient.NewCtrClient(ctx, "k8s.io", containerDHost)
	if err != nil {
		assert(err)
	}
	defer cancel()

	b, err := bridge.New(ctrClient, flag.Arg(0), bridge.Config{
		HostIp:          *hostIp,
		Internal:        *internal,
		Explicit:        *explicit,
		UseIpFromLabel:  *useIpFromLabel,
		ForceTags:       *forceTags,
		RefreshTtl:      *refreshTtl,
		RefreshInterval: *refreshInterval,
		DeregisterCheck: *deregister,
		Cleanup:         *cleanup,
		DataCenterId:    *dataCenterId,
	}, clientCtx)
	assert(err)

	attempt := 0
	for *retryAttempts == -1 || attempt <= *retryAttempts {
		log.Printf("Connecting to backend (%v/%v)", attempt, *retryAttempts)

		err = b.Ping()
		if err == nil {
			break
		}

		if err != nil && attempt == *retryAttempts {
			assert(err)
		}

		time.Sleep(time.Duration(*retryInterval) * time.Millisecond)
		attempt++
	}

	// Start event listener before listing containers to avoid missing anything
	eventService := ctrClient.EventService()
	eventsCh, errCh := eventService.Subscribe(clientCtx)
	if err != nil {
		assert(err)
	}

	b.Sync(false)

	quit := make(chan struct{})

	// Start the TTL refresh timer
	if *refreshInterval > 0 {
		ticker := time.NewTicker(time.Duration(*refreshInterval) * time.Second)
		go func() {
			for {
				select {
				case <-ticker.C:
					b.Refresh()
				case <-quit:
					ticker.Stop()
					return
				}
			}
		}()
	}

	// Start the resync timer if enabled
	if *resyncInterval > 0 {
		resyncTicker := time.NewTicker(time.Duration(*resyncInterval) * time.Second)
		go func() {
			for {
				select {
				case <-resyncTicker.C:
					b.Sync(true)
				case <-quit:
					resyncTicker.Stop()
					return
				}
			}
		}()
	}

	containerTaskStartHandle := func(eventData typeurl.Any) {
		containerTaskEvent := events2.TaskStart{}
		err := typeurl.UnmarshalTo(eventData, &containerTaskEvent)

		if err != nil {
			fmt.Println("containerTaskStartHandle error", err)

		} else {
			fmt.Println("task start", containerTaskEvent.ContainerID)
			b.Add(containerTaskEvent.ContainerID)
		}
	}

	containerTaskDeleteHandle := func(eventData typeurl.Any) {
		containerTaskEvent := events2.TaskDelete{}
		err := typeurl.UnmarshalTo(eventData, &containerTaskEvent)

		if err != nil {
			fmt.Println("containerTaskDeleteHandle error", err)
		} else {
			fmt.Println("task delete", containerTaskEvent.ContainerID)
			b.Remove(containerTaskEvent.ContainerID)
		}
	}

	otherEventHandler := func(e *events.Envelope) {
		v, err := typeurl.UnmarshalAny(e.Event)

		if err != nil {
			log.Println("cannot unmarshal an event from Any", err)
			return
		}

		out, err := json.Marshal(v)
		if err != nil {
			log.Println("cannot marshal Any into JSON", err)
			return
		}

		if _, err := fmt.Println(
			e.Timestamp,
			e.Namespace,
			e.Topic,
			string(out),
		); err != nil {
			fmt.Println(err)
		}
	}

	for {
		var e *events.Envelope
		select {
		case e = <-eventsCh:
		case err := <-errCh:
			log.Println("watch event error", err)
		}
		if e == nil || e.Event == nil {
			continue
		}
		switch e.Topic {
		case "/tasks/start":
			go containerTaskStartHandle(e.Event)
		case "/tasks/delete":
			go containerTaskDeleteHandle(e.Event)
		default:
			go otherEventHandler(e)
		}
	}
	close(quit)
	log.Fatal("Containerd event loop closed")
}
