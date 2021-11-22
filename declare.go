/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"encoding/json"

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
	if len(routerSrv.NATSServers) > 0 {
		godif.Require(&ibus.SendRequest2)
	}
}

func Declare(ctx context.Context, cqn ibusnats.CurrentQueueName) {
	queues := ibusnats.QueuesPartitionsMap{
		"airs-bp": airsBPPartitionsAmount,
	}

	params := ProvideRouterParamsFromCmdLine()
	params.QueuesPartitions = queues

	if len(params.NATSServers) > 0 {
		ibusnatsSrv := &ibusnats.Service{
			NATSServers:      params.NATSServers,
			Queues:           queues,
			CurrentQueueName: cqn,
			Verbose:          ibusnats.Verbose(params.Verbose),
		}
		ibusnats.Declare(ibusnatsSrv)
	}

	srvs := Provide(ctx, params)
	routerSrv := Service{
		RouterParams:    params,
		srvs:            srvs,
		QueuePartitions: queues,
	}

	DeclareEmbeddedRouter(routerSrv)
}
