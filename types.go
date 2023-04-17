/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/voedger/voedger/pkg/iblobstorage"
	"github.com/voedger/voedger/pkg/in10n"
	"github.com/voedger/voedger/pkg/iprocbus"
	"github.com/voedger/voedger/pkg/iprocbusmem"
	istructs "github.com/voedger/voedger/pkg/istructs"

	"github.com/gorilla/mux"
	ibus "github.com/untillpro/airs-ibus"
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
	UseBP3               bool // impacts on router handlers
	HTTP01ChallengeHosts []string
	CertDir              string
	RouteDefault         string            // http://10.0.0.3:3000/not-found : https://alpha.dev.untill.ru/unknown/foo -> http://10.0.0.3:3000/not-found/unknown/foo
	Routes               map[string]string // /grafana=http://10.0.0.3:3000 : https://alpha.dev.untill.ru/grafana/foo -> http://10.0.0.3:3000/grafana/foo
	RoutesRewrite        map[string]string // /grafana-rewrite=http://10.0.0.3:3000/rewritten : https://alpha.dev.untill.ru/grafana-rewrite/foo -> http://10.0.0.3:3000/rewritten/foo
	RouteDomains         map[string]string // resellerportal.dev.untill.ru=http://resellerportal : https://resellerportal.dev.untill.ru/foo -> http://resellerportal/foo
}

type BlobberServiceChannels []iprocbusmem.ChannelGroup
type BLOBMaxSizeType int64

type BlobberParams struct {
	ServiceChannels        []iprocbusmem.ChannelGroup
	ClusterAppBlobberID    istructs.ClusterAppID
	BLOBStorage            iblobstorage.IBLOBStorage
	BLOBWorkersNum         int
	procBus                iprocbus.IProcBus
	RetryAfterSecondsOn503 int
	BLOBMaxSize            BLOBMaxSizeType
}

type httpService struct {
	RouterParams
	*BlobberParams
	router       *mux.Router
	server       *http.Server
	listener     net.Listener
	queues       ibusnats.QueuesPartitionsMap
	n10n         in10n.IN10nBroker
	blobWG       sync.WaitGroup
	bus          ibus.IBus
	busTimeout   time.Duration
	appsWSAmount map[istructs.AppQName]istructs.AppWSAmount
}

type httpsService struct {
	*httpService
	crtMgr *autocert.Manager
}

type acmeService struct {
	http.Server
}

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

type route struct {
	targetURL  *url.URL
	isRewrite  bool
	fromDomain string
}

type implIBusBP2 struct{}
