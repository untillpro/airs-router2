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
	"strconv"
	"time"

	"github.com/gorilla/mux"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/net/netutil"
)

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
	return []interface{}{httpsService, acmeService}
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

	if len(s.HostTargetDefault) > 0 {
		hostTargetDefaultURL, err := url.Parse(s.HostTargetDefault)
		if err != nil {
			return fmt.Errorf("host target default target url %s parse failed: %w", s.HostTargetDefault, err)
		}
		s.router.NotFoundHandler = createReverseProxy(hostTargetDefaultURL)
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

func (s *httpService) registerReverseProxyHandlers() error {
	for host, target := range s.ReverseProxyMapping {
		remoteUrl, err := url.Parse(target)
		if err != nil {
			return fmt.Errorf("target url %s parse failed: %w", target, err)
		}
		s.reverseProxy.hostProxy[host] = createReverseProxy(remoteUrl)
		s.router.Handle(host, s.reverseProxy)
		log.Printf("reverse proxy route registered: %s -> %s\n", host, target)
	}
	return nil
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
