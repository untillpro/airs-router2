/*
 * Copyright (c) 2021-present Sigma-Soft, Ltd. Aleksei Ponomarev
 */

package router2

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io/ioutil"

	in10n "github.com/heeus/core-in10n"
	iprocbusmem "github.com/heeus/core-iprocbusmem"
	istructs "github.com/heeus/core-istructs"

	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	logger "github.com/heeus/core-logger"
	flag "github.com/spf13/pflag"
	ibus "github.com/untillpro/airs-ibus"
	"github.com/valyala/bytebufferpool"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/netutil"
)

func ProvideBP2(ctx context.Context, rp RouterParams) []interface{} {
	return ProvideBP3(ctx, rp, ibus.DefaultTimeout, nil, in10n.Quotas{}, nil)
}

// http -> return []interface{pipeline.IService(httpService)}, https ->  []interface{pipeline.IService(httpsService), pipeline.IService(acmeService)}
func ProvideBP3(ctx context.Context, rp RouterParams, aBusTimeout time.Duration, broker in10n.IN10nBroker, quotas in10n.Quotas, bp *BlobberParams) []interface{} {
	httpService := httpService{
		RouterParams:  rp,
		queues:        rp.QueuesPartitions,
		n10n:          broker,
		BlobberParams: bp,
	}
	if bp != nil {
		bp.procBus = iprocbusmem.Provide(bp.ServiceChannels)
		for i := 0; i < bp.BLOBWorkersNum; i++ {
			httpService.blobWG.Add(1)
			go func(i int) {
				defer httpService.blobWG.Done()
				blobMessageHandler(ctx, bp.procBus.ServiceChannel(0, 0), bp.ClusterAppBlobberID, bp.BLOBStorage)
			}(i)
		}

	}
	busTimeout = aBusTimeout
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
		HostPolicy: autocert.HostWhitelist(rp.HTTP01ChallengeHost),
		Cache:      autocert.DirCache(rp.CertDir),
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
	if logger.IsDebug() {
		acmeService.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			logger.Debug("acme server request:", r.Method, r.Host, r.RemoteAddr, r.RequestURI, r.URL.String())
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
	fs.StringSliceVar(&routesRewrite, "rhtr", []string{}, "reverse proxy </url-part-after-ip>=<target> rewritting mapping")
	fs.StringVar(&rp.HTTP01ChallengeHost, "rch", "", "HTTP-01 Challenge host for let's encrypt service. Must be specified if router-port is 443, ignored otherwise")
	fs.StringVar(&rp.RouteDefault, "rhtd", "", "url to be redirected to if url is unknown")
	fs.StringVar(&rp.CertDir, "rcd", ".", "SSL certificates dir")

	_ = fs.Parse(os.Args[1:]) // os.Exit on error
	if len(natsServers) > 0 {
		_ = rp.NATSServers.Set(natsServers) // error impossible
	}
	if err := ParseRoutes(routes, rp.Routes); err != nil {
		panic(err)
	}
	if err := ParseRoutes(routesRewrite, rp.RoutesRewrite); err != nil {
		panic(err)
	}
	if isVerbose {
		logger.SetLogLevel(logger.LogLevelDebug)
	}
	return rp
}

func (s *httpsService) Prepare(work interface{}) error {
	if err := s.httpService.Prepare(work); err != nil {
		return err
	}

	s.server.TLSConfig = &tls.Config{GetCertificate: s.crtMgr.GetCertificate}
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

	if err = s.registerHandlers(s.BlobberParams); err != nil {
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
	s.ctx = ctx
	log.Printf("Starting HTTP server on %s\n", s.server.Addr)
	if err := s.server.Serve(s.listener); err != http.ErrServerClosed {
		log.Println("main HTTP server failure: " + err.Error())
	}
}

// pipeline.IService
func (s *httpService) Stop() {
	if err := s.server.Shutdown(s.ctx); err != nil {
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

func parseRoutes(routesURLs map[string]route, routes map[string]string, isRewrite bool) error {
	for from, to := range routes {
		if !strings.HasPrefix(from, "/") {
			return fmt.Errorf("%s reverse proxy url must have a leading slash", from)
		}
		targetURL, err := parseURL(to)
		if err != nil {
			return err
		}
		routesURLs[from] = route{
			targetURL,
			isRewrite,
		}
		logger.Info("reverse proxy route registered: ", from, " -> ", to)
	}
	return nil
}

// match reverse proxy urls, redirect and handle as reverse proxy
// route        : /grafana=http://10.0.0.3:3000 : https://alpha.dev.untill.ru/grafana/foo -> http://10.0.0.3:3000/grafana/foo
// route rewrite: /grafana-rewrite=http://10.0.0.3:3000/rewritten : https://alpha.dev.untill.ru/grafana-rewrite/foo -> http://10.0.0.3:3000/rewritten/foo
// default route: http://10.0.0.3:3000/not-found : https://alpha.dev.untill.ru/unknown/foo -> http://10.0.0.3:3000/not-found/unknown/foo
func (s *httpService) getRedirectMatcher() (redirectMatcher mux.MatcherFunc, err error) {
	routes := map[string]route{}
	reverseProxy := &httputil.ReverseProxy{Director: func(r *http.Request) {}} // director's job is done by redirectMatcher
	if err := parseRoutes(routes, s.Routes, false); err != nil {
		return nil, err
	}
	if err = parseRoutes(routes, s.RoutesRewrite, true); err != nil {
		return nil, err
	}
	var defaultRouteURL *url.URL
	if len(s.RouteDefault) > 0 {
		if defaultRouteURL, err = parseURL(s.RouteDefault); err != nil {
			return nil, err
		}
		logger.Info("default route registered: ", s.RouteDefault)
	}
	return func(req *http.Request, rm *mux.RouteMatch) bool {
		pathPrefix := bytebufferpool.Get()
		defer bytebufferpool.Put(pathPrefix)

		pathParts := strings.Split(req.URL.Path, "/")
		for _, pathPart := range pathParts[1:] { // ignore first empty path part. URL must have a trailing slash (already checked)
			_, _ = pathPrefix.WriteString("/")      // error impossible
			_, _ = pathPrefix.WriteString(pathPart) // error impossible
			route, ok := routes[pathPrefix.String()]
			if !ok {
				continue
			}
			targetPath := req.URL.Path
			if route.isRewrite {
				// /grafana-rewrite/foo -> /rewritten/foo
				targetPath = strings.Replace(req.URL.Path, pathPrefix.String(), route.targetURL.Path, 1)
			}
			redirect(req, targetPath, route.targetURL)
			rm.Handler = reverseProxy
			return true
		}
		if defaultRouteURL != nil {
			// no match -> redirect to default route if specified
			targetPath := defaultRouteURL.Path + req.URL.Path
			redirect(req, targetPath, defaultRouteURL)
			rm.Handler = reverseProxy
			return true
		}
		return false
	}, nil
}

func (s *httpService) registerHandlers(bp *BlobberParams) (err error) {
	redirectMatcher, err := s.getRedirectMatcher()
	if err != nil {
		return err
	}
	s.router.HandleFunc("/api/check", corsHandler(checkHandler())).Methods("POST", "OPTIONS").Name("router check")
	s.router.HandleFunc("/api", corsHandler(queueNamesHandler())).Name("queues names")
	if bp != nil {
		s.router.Handle(fmt.Sprintf("/api/%s/{%s:[0-9]+}", istructs.AppQName_sys_blobber.String(), wSIDVar),
			corsHandler(s.blobReadRequestHandler())).
			Methods("GET").
			Queries("principalToken", "{principalToken}", "blobID", "{blobID}").
			Name("blob read")
		s.router.Handle(fmt.Sprintf("/api/%s/{%s:[0-9]+}", istructs.AppQName_sys_blobber.String(), wSIDVar),
			corsHandler(s.blobWriteRequestHandler())).
			Methods("POST").
			Queries("principalToken", "{principalToken}", "name", "{name}", "mimeType", "{mimeType}").
			Name("blob write")
	}
	if s.RouterParams.UseBP3 {
		s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", bp3AppOwner, bp3AppName,
			wSIDVar, resourceNameVar), corsHandler(partitionHandler(s.queues))).
			Methods("POST", "PATCH", "OPTIONS").Name("api")
	} else {
		s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", queueAliasVar,
			wSIDVar, resourceNameVar), corsHandler(partitionHandler(s.queues))).
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

func parseURL(urlStr string) (url *url.URL, err error) {
	url, err = url.Parse(urlStr)
	if err != nil {
		err = fmt.Errorf("target url %s parse failed: %w", urlStr, err)
	}
	return
}

func redirect(req *http.Request, targetPath string, targetURL *url.URL) {
	if logger.IsDebug() {
		logger.Debug(fmt.Sprintf("reverse proxy: incoming %s %s%s, redirecting to %s%s", req.Method, req.Host, req.URL, targetURL.Host, targetPath))
	}
	req.URL.Path = targetPath
	req.Host = targetURL.Host
	req.URL.Scheme = targetURL.Scheme
	req.URL.Host = targetURL.Host
	targetQuery := targetURL.RawQuery
	if targetQuery == "" || req.URL.RawQuery == "" {
		req.URL.RawQuery = targetQuery + req.URL.RawQuery
	} else {
		req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
	}
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
	s.ctx = ctx
	log.Println("Starting ACME HTTP server on :80")
	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		log.Println("ACME HTTP server failure: ", err.Error())
	}
}

// pipeline.IService
func (s *acmeService) Stop() {
	if err := s.Shutdown(s.ctx); err != nil {
		s.Close()
	}
}

/*
curl -G --data-urlencode "payload={\"SubjectLogin\": \"paa\", \"ProjectionKey\":[{\"App\":\"Application\",\"Projection\":\"paa.price\",\"WS\":1}, {\"App\":\"Application\",\"Projection\":\"paa.wine_price\",\"WS\":1}]}" https://alpha2.dev.untill.ru/n10n/channel -H "Content-Type: application/json"
*/
func (s *httpService) subscribeAndWatchHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		var (
			urlParams createChannelParamsType
			channel   in10n.ChannelID
			flusher   http.Flusher
			err       error
		)
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")
		jsonParam, ok := req.URL.Query()["payload"]
		if !ok || len(jsonParam[0]) < 1 {
			log.Println("Query parameter with payload (SubjectLogin id and ProjectionKey) is missing.")
			err = errors.New("query parameter with payload (SubjectLogin id and ProjectionKey) is missing")
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		err = json.Unmarshal([]byte(jsonParam[0]), &urlParams)
		if err != nil {
			log.Println(err)
			err = fmt.Errorf("cannot unmarshal input payload %w", err)
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		flusher, ok = rw.(http.Flusher)
		if !ok {
			http.Error(rw, "Streaming unsupported!", http.StatusInternalServerError)
			return
		}
		channel, err = s.n10n.NewChannel(urlParams.SubjectLogin, 24*time.Hour)
		if err != nil {
			log.Println(err)
			http.Error(rw, "Error create new channel", http.StatusTooManyRequests)
			return
		}
		if _, err = fmt.Fprintf(rw, "event: channelId\ndata: %s\n\n", channel); err != nil {
			log.Println("failed to write created channel id to client:", err)
			return
		}
		flusher.Flush()
		for _, projection := range urlParams.ProjectionKey {
			err = s.n10n.Subscribe(channel, projection)
			if err != nil {
				log.Println(err)
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		ch := make(chan UpdateUnit)
		go func() {
			defer close(ch)
			s.n10n.WatchChannel(req.Context(), channel, func(projection in10n.ProjectionKey, offset istructs.Offset) {
				var unit = UpdateUnit{
					Projection: projection,
					Offset:     offset,
				}
				ch <- unit
			})
		}()
		for req.Context().Err() == nil {
			var (
				projection, offset []byte
			)
			result, ok := <-ch
			if !ok {
				break
			}
			projection, err = json.Marshal(&result.Projection)
			if err == nil {
				if _, err = fmt.Fprintf(rw, "event: %s\n", projection); err != nil {
					log.Println("failed to write projection key event to client:", err)
				}
			}
			offset, _ = json.Marshal(&result.Offset) // error impossible
			if _, err = fmt.Fprintf(rw, "data: %s\n\n", offset); err != nil {
				log.Println("failed to write projection key offset to client:", err)
			}
			flusher.Flush()
		}
	}
}

/*
curl -G --data-urlencode "payload={\"Channel\": \"a23b2050-b90c-4ed1-adb7-1ecc4f346f2b\", \"ProjectionKey\":[{\"App\":\"Application\",\"Projection\":\"paa.wine_price\",\"WS\":1}]}" https://alpha2.dev.untill.ru/n10n/subscribe -H "Content-Type: application/json"
*/
func (s *httpService) subscribeHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		var parameters subscriberParamsType
		err := getJsonPayload(req, &parameters)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
		}
		for _, projection := range parameters.ProjectionKey {
			err = s.n10n.Subscribe(parameters.Channel, projection)
			if err != nil {
				log.Println(err)
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
}

/*
curl -G --data-urlencode "payload={\"Channel\": \"a23b2050-b90c-4ed1-adb7-1ecc4f346f2b\", \"ProjectionKey\":[{\"App\":\"Application\",\"Projection\":\"paa.wine_price\",\"WS\":1}]}" https://alpha2.dev.untill.ru/n10n/unsubscribe -H "Content-Type: application/json"
*/
func (s *httpService) unSubscribeHandler() http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		var parameters subscriberParamsType
		err := getJsonPayload(req, &parameters)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
		}
		for _, projection := range parameters.ProjectionKey {
			err = s.n10n.Unsubscribe(parameters.Channel, projection)
			if err != nil {
				log.Println(err)
				http.Error(rw, err.Error(), http.StatusInternalServerError)
				return
			}
		}
	}
}

// curl -X POST "http://localhost:3001/n10n/update" -H "Content-Type: application/json" -d "{\"App\":\"Application\",\"Projection\":\"paa.price\",\"WS\":1}"
// TODO: eliminate after airs-bp3 integration tests implementation
func (s *httpService) updateHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		var p in10n.ProjectionKey
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			log.Println(err)
			http.Error(resp, "Error when read request body", http.StatusInternalServerError)
			return
		}
		err = json.Unmarshal(body, &p)
		if err != nil {
			log.Println(err)
			http.Error(resp, "Error when parse request body", http.StatusBadRequest)
			return
		}

		params := mux.Vars(req)
		offset := params["offset"]
		if off, err := strconv.ParseInt(offset, 10, 64); err == nil {
			s.n10n.Update(p, istructs.Offset(off))
		}
	}
}

func getJsonPayload(req *http.Request, payload *subscriberParamsType) (err error) {
	jsonParam, ok := req.URL.Query()["payload"]
	if !ok || len(jsonParam[0]) < 1 {
		log.Println("Url parameter with payload (channel id and projection key) is missing")
		return errors.New("url parameter with payload (channel id and projection key) is missing")
	}
	err = json.Unmarshal([]byte(jsonParam[0]), payload)
	if err != nil {
		log.Println(err)
		err = fmt.Errorf("cannot unmarshal input payload %w", err)
	}
	return err
}
