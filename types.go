/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"net"
	"net/http"

	"github.com/gorilla/mux"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"golang.org/x/crypto/acme/autocert"
)

type RouterParams struct {
	NATSServers      ibusnats.NATSServers
	Port             int
	WriteTimeout     int
	ReadTimeout      int
	ConnectionsLimit int
	Verbose          bool
	QueuesPartitions ibusnats.QueuesPartitionsMap
	RouterOnly       bool

	// used in airs-bp3 only
	HTTP01ChallengeHost string
	CertDir             string
	RouteDefault        string            // http://10.0.0.3:3000/not-found : https://alpha.dev.untill.ru/unknown/foo -> http://10.0.0.3:3000/not-found/unknown/foo
	Routes              map[string]string // /grafana=http://10.0.0.3:3000 : https://alpha.dev.untill.ru/grafana/foo -> http://10.0.0.3:3000/grafana/foo
	RoutesRewrite       map[string]string // /grafana-rewrite=http://10.0.0.3:3000/rewritten : https://alpha.dev.untill.ru/grafana-rewrite/foo -> http://10.0.0.3:3000/rewritten/foo
}

type reverseProxyHandler http.HandlerFunc

// type reverseProxyHandler struct {
// 	hostProxy map[string]*httputil.ReverseProxy // key is path prefix
// }

type httpService struct {
	RouterParams
	router       *mux.Router
	server       *http.Server
	listener     net.Listener
	ctx          context.Context
	queues       ibusnats.QueuesPartitionsMap
}

type httpsService struct {
	httpService
	crtMgr *autocert.Manager
}

type acmeService struct {
	http.Server
	ctx context.Context
}
