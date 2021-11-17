/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
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

	"golang.org/x/crypto/acme/autocert"

	"github.com/gorilla/mux"
	"golang.org/x/net/netutil"
)

// Service s.e.
// if Service use port 443, it will be started safely with TLS and use Lets Encrypt certificate
// ReverseProxy create handlers for access to service inside security perimeter
// AllowedHost is needed for hostPolicy function, that controls for which domain the Manager will attempt to retrieve new certificates
type Service struct {
	Port, WriteTimeout, ReadTimeout, ConnectionsLimit int
	router                                            *mux.Router
	server                                            *http.Server
	listener                                          net.Listener

	// used in airs-bp3
	acmeServer          *http.Server
	ReverseProxy        *reverseProxyHandler
	HTTP01ChallengeHost string
	HostTargetDefault   string
	CertDir             string
}

type reverseProxyHandler struct {
	// hostTarget dict must look like:
	// "/count":"http://192.168.1.1:8080/count",
	// "/metric":"http://192.168.1.1:8080/metric",
	// "/users":"http://192.168.1.1:8080/users"
	// and used for register path in multiplexer
	hostTarget map[string]string
	hostProxy  map[string]*httputil.ReverseProxy
}

type routerKeyType string

const routerKey = routerKeyType("router")

// Start s.e.
func (s *Service) Start(ctx context.Context) (context.Context, error) {

	s.router = mux.NewRouter()

	port := strconv.Itoa(s.Port)

	var err error
	if s.listener, err = net.Listen("tcp", ":"+port); err != nil {
		return ctx, err
	}

	if s.ConnectionsLimit > 0 {
		s.listener = netutil.LimitListener(s.listener, s.ConnectionsLimit)
	}

	s.server = &http.Server{
		BaseContext: func(l net.Listener) context.Context {
			return ctx // need to track both client disconnect and app finalize
		},
		Addr:         ":" + port,
		Handler:      s.router,
		ReadTimeout:  time.Duration(s.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.WriteTimeout) * time.Second,
	}

	s.registerHandlers(ctx)
	if s.ReverseProxy != nil {
		if err = s.registerReverseProxyHandlers(); err != nil {
			return ctx, err
		}
	}
	if len(s.HostTargetDefault) > 0 {
		hostTargetDefaultURL, err := url.Parse(s.HostTargetDefault)
		if err != nil {
			return ctx, fmt.Errorf("host target default target url %s parse failed: %w", s.HostTargetDefault, err)
		}
		log.Println("host target default:" + hostTargetDefaultURL.String())
		notFoundHandler := s.ReverseProxy.createReverseProxy(hostTargetDefaultURL)
		s.router.NotFoundHandler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			log.Printf("not found handler: %s %s, %s, %s, %s\n", r.Method, r.Host, r.RemoteAddr, r.RequestURI, r.URL.String())
			notFoundHandler.ServeHTTP(rw, r)
		})

	}

	if s.Port == HTTPSPort {
		s.startSecureService()
	} else {
		go func() {
			if err := s.server.Serve(s.listener); err != http.ErrServerClosed {
				log.Fatalf("main HTTP server failure: %s", err.Error())
			}
		}()
	}

	log.Println("Router started")
	return context.WithValue(ctx, routerKey, s), nil
}

// Stop s.e.
func (s *Service) Stop(ctx context.Context) {
	if err := s.server.Shutdown(ctx); err != nil {
		s.listener.Close()
		s.server.Close()
	}
	if s.acmeServer != nil {
		if err := s.acmeServer.Shutdown(ctx); err != nil {
			s.acmeServer.Close()
		}
	}
}

func (s *Service) registerHandlers(ctx context.Context) {
	s.router.HandleFunc("/api/check", corsHandler(checkHandler())).Methods("POST", "OPTIONS")
	s.router.HandleFunc("/api", corsHandler(queueNamesHandler()))
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}", queueAliasVar, wSIDVar), corsHandler(partitionHandler(ctx))).
		Methods("POST", "OPTIONS")
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/.]+}", queueAliasVar,
		wSIDVar, resourceNameVar), corsHandler(partitionHandler(ctx))).
		Methods("POST", "PATCH", "OPTIONS").Headers()
}

func (s *Service) registerReverseProxyHandlers() error {
	for host, target := range s.ReverseProxy.hostTarget {
		remoteUrl, err := url.Parse(target)
		if err != nil {
			return fmt.Errorf("target url %s parse failed: %w", target, err)
		}
		s.ReverseProxy.hostProxy[host] = s.ReverseProxy.createReverseProxy(remoteUrl)
	}
	for host, path := range s.ReverseProxy.hostTarget {
		s.router.Handle(host, s.ReverseProxy)
		log.Printf("reverse proxy route registered: %s -> %s\n", host, path)
	}

	return nil
}

func (p *reverseProxyHandler) ServeHTTP(res http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	proxy := p.hostProxy[path]
	log.Printf("reverse proxy request: %s %s, %s, %s, %s\n", r.Method, r.Host, r.RemoteAddr, r.RequestURI, r.URL.String())
	proxy.ServeHTTP(res, r)
}

func (p *reverseProxyHandler) createReverseProxy(remote *url.URL) *httputil.ReverseProxy {
	targetQuery := remote.RawQuery
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.Host = remote.Host
			req.URL.Scheme = remote.Scheme
			req.URL.Host = remote.Host
			req.URL.Path = remote.Path
			if targetQuery == "" || req.URL.RawQuery == "" {
				req.URL.RawQuery = targetQuery + req.URL.RawQuery
			} else {
				req.URL.RawQuery = targetQuery + "&" + req.URL.RawQuery
			}
			log.Printf("reverse proxy request %s, %s, %s, %s", targetQuery, req.Host, req.URL.String(), req.URL.RawQuery)
		},
	}
	return proxy
}

func NewReverseProxy(urlMapping map[string]string) *reverseProxyHandler {
	return &reverseProxyHandler{urlMapping, make(map[string]*httputil.ReverseProxy)}
}

func (s *Service) startSecureService() {
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
		HostPolicy: autocert.HostWhitelist(s.HTTP01ChallengeHost),
		Cache:      autocert.DirCache(s.CertDir),
	}
	s.server.TLSConfig = &tls.Config{
		GetCertificate: crtMgr.GetCertificate,
	}
	go func() {
		log.Printf("Starting HTTPS server on %s\n", s.server.Addr)
		if err := s.server.ServeTLS(s.listener, "", ""); err != http.ErrServerClosed {
			log.Println("Service.ServeTLS() failure: ", err.Error())
		}
	}()

	s.acmeServer = &http.Server{
		Addr:         ":80",
		ReadTimeout:  DefaultACMEServerReadTimeout,
		WriteTimeout: DefaultACMEServerWriteTimeout,
	}

	// handle Lets Encrypt callback over 80 port - only port 80 allowed
	s.acmeServer.Handler = crtMgr.HTTPHandler(s.acmeServer.Handler)
	// s.acmeServer.Handler = http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
	// 	log.Printf("acme server request: %s %s, %s, %s, %s\n", r.Method, r.Host, r.RemoteAddr, r.RequestURI, r.URL.String())
	// 	crtMgr.HTTPHandler(nil).ServeHTTP(rw, r)
	// })

	go func() {
		log.Println("Starting ACME HTTP server on :80")
		if err := s.acmeServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Println("ACME HTTP server failure: ", err.Error())
		}
	}()
}
