/*
 * Copyright (c) 2021-present Sigma-Soft, Ltd.
 * Aleksei Ponomarev
 */

package main

import (
	"context"
	router "github.com/untillpro/airs-router2"
	"sync"
)

var wg sync.WaitGroup

func main() {
	ctx := context.TODO()
	rp := router.RouterParams{
		RouterPort:             443,
		RouterWriteTimeout:     42,
		RouterReadTimeout:      43,
		RouterConnectionsLimit: 44,
	}
	routerSrv := ProvideRouterSrv(rp)
	wg.Add(1)
	func() {
		routerSrv.Start(ctx)
	}()
	wg.Wait()
}

func ProvideRouterSrv(rp router.RouterParams) router.Service {
	var (
		urlMapping = map[string]string{
			"/sigma":   "http://www.sigma-soft.ru",
			"/about":   "http://www.sigma-soft.ru/about/about.shtml",
			"/support": "http://www.sigma-soft.ru/support/support.shtml",
		}
	)

	return router.Service{
		Port:             rp.RouterPort,
		WriteTimeout:     rp.RouterWriteTimeout,
		ReadTimeout:      rp.RouterReadTimeout,
		ConnectionsLimit: rp.RouterConnectionsLimit,
		ReverseProxy:     router.NewReverseProxy(urlMapping),
	}
}
