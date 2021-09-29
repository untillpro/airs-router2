/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"encoding/json"
	"flag"
	"os"

	ibus "github.com/untillpro/airs-ibus"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

func DeclareEmbeddedRouter(routerSrv Service, queues ibusnats.QueuesPartitionsMap) {
	queueNumberOfPartitions = queues
	queuesNames := []string{}
	for name := range queues {
		queuesNames = append(queuesNames, name)
	}
	queueNamesJSON, _ = json.Marshal(&queuesNames) // error impossible
	godif.ProvideSliceElement(&services.Services, &routerSrv)
	godif.Require(&ibus.SendRequest2)
}

type RouterParams struct {
	NATSServers            ibusnats.NATSServers
	RouterPort             int
	RouterWriteTimeout     int
	RouterReadTimeout      int
	RouterConnectionsLimit int
	Verbose                bool
}

func ProvideRouterParamsFromCmdLine() RouterParams {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	cp := RouterParams{}
	fs.Var(&cp.NATSServers, "ns", "The nats server URLs (separated by comma)")
	fs.IntVar(&cp.RouterPort, "p", DefaultRouterPort, "Server port")
	fs.IntVar(&cp.RouterWriteTimeout, "wt", DefaultRouterWriteTimeout, "Write timeout in seconds")
	fs.IntVar(&cp.RouterReadTimeout, "rt", DefaultRouterReadTimeout, "Read timeout in seconds")
	fs.IntVar(&cp.RouterConnectionsLimit, "cl", DefaultRouterConnectionsLimit, "Limit of incoming connections")
	fs.BoolVar(&cp.Verbose, "v", false, "verbose, log raw NATS traffic")
	fs.Parse(os.Args[1:])
	return cp
}

func Declare() {
	queues := ibusnats.QueuesPartitionsMap{
		"airs-bp": airsBPPartitionsAmount,
	}

	params := ProvideRouterParamsFromCmdLine()

	ibusnatsSrv := &ibusnats.Service{
		NATSServers:      params.NATSServers,
		Queues:           queues,
		CurrentQueueName: "airs-bp",
		Verbose:          ibusnats.Verbose(params.Verbose),
	}
	ibusnats.Declare(ibusnatsSrv)

	routerSrv := ProvideRouterSrv(params)

	DeclareEmbeddedRouter(routerSrv, queues)
}

func ProvideRouterSrv(rp RouterParams) Service {
	return Service{
		Port:             rp.RouterPort,
		WriteTimeout:     rp.RouterWriteTimeout,
		ReadTimeout:      rp.RouterReadTimeout,
		ConnectionsLimit: rp.RouterConnectionsLimit,
	}
}
