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
	"github.com/valyala/bytebufferpool"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/netutil"
)

// http -> return []interface{pipeline.IService(httpService)}, https ->  []interface{pipeline.IService(httpsService), pipeline.IService(acmeService)}
func Provide(ctx context.Context, rp RouterParams) []interface{} {
	httpService := httpService{
		RouterParams: rp,
		queues:       rp.QueuesPartitions,
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
	fs.BoolVar(&rp.RouterOnly, "ro", false, "Router only mode, no NATS connection")

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

func getReverseProxyURLs(routesURLs map[string]t, routes map[string]string, isRewrite bool) error {
	for from, to := range routes {
		if !strings.HasPrefix(from, "/") {
			return fmt.Errorf("%s reverse proxy url must have trailing slash", from)
		}
		targetURL, err := parseURL(to)
		if err != nil {
			return err
		}
		routesURLs[from] = t{
			targetURL,
			isRewrite,
		}
		logger.Info("reverse proxy route registered: ", from, " -> ", to)
	}
	return nil
}

func (s *httpService) registerHandlers() (err error) {
	routesURLs := map[string]t{}
	proxy := &httputil.ReverseProxy{Director: func(r *http.Request) {}}

	if err = getReverseProxyURLs(routesURLs, s.Routes, false); err != nil {

		return err
	}
	if err = getReverseProxyURLs(routesURLs, s.RoutesRewrite, true); err != nil {
		return err
	}
	var notFoundURL *url.URL
	if len(s.RouteDefault) > 0 {
		if notFoundURL, err = parseURL(s.RouteDefault); err != nil {
			return err
		}
		logger.Info("not found handler registered: ", s.RouteDefault)
	}
	s.router.MatcherFunc(func(req *http.Request, rm *mux.RouteMatch) bool {
		pathPrefix := bytebufferpool.Get()
		defer bytebufferpool.Put(pathPrefix)
		// route        : /grafana=http://10.0.0.3:3000 : https://alpha.dev.untill.ru/grafana/foo -> http://10.0.0.3:3000/grafana/foo
		// route rewrite: /grafana-rewrite=http://10.0.0.3:3000/rewritten : https://alpha.dev.untill.ru/grafana-rewrite/foo -> http://10.0.0.3:3000/rewritten/foo
		// default route: http://10.0.0.3:3000/not-found : https://alpha.dev.untill.ru/unknown/foo -> http://10.0.0.3:3000/not-found/unknown/foo
		pathParts := strings.Split(req.URL.Path, "/")
		for _, pathPart := range pathParts[1:] { // ignore first empty path part. URL must have a trailing slash (already checked)
			pathPrefix.WriteString("/")
			pathPrefix.WriteString(pathPart)
			toData, ok := routesURLs[pathPrefix.String()]
			if !ok {
				continue
			}
			targetPath := req.URL.Path
			if toData.isRewrite {
				targetPath = strings.Replace(req.URL.Path, pathPrefix.String(), toData.targetURL.Path, 1)
			}
			redirect(req, targetPath, toData.targetURL)
			rm.Handler = proxy
			return true
		}
		if notFoundURL != nil {
			// no match -> not found handler
			targetPath := notFoundURL.Path + req.URL.Path
			redirect(req, targetPath, notFoundURL)
			rm.Handler = proxy
			if logger.IsDebug() {
				logger.Debug(fmt.Sprintf("reverse proxy (not found handler): incoming %s %s%s", req.Method, req.Host, req.URL))
			}
			return true
		}
		return false
	}).Name("reverse proxy")
	s.router.HandleFunc("/api/check", corsHandler(checkHandler())).Methods("POST", "OPTIONS").Name("router check")
	s.router.HandleFunc("/api", corsHandler(queueNamesHandler())).Name("app names")
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", queueAliasVar,
		wSIDVar, resourceNameVar), corsHandler(partitionHandler(s.queues))).
		Methods("POST", "PATCH", "OPTIONS").Name("api")
	return nil
}

type t struct {
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
