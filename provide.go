/*
 * Copyright (c) 2021-present Sigma-Soft, Ltd. Aleksei Ponomarev
 */

package router2

import (
	"context"
	"crypto/tls"
	"encoding/json"
	in10n "github.com/heeus/core-in10n"
	istructs "github.com/heeus/core-istructs"
	"io/ioutil"

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

func Provide(ctx context.Context, rp RouterParams) []interface{} {
	return ProvideWithBusTimeout(ctx, rp, ibus.DefaultTimeout, nil, in10n.Quotas{})
}

// http -> return []interface{pipeline.IService(httpService)}, https ->  []interface{pipeline.IService(httpsService), pipeline.IService(acmeService)}
func ProvideWithBusTimeout(ctx context.Context, rp RouterParams, aBusTimeout time.Duration, broker in10n.IN10nBroker, quotas in10n.Quotas) []interface{} {
	httpService := httpService{
		RouterParams: rp,
		queues:       rp.QueuesPartitions,
		n10n:         broker,
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
		httpService: httpService,
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
	if err := s.server.ServeTLS(s.listener, "", ""); err != http.ErrServerClosed {
		log.Fatalf("Service.ServeTLS() failure: %s", err)
	}
}

// pipeline.IService
func (s *httpService) Prepare(work interface{}) (err error) {
	s.router = mux.NewRouter()

	if err = s.registerHandlers(); err != nil {
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

func (s *httpService) registerHandlers() (err error) {
	redirectMatcher, err := s.getRedirectMatcher()
	if err != nil {
		return err
	}
	s.router.HandleFunc("/api/check", corsHandler(checkHandler())).Methods("POST", "OPTIONS").Name("router check")
	s.router.HandleFunc("/api", corsHandler(queueNamesHandler())).Name("app names")
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", queueAliasVar,
		wSIDVar, resourceNameVar), corsHandler(partitionHandler(s.queues))).
		Methods("POST", "PATCH", "OPTIONS").Name("api")
	s.router.MatcherFunc(redirectMatcher).Name("reverse proxy")

	s.router.Handle("/n10n/channel", s.newChannelHandler())
	s.router.Handle("/n10n/subscribe", s.subscribeAndWatchHandler(s.ctx))
	s.router.Handle("/n10n/update/{offset:[0-9]{1,10}}", s.updateHandler())

	return nil
}

type route struct {
	targetURL *url.URL
	isRewrite bool
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

// curl -X POST "http://localhost:3001/n10n/channel" -H "Content-Type: application/json" -d "{\"SubjectLogin\": \"paa\"}"
func (s *httpService) newChannelHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			log.Println(err)
			http.Error(resp, "Error when read request body", http.StatusInternalServerError)
			return
		}
		var p map[string]string
		err = json.Unmarshal(body, &p)
		if err != nil {
			log.Println(err)
			http.Error(resp, "Error when parse request body", http.StatusBadRequest)
			return
		}
		subjectLogin, ok := p["SubjectLogin"]
		if !ok {
			log.Println("For create channel you must use SubjectLogin:", err)
			http.Error(resp, "For create channel you must use SubjectLogin!", http.StatusForbidden)
			return
		}
		channel, err := s.n10n.NewChannel(istructs.SubjectLogin(subjectLogin), 24*time.Hour)
		if err != nil {
			log.Println(err)
			http.Error(resp, "Error create new channel", http.StatusTooManyRequests)
			return
		}
		if _, err = resp.Write([]byte(channel)); err != nil {
			log.Println("failed to write channel id to  response:", err)
			http.Error(resp, "Failed to write channel id to response!", http.StatusInternalServerError)
			return
		}
	}
}

// curl -X POST "http://localhost:3001/n10n/subscribe" -H "Content-Type: application/json" -d "{\"channel\": \"c9ce4147-f78f-4e2e-83f5-38ad53890a9e\", \"projectionKey\":{\"App\":\"Application\",\"Projection\":\"paa.price\",\"WS\":1}}"
func (s *httpService) subscribeAndWatchHandler(ctx context.Context) http.HandlerFunc {
	return func(rw http.ResponseWriter, req *http.Request) {
		var payload channelStruct
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			log.Println(err)
			http.Error(rw, "Error read channel id and projections key data for subscribe!", http.StatusInternalServerError)
			return
		}
		err = json.Unmarshal(body, &payload)
		if err != nil {
			log.Println(err)
			http.Error(rw, "Error when parse request body", http.StatusBadRequest)
			return
		}
		channel := payload.Channel
		flusher, ok := rw.(http.Flusher)
		if !ok {
			http.Error(rw, "Streaming unsupported!", http.StatusInternalServerError)
			return
		}
		err = s.n10n.Subscribe(channel, payload.ProjectionKey)
		if err != nil {
			log.Println(err)
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
		ch := make(chan UpdateUnit)
		go s.n10n.WatchChannel(ctx, channel, func(projection in10n.ProjectionKey, offset istructs.Offset) {
			var unit = UpdateUnit{
				Projection: projection,
				Offset:     offset,
			}
			ch <- unit
		})
		for ctx.Err() == nil {
			select {
			case result := <-ch:
				json, err := json.Marshal(&result)
				if err == nil {
					if _, err = rw.Write(append(json, '\n')); err != nil {
						log.Println("failed to write projection update to client:", err)
						http.Error(rw, "failed to write projection update to client", http.StatusInternalServerError)
						return
					}
					flusher.Flush()
				}
			}
		}

	}
}

// curl -X POST "http://localhost:3001/n10n/update" -H "Content-Type: application/json" -d "{\"App\":\"Application\",\"Projection\":\"paa.price\",\"WS\":1}"
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
