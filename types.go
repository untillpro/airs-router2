/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"net"
	"net/http"

	in10n "github.com/heeus/core-in10n"
	istructs "github.com/heeus/core-istructs"

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

	// used in airs-bp3 only
	UseBP3              bool // impacts on router handlers
	HTTP01ChallengeHost string
	CertDir             string
	RouteDefault        string            // http://10.0.0.3:3000/not-found : https://alpha.dev.untill.ru/unknown/foo -> http://10.0.0.3:3000/not-found/unknown/foo
	Routes              map[string]string // /grafana=http://10.0.0.3:3000 : https://alpha.dev.untill.ru/grafana/foo -> http://10.0.0.3:3000/grafana/foo
	RoutesRewrite       map[string]string // /grafana-rewrite=http://10.0.0.3:3000/rewritten : https://alpha.dev.untill.ru/grafana-rewrite/foo -> http://10.0.0.3:3000/rewritten/foo
}

type httpService struct {
	RouterParams
	router   *mux.Router
	server   *http.Server
	listener net.Listener
	ctx      context.Context
	queues   ibusnats.QueuesPartitionsMap
	n10n     in10n.IN10nBroker
}

type httpsService struct {
	httpService
	crtMgr *autocert.Manager
}

type acmeService struct {
	http.Server
	ctx context.Context
}

// UpdateUnit TODO: DUPLICATE!!!! change in coew-in10nmem from updateUnit to UpdateUnit for correct marshal/unmarshal
type UpdateUnit struct {
	Projection in10n.ProjectionKey
	Offset     istructs.Offset
}

type createChannelParamsType struct {
	SubjectLogin  istructs.SubjectLogin
	ProjectionKey []in10n.ProjectionKey
}

type subscriberParamsType struct {
	Channel       in10n.ChannelID
	ProjectionKey []in10n.ProjectionKey
}
