/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"encoding/json"
	"flag"
	"os"

	ibus "github.com/untillpro/airs-ibus"
	bus "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

func ProvideIBusNATSSrv(cp CLIParams, queues QueuesPartitionsMap) *bus.Service {
	return &bus.Service{
		NATSServers:      cp.NATSServers,
		Queues:           queues,
		CurrentQueueName: currentQueueName, // not empty in tests only
		Verbose:          cp.Verbose,
	}
}

type QueuesPartitionsMap map[string]int

func DeclareEmbedded(routerSrv Service, queues QueuesPartitionsMap) {
	queueNumberOfPartitions = queues
	queuesNames := []string{}
	for name := range queues {
		queuesNames = append(queuesNames, name)
	}
	queueNamesJSON, _ = json.Marshal(&queuesNames) // error impossible
	godif.ProvideSliceElement(&services.Services, &routerSrv)
	godif.Require(&ibus.SendRequest2)
}

type CLIParams struct {
	NATSServers            string
	RouterPort             int
	RouterWriteTimeout     int
	RouterReadTimeout      int
	RouterConnectionsLimit int
	Verbose                bool
}

func ProvideCliParams() CLIParams {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	cp := CLIParams{}
	fs.StringVar(&cp.NATSServers, "ns", defaultNATSServer, "The nats server URLs (separated by comma)")
	fs.IntVar(&cp.RouterPort, "p", defaultRouterPort, "Server port")
	fs.IntVar(&cp.RouterWriteTimeout, "wt", defaultRouterWriteTimeout, "Write timeout in seconds")
	fs.IntVar(&cp.RouterReadTimeout, "rt", defaultRouterReadTimeout, "Read timeout in seconds")
	fs.IntVar(&cp.RouterConnectionsLimit, "cl", defaultRouterConnectionsLimit, "Limit of incoming connections")
	fs.BoolVar(&cp.Verbose, "v", false, "verbose, log raw NATS traffic")
	fs.Parse(os.Args[1:]) // os.Exit() on error
	return cp
}

func Declare() {

	queues := QueuesPartitionsMap{
		"airs-bp": airsBPPartitionsAmount,
	}

	CLIParams := ProvideCliParams()

	busSrv := ProvideIBusNATSSrv(CLIParams, queues)
	bus.Declare(busSrv)

	routerSrv := ProvideRouterSrv(CLIParams)

	DeclareEmbedded(routerSrv, queues)
}

func ProvideRouterSrv(cp CLIParams) Service {
	return Service{
		Port:             cp.RouterPort,
		WriteTimeout:     cp.RouterWriteTimeout,
		ReadTimeout:      cp.RouterReadTimeout,
		ConnectionsLimit: cp.RouterConnectionsLimit,
	}
}
