/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"

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
	HostTargetDefault   string
	ReverseProxyMapping map[string]string // used for register path in multiplexer, e.g. "/metric":"http://192.168.1.1:8080/metric"
}

type reverseProxyHandler struct {
	hostProxy map[string]*httputil.ReverseProxy
}

type httpService struct {
	RouterParams
	router       *mux.Router
	server       *http.Server
	listener     net.Listener
	reverseProxy *reverseProxyHandler
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
