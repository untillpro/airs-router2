/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"encoding/json"
	"flag"
	"os"

	ibus "github.com/untillpro/airs-ibus"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

func DeclareEmbeddedRouter(routerSrv Service) {
	queuesNames := []string{}
	for name := range routerSrv.QueuePartitions {
		queuesNames = append(queuesNames, name)
	}
	queueNamesJSON, _ = json.Marshal(&queuesNames) // error impossible
	godif.ProvideSliceElement(&services.Services, &routerSrv)
	godif.Require(&ibus.SendRequest2)
}

type RouterParams struct {
	NATSServers      ibusnats.NATSServers
	Port             int
	WriteTimeout     int
	ReadTimeout      int
	ConnectionsLimit int
	Verbose          bool
	QueuesPartitions ibusnats.QueuesPartitionsMap

	// used in airs-bp3 only
	HTTP01ChallengeHost string
	CertDir             string
	HostTargetDefault   string
	ReverseProxyMapping map[string]string // used for register path in multiplexer, e.g. "/metric":"http://192.168.1.1:8080/metric"
}

func ProvideRouterParamsFromCmdLine() RouterParams {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	rp := RouterParams{}
	fs.Var(&rp.NATSServers, "ns", "The nats server URLs (separated by comma)")
	fs.IntVar(&rp.Port, "p", DefaultRouterPort, "Server port")
	fs.IntVar(&rp.WriteTimeout, "wt", DefaultRouterWriteTimeout, "Write timeout in seconds")
	fs.IntVar(&rp.ReadTimeout, "rt", DefaultRouterReadTimeout, "Read timeout in seconds")
	fs.IntVar(&rp.ConnectionsLimit, "cl", DefaultRouterConnectionsLimit, "Limit of incoming connections")
	fs.BoolVar(&rp.Verbose, "v", false, "verbose, log raw NATS traffic")
	fs.Parse(os.Args[1:])
	return rp
}

func Declare(ctx context.Context, cqn ibusnats.CurrentQueueName) {
	queues := ibusnats.QueuesPartitionsMap{
		"airs-bp": airsBPPartitionsAmount,
	}

	params := ProvideRouterParamsFromCmdLine()
	params.QueuesPartitions = queues

	ibusnatsSrv := &ibusnats.Service{
		NATSServers:      params.NATSServers,
		Queues:           queues,
		CurrentQueueName: cqn,
		Verbose:          ibusnats.Verbose(params.Verbose),
	}
	ibusnats.Declare(ibusnatsSrv)

	srvs := Provide(ctx, params)
	routerSrv := Service{
		RouterParams:    params,
		srvs:            srvs,
		QueuePartitions: queues,
	}

	DeclareEmbeddedRouter(routerSrv)
}
