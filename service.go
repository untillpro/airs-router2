/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"log"

	ibusnats "github.com/untillpro/airs-ibusnats"
)

// Service s.e.
// if Service use port 443, it will be started safely with TLS and use Lets Encrypt certificate
type Service struct {
	RouterParams
	QueuePartitions ibusnats.QueuesPartitionsMap
	srvs            []interface{}
}

type routerKeyType string

const routerKey = routerKeyType("router")

// Start s.e.
func (s *Service) Start(ctx context.Context) (context.Context, error) {
	s.srvs = Provide(ctx, s.RouterParams)
	for _, srvIntf := range s.srvs {
		// simulate pipeline.ServiceOperator
		srv := srvIntf.(interface {
			Prepare(work interface{}) error
			Run(ctx context.Context)
		})
		if err := srv.Prepare(nil); err != nil {
			return ctx, err
		}
		go srv.Run(ctx)
	}
	if len(s.NATSServers) == 0 {
		log.Println("Router started in router-only mode")
	} else {
		log.Println("Router started")
	}
	return context.WithValue(ctx, routerKey, s), nil
}

// Stop s.e.
func (s *Service) Stop(ctx context.Context) {
	for _, srvIntf := range s.srvs {
		srv := srvIntf.(interface{ Stop() })
		srv.Stop()
	}
}
