/*
 * Copyright (c) 2021-present Sigma-Soft, Ltd. Aleksei Ponomarev
 */

package router2

import (
	"context"
	"crypto/tls"

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
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/netutil"
)

// http -> return []interface{pipeline.IService(httpService)}, https ->  []interface{pipeline.IService(httpsService), pipeline.IService(acmeService)}
func Provide(ctx context.Context, rp RouterParams) []interface{} {
	httpService := httpService{
		RouterParams: rp,
		reverseProxy: &reverseProxyHandler{
			map[string]*httputil.ReverseProxy{},
		},
		queues: rp.QueuesPartitions,
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
	fs.BoolVar(&rp.RouterOnly, "ro", false, "Router only mode, no NATS. http/https server will be launched")

	// actual for airs-bp3 only
	fs.StringSliceVar(&routes, "rht", []string{}, "reverse proxy </url-part-after-ip>=<target> mapping")
	fs.StringSliceVar(&routesRewrite, "rhtr", []string{}, "reverse proxy </url-part-after-ip>=<target> rewritting mapping")
	fs.StringVar(&rp.HTTP01ChallengeHost, "rch", "", "HTTP-01 Challenge host for let's encrypt service. Must be specified if router-port is 443, ignored otherwise")
	fs.StringVar(&rp.RouteDefault, "rhtd", "", "url to be redirected to if url is unknown")
	fs.StringVar(&rp.CertDir, "rcd", ".", "SSL certificates dir")

	fs.Parse(os.Args[1:])
	rp.NATSServers.Set(natsServers)
	ParseRoutes(routes, rp.Routes)
	ParseRoutes(routesRewrite, rp.RoutesRewrite)
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

	s.registerHandlers()
	if err = s.registerReverseProxyHandlers(); err != nil {
		return err
	}

	if len(s.RouteDefault) > 0 {
		routeDefaultURL, err := url.Parse(s.RouteDefault)
		if err != nil {
			return fmt.Errorf("route default url %s parse failed: %w", s.RouteDefault, err)
		}
		rp := createReverseProxy(routeDefaultURL, "", routeDefaultURL)
		var handler http.Handler
		if logger.IsDebug() {
			handler = http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
				logger.Debug(fmt.Sprintf("reverse proxy: incoming %s %s%s", req.Method, req.Host, req.URL))
				rp.ServeHTTP(rw, req)
			})
		} else {
			handler = rp
		}
		s.router.NotFoundHandler = handler
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

func (s *httpService) Run(ctx context.Context) {
	s.server.BaseContext = func(l net.Listener) context.Context {
		return ctx // need to track both client disconnect and app finalize
	}
	s.ctx = ctx
	if err := s.server.Serve(s.listener); err != http.ErrServerClosed {
		log.Println("main HTTP server failure: " + err.Error())
	}
}

func (s *httpService) Stop() {
	if err := s.server.Shutdown(s.ctx); err != nil {
		s.listener.Close()
		s.server.Close()
	}
}

func (s *httpService) registerHandlers() {
	s.router.HandleFunc("/api/check", corsHandler(checkHandler())).Methods("POST", "OPTIONS")
	s.router.HandleFunc("/api", corsHandler(queueNamesHandler()))
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}", queueAliasVar, wSIDVar), corsHandler(partitionHandler(s.queues))).
		Methods("POST", "OPTIONS")
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", queueAliasVar,
		wSIDVar, resourceNameVar), corsHandler(partitionHandler(s.queues))).
		Methods("POST", "PATCH", "OPTIONS").Headers()
}

func (s *httpService) registerReverseProxy(routes map[string]string, isRewrite bool) error {
	for from, to := range routes {
		toURL, err := url.Parse(to)
		if err != nil {
			return fmt.Errorf("target url %s parse failed: %w", to, err)
		}
		toBiteOut := ""
		if isRewrite {
			toBiteOut = from
		}
		s.reverseProxy.hostProxy[from] = createReverseProxy(toURL, toBiteOut, toURL)
		s.router.PathPrefix(from).Handler(s.reverseProxy)
		log.Printf("reverse proxy route registered: %s -> %s\n", from, to)
	}
	return nil
}

func (s *httpService) registerReverseProxyHandlers() (err error) {
	if err = s.registerReverseProxy(s.Routes, false); err != nil {
		return err
	}
	return s.registerReverseProxy(s.RoutesRewrite, true)
}

// pipeline.IService
func (s *acmeService) Prepare(work interface{}) error {
	return nil
}

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

func (s *acmeService) Stop() {
	if err := s.Shutdown(s.ctx); err != nil {
		s.Close()
	}
}
func (p *reverseProxyHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	s := strings.FieldsFunc(req.URL.Path, func(c rune) bool { return c == '/' })
	for i := 0; i < len(s); i++ {
		path := "/" + strings.Join(s[0:len(s)-i], "/")
		proxy, ok := p.hostProxy[path]
		if ok {
			proxy.ServeHTTP(res, req)
			break
		}
	}
}

func createReverseProxy(remote *url.URL, toBiteOut string, toPrepend *url.URL) *httputil.ReverseProxy {
	targetQuery := remote.RawQuery
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// default route: http://10.0.0.3:3000/not-found : https://alpha.dev.untill.ru/unknown/foo -> http://10.0.0.3:3000/not-found/unknown/foo
			// route        : /grafana=http://10.0.0.3:3000 : https://alpha.dev.untill.ru/grafana/foo -> http://10.0.0.3:3000/grafana/foo
			// route rewrite: /grafana-rewrite=http://10.0.0.3:3000/rewritten : https://alpha.dev.untill.ru/grafana-rewrite/foo -> http://10.0.0.3:3000/rewritten/foo
			newPath := req.URL.Path
			if len(toBiteOut) > 0 {
				newPath = strings.Replace(req.URL.Path, toBiteOut, "", 1)
			}
			if toPrepend != nil {
				newPath = toPrepend.Path + newPath
			}
			if logger.IsDebug() {
				logger.Debug(fmt.Sprintf("reverse proxy: incoming %s %s%s, redirecting to %s%s", req.Method, req.Host, req.URL, remote.Host, newPath))
			}
			req.URL.Path = newPath
			req.Host = remote.Host
			req.URL.Scheme = remote.Scheme
			req.URL.Host = remote.Host
			if targetQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = targetQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
			}
		},
	}
	return proxy
}
