/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	ibus "github.com/untillpro/airs-ibus"
	bus "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

var (
	// to check in tests
	busSrv    *bus.Service
	routerSrv Service

	routerPort             *int
	natsServers            *string
	routerWriteTimeout     *int
	routerReadTimeout      *int
	routerConnectionsLimit *int
	verbose                *bool
)

// called directly in tests only
func declare() {
	// fs := flag.NewFlagSet("", 1)
	natsServers = flag.String("ns", "nats://127.0.0.1:4222", "The nats server URLs (separated by comma)")
	routerPort = flag.Int("p", defaultRouterPort, "Server port")
	routerWriteTimeout = flag.Int("wt", defaultRouterWriteTimeout, "Write timeout in seconds")
	routerReadTimeout = flag.Int("rt", defaultRouterReadTimeout, "Read timeout in seconds")
	routerConnectionsLimit = flag.Int("cl", defaultRouterConnectionsLimit, "Limit of incoming connections")
	verbose = flag.Bool("v", false, "verbose, log raw NATS traffic")
	// if err := fs.Parse(os.Args); err != nil {
	// 	panic(err)
	// }
	flag.Parse()

	queueNumberOfPartitions["airs-bp"] = airsBPPartitionsAmount
	queueNamesJSON = []byte(`["airs-bp"]`)

	busSrv = &bus.Service{
		NATSServers:      *natsServers,
		Queues:           queueNumberOfPartitions,
		CurrentQueueName: currentQueueName, // not empty in tests only
		Verbose:          *verbose,
	}
	fmt.Println(os.Args)
	fmt.Println(busSrv)
	bus.Declare(busSrv)

	routerSrv = Service{
		Port:             *routerPort,
		WriteTimeout:     *routerWriteTimeout,
		ReadTimeout:      *routerReadTimeout,
		ConnectionsLimit: *routerConnectionsLimit,
	}
	godif.ProvideSliceElement(&services.Services, &routerSrv)
	godif.Require(&ibus.SendRequest2)
}

func main() {
	declare()
	if err := services.Run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
