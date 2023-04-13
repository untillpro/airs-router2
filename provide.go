/*
 * Copyright (c) 2021-present Sigma-Soft, Ltd. Aleksei Ponomarev
 */

package router2

import (
	"context"
	"crypto/tls"

	iprocbusmem "github.com/heeus/core-iprocbusmem"
	"github.com/untillpro/voedger/pkg/in10n"
	istructs "github.com/untillpro/voedger/pkg/istructs"
	coreutils "github.com/untillpro/voedger/pkg/utils"

	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	flag "github.com/spf13/pflag"
	ibus "github.com/untillpro/airs-ibus"
	"github.com/untillpro/goutils/logger"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/netutil"
)

func ProvideBP2(ctx context.Context, rp RouterParams, busTimeout time.Duration) []interface{} {
	return ProvideBP3(ctx, rp, busTimeout, nil, in10n.Quotas{}, nil, nil, &implIBusBP2{}, nil)
}

// http -> return []interface{pipeline.IService(httpService)}, https ->  []interface{pipeline.IService(httpsService), pipeline.IService(acmeService)}
func ProvideBP3(hvmCtx context.Context, rp RouterParams, aBusTimeout time.Duration, broker in10n.IN10nBroker, quotas in10n.Quotas, bp *BlobberParams, autocertCache autocert.Cache,
	bus ibus.IBus, appsWSAmount map[istructs.AppQName]istructs.AppWSAmount) []interface{} {
	httpService := httpService{
		RouterParams:  rp,
		queues:        rp.QueuesPartitions,
		n10n:          broker,
		BlobberParams: bp,
		bus:           bus,
		busTimeout:    aBusTimeout,
		appsWSAmount:  appsWSAmount,
	}
	if bp != nil {
		bp.procBus = iprocbusmem.Provide(bp.ServiceChannels)
		for i := 0; i < bp.BLOBWorkersNum; i++ {
			httpService.blobWG.Add(1)
			go func() {
				defer httpService.blobWG.Done()
				blobMessageHandler(hvmCtx, bp.procBus.ServiceChannel(0, 0), bp.BLOBStorage, bus, aBusTimeout)
			}()
		}

	}
	if rp.Port != HTTPSPort {
		return []interface{}{&httpService}
	}
	crtMgr := &autocert.Manager{
		/*
			В том случае если требуется тестировать выпуск большого количества сертификатов для разных доменов,
			то нужно использовать тестовый контур компании. Для этого в Manager требуется переопределить DirectoryURL в клиенте на
			https://acme-staging-v02.api.letsencrypt.org/directory :
			Client: &acme.Client{
				DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
			},
			поскольку есть квоты на выпуск сертификатов - на количество доменов,  сертификатов в единицу времени и пр.
		*/
		Prompt:     autocert.AcceptTOS,
		HostPolicy: autocert.HostWhitelist(rp.HTTP01ChallengeHosts...),
		Cache:      autocertCache,
	}
	if crtMgr.Cache == nil {
		crtMgr.Cache = autocert.DirCache(rp.CertDir)
	}
	httpsService := &httpsService{
		httpService: &httpService,
		crtMgr:      crtMgr,
	}

	// handle Lets Encrypt callback over 80 port - only port 80 allowed
	acmeService := &acmeService{
		Server: http.Server{
			Addr:         ":80",
			ReadTimeout:  DefaultACMEServerReadTimeout,
			WriteTimeout: DefaultACMEServerWriteTimeout,
			Handler:      crtMgr.HTTPHandler(nil),
		},
	}
	acmeServiceHadler := crtMgr.HTTPHandler(nil)
	if logger.IsVerbose() {
		acmeService.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			logger.Verbose("acme server request:", r.Method, r.Host, r.RemoteAddr, r.RequestURI, r.URL.String())
			acmeServiceHadler.ServeHTTP(rw, r)
		})
	} else {
		acmeService.Handler = acmeServiceHadler
	}
	return []interface{}{httpsService, acmeService}
}

func ProvideRouterParamsFromCmdLine() RouterParams {
	fs := flag.NewFlagSet("", flag.ExitOnError)
	rp := RouterParams{}
	routes := []string{}
	routesRewrite := []string{}
	natsServers := ""
	isVerbose := false
	fs.StringVar(&natsServers, "ns", "", "The nats server URLs (separated by comma)")
	fs.IntVar(&rp.Port, "p", DefaultRouterPort, "Server port")
	fs.IntVar(&rp.WriteTimeout, "wt", DefaultRouterWriteTimeout, "Write timeout in seconds")
	fs.IntVar(&rp.ReadTimeout, "rt", DefaultRouterReadTimeout, "Read timeout in seconds")
	fs.IntVar(&rp.ConnectionsLimit, "cl", DefaultRouterConnectionsLimit, "Limit of incoming connections")
	fs.BoolVar(&rp.Verbose, "v", false, "verbose, log raw NATS traffic")

	// actual for airs-bp3 only
	fs.StringSliceVar(&routes, "rht", []string{}, "reverse proxy </url-part-after-ip>=<target> mapping")
	fs.StringSliceVar(&routesRewrite, "rhtr", []string{}, "reverse proxy </url-part-after-ip>=<target> rewriting mapping")
	fs.StringSliceVar(&rp.HTTP01ChallengeHosts, "rch", []string{}, "HTTP-01 Challenge host for let's encrypt service. Must be specified if router-port is 443, ignored otherwise")
	fs.StringVar(&rp.RouteDefault, "rhtd", "", "url to be redirected to if url is unknown")
	fs.StringVar(&rp.CertDir, "rcd", ".", "SSL certificates dir")

	_ = fs.Parse(os.Args[1:]) // os.Exit on error
	if len(natsServers) > 0 {
		_ = rp.NATSServers.Set(natsServers) // error impossible
	}
	if err := coreutils.PairsToMap(routes, rp.Routes); err != nil {
		panic(err)
	}
	if err := coreutils.PairsToMap(routesRewrite, rp.RoutesRewrite); err != nil {
		panic(err)
	}
	if isVerbose {
		logger.SetLogLevel(logger.LogLevelVerbose)
	}
	return rp
}

func (s *httpsService) Prepare(work interface{}) error {
	if err := s.httpService.Prepare(work); err != nil {
		return err
	}

	s.server.TLSConfig = &tls.Config{GetCertificate: s.crtMgr.GetCertificate, MinVersion: tls.VersionTLS12} // VersionTLS13 is unsupported by Chargebee
	return nil
}

func (s *httpsService) Run(ctx context.Context) {
	log.Printf("Starting HTTPS server on %s\n", s.server.Addr)
	logger.Info("HTTPS server Write Timeout: ", s.server.WriteTimeout)
	logger.Info("HTTPS server Read Timeout: ", s.server.ReadTimeout)
	if err := s.server.ServeTLS(s.listener, "", ""); err != http.ErrServerClosed {
		log.Fatalf("Service.ServeTLS() failure: %s", err)
	}
}

// pipeline.IService
func (s *httpService) Prepare(work interface{}) (err error) {
	s.router = mux.NewRouter()

	// https://dev.untill.com/projects/#!627072
	s.router.SkipClean(true)

	if err = s.registerHandlers(s.busTimeout, s.appsWSAmount); err != nil {
		return err
	}

	port := strconv.Itoa(s.RouterParams.Port)

	if s.listener, err = net.Listen("tcp", ":"+port); err != nil {
		return err
	}

	if s.RouterParams.ConnectionsLimit > 0 {
		s.listener = netutil.LimitListener(s.listener, s.RouterParams.ConnectionsLimit)
	}

	s.server = &http.Server{
		Addr:         ":" + port,
		Handler:      s.router,
		ReadTimeout:  time.Duration(s.RouterParams.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.RouterParams.WriteTimeout) * time.Second,
	}

	return nil
}

// pipeline.IService
func (s *httpService) Run(ctx context.Context) {
	s.server.BaseContext = func(l net.Listener) context.Context {
		return ctx // need to track both client disconnect and app finalize
	}
	logger.Info("Starting HTTP server on", s.listener.Addr().(*net.TCPAddr).String())
	if err := s.server.Serve(s.listener); err != http.ErrServerClosed {
		log.Println("main HTTP server failure: " + err.Error())
	}
}

// pipeline.IService
func (s *httpService) Stop() {
	// ctx here is used to avoid eternal waiting for close idle connections and listeners
	// all connections and listeners are closed in the explicit way (they're tracks ctx.Done()) so it is not necessary to track ctx here
	if err := s.server.Shutdown(context.Background()); err != nil {
		log.Println("http server Shutdown() failed: " + err.Error())
		s.listener.Close()
		s.server.Close()
	}
	if s.n10n != nil {
		for s.n10n.MetricNumSubcriptions() > 0 {
			time.Sleep(subscriptionsCloseCheckInterval)
		}
	}
	s.blobWG.Wait()
}

func (s *httpService) GetPort() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

func (s *httpService) registerHandlers(busTimeout time.Duration, appsWSAmount map[istructs.AppQName]istructs.AppWSAmount) (err error) {
	redirectMatcher, err := s.getRedirectMatcher()
	if err != nil {
		return err
	}
	s.router.HandleFunc("/api/check", corsHandler(checkHandler())).Methods("POST", "OPTIONS").Name("router check")
	s.router.HandleFunc("/api", corsHandler(queueNamesHandler())).Name("queues names")
	/*
		launching app from localhost from browser. Trying to execute POST from web app within browser.
		Browser sees that hosts differs: from localhost to alpha -> need CORS -> denies POST and executes the same request with OPTIONS header
		-> need to allow OPTIONS
	*/
	if s.BlobberParams != nil {
		s.router.Handle(fmt.Sprintf("/blob/{%s}/{%s}/{%s:[0-9]+}", bp3AppOwner, bp3AppName, wSIDVar), corsHandler(s.blobWriteRequestHandler())).
			Methods("POST", "OPTIONS").
			Name("blob write")
		s.router.Handle(fmt.Sprintf("/blob/{%s}/{%s}/{%s:[0-9]+}/{%s:[0-9]+}", bp3AppOwner, bp3AppName, wSIDVar, bp3BLOBID), corsHandler(s.blobReadRequestHandler())).
			Methods("POST", "GET", "OPTIONS").
			Name("blob read")
	}
	if s.RouterParams.UseBP3 {
		s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", bp3AppOwner, bp3AppName,
			wSIDVar, resourceNameVar), corsHandler(partitionHandler(s.queues, s.bus, busTimeout, appsWSAmount))).
			Methods("POST", "PATCH", "OPTIONS").Name("api")
	} else {
		s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", queueAliasVar,
			wSIDVar, resourceNameVar), corsHandler(partitionHandler(s.queues, s.bus, busTimeout, appsWSAmount))).
			Methods("POST", "PATCH", "OPTIONS").Name("api")
	}
	s.router.Handle("/n10n/channel", corsHandler(s.subscribeAndWatchHandler())).Methods("GET")
	s.router.Handle("/n10n/subscribe", corsHandler(s.subscribeHandler())).Methods("GET")
	s.router.Handle("/n10n/unsubscribe", corsHandler(s.unSubscribeHandler())).Methods("GET")
	s.router.Handle("/n10n/update/{offset:[0-9]{1,10}}", corsHandler(s.updateHandler()))

	// must be the last handler
	s.router.MatcherFunc(redirectMatcher).Name("reverse proxy")
	return nil
}

// pipeline.IService
func (s *acmeService) Prepare(work interface{}) error {
	return nil
}

// pipeline.IService
func (s *acmeService) Run(ctx context.Context) {
	s.BaseContext = func(l net.Listener) context.Context {
		return ctx // need to track both client disconnect and app finalize
	}
	log.Println("Starting ACME HTTP server on :80")
	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		log.Println("ACME HTTP server failure: ", err.Error())
	}
}

// pipeline.IService
func (s *acmeService) Stop() {
	// ctx here is used to avoid eternal waiting for close idle connections and listeners
	// all connections and listeners are closed in the explicit way so it is not necessary to track ctx
	if err := s.Shutdown(context.Background()); err != nil {
		s.Close()
	}
}
