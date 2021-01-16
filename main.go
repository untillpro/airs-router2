/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package router

import (
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

// called directly in tests only
func declare() {
	fs := flag.NewFlagSet("", 1)
	var natsServers = fs.String("ns", "nats://127.0.0.1:4222", "The nats server URLs (separated by comma)")
	var routerPort = fs.Int("p", defaultRouterPort, "Server port")
	var routerWriteTimeout = fs.Int("wt", defaultRouterWriteTimeout, "Write timeout in seconds")
	var routerReadTimeout = fs.Int("rt", defaultRouterReadTimeout, "Read timeout in seconds")
	var routerConnectionsLimit = fs.Int("cl", defaultRouterConnectionsLimit, "Limit of incoming connections")
	var verbose = fs.Bool("v", false, "verbose, log raw NATS traffic")
	fs.Parse(os.Args)

	queueNumberOfPartitions["airs-bp"] = airsBPPartitionsAmount
	queueNamesJSON = []byte(`["airs-bp"]`)

	busSrv = &bus.Service{
		NATSServers:      *natsServers,
		Queues:           queueNumberOfPartitions,
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
