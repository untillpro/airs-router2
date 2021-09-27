/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"

	ibus "github.com/untillpro/airs-ibus"
	bus "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

var (
	// checked in tests
	busSrv    *bus.Service
	routerSrv Service
)

func DeclareEmbedded(routerSrv Service, queues map[string]int) {
	queueNumberOfPartitions = queues
	queuesNames := []string{}
	for name := range queues {
		queuesNames = append(queuesNames, name)
	}
	queueNamesJSON, _ = json.Marshal(&queuesNames) // error impossible
	godif.ProvideSliceElement(&services.Services, &routerSrv)
	godif.Require(&ibus.SendRequest2)
}

func declare() {
	fs := flag.NewFlagSet("", 1)
	var natsServers = fs.String("ns", defaultNATSServer, "The nats server URLs (separated by comma)")
	var routerPort = fs.Int("p", defaultRouterPort, "Server port")
	var routerWriteTimeout = fs.Int("wt", defaultRouterWriteTimeout, "Write timeout in seconds")
	var routerReadTimeout = fs.Int("rt", defaultRouterReadTimeout, "Read timeout in seconds")
	var routerConnectionsLimit = fs.Int("cl", defaultRouterConnectionsLimit, "Limit of incoming connections")
	var verbose = fs.Bool("v", false, "verbose, log raw NATS traffic")

	fs.Parse(os.Args[1:]) // os.Exit() on error

	queues := map[string]int{
		"airs-bp": airsBPPartitionsAmount,
	}

	busSrv = &bus.Service{
		NATSServers:      *natsServers,
		Queues:           queues,
		CurrentQueueName: currentQueueName, // not empty in tests only
		Verbose:          *verbose,
	}
	bus.Declare(busSrv)

	routerSrv = Service{
		Port:             *routerPort,
		WriteTimeout:     *routerWriteTimeout,
		ReadTimeout:      *routerReadTimeout,
		ConnectionsLimit: *routerConnectionsLimit,
	}
	DeclareEmbedded(routerSrv, queues)
}

func main() {
	declare()
	if err := services.Run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
