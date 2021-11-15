/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"crypto/tls"
	"fmt"
	"golang.org/x/crypto/acme/autocert"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/netutil"
)

// Service s.e.
// if Service use port 44s, it will be started safely with TLS and use Lets Encrypt certificate
// ReverseProxy create handlers for access to service inside security perimeter
// AllowedHost is needed for hostPolicy function, that controls for which domain the Manager will attempt to retrieve new certificates
type Service struct {
	Port, WriteTimeout, ReadTimeout, ConnectionsLimit int
	router                                            *mux.Router
	server                                            *http.Server
	acmeServer                                        *http.Server
	listener                                          net.Listener
	ReverseProxy                                      *reverseProxyHandler
	AllowedHost                                       string
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
	s.listener, err = net.Listen("tcp", ":"+port)
	if err != nil {
		return ctx, err
	}

	if s.ConnectionsLimit > 0 {
		s.listener = netutil.LimitListener(s.listener, s.ConnectionsLimit)
	}

	if isProduction(s.Port) {
		err = s.startSecureService(ctx, port)
		return context.WithValue(ctx, routerKey, s), err
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
	s.registerReverseProxyHandlers()

	log.Println("Router started")
	go func() {
		if err := s.server.Serve(s.listener); err != nil {
			log.Println(err)
		}
	}()
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

func (s *Service) registerReverseProxyHandlers() {
	if s.ReverseProxy.hostProxy != nil {
		for path := range s.ReverseProxy.hostTarget {
			s.router.Handle(path, s.ReverseProxy)
		}
	}
}

func (p *reverseProxyHandler) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	host := req.Host
	path := req.URL.Path
	if proxy, ok := p.hostProxy[path]; ok {
		proxy.ServeHTTP(res, req)
		return
	}
	if target, ok := p.hostTarget[path]; ok {
		remoteUrl, err := url.Parse(target)
		if err != nil {
			log.Println("target parse fail:", err)
			return
		}
		proxy := p.createReverseProxy(remoteUrl)
		p.hostProxy[path] = proxy
		proxy.ServeHTTP(res, req)
		return
	}
	res.WriteHeader(http.StatusNotFound)
	res.Write([]byte("404: Not Found" + host + path))
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
		},
	}
	return proxy
}

func NewReverseProxy(urlMapping map[string]string) *reverseProxyHandler {
	return &reverseProxyHandler{urlMapping, make(map[string]*httputil.ReverseProxy)}
}

func isProduction(port int) bool {
	if port == httpsPort {
		return true
	}
	return false
}

func (s *Service) startSecureService(ctx context.Context, port string) (err error) {
	hostPolicy := func(ctx context.Context, host string) error {
		if host == s.AllowedHost {
			return nil
		}
		return fmt.Errorf("acme/autocert: only %s host is allowed", s.AllowedHost)
	}
	dataDir := "."
	crtMgr := &autocert.Manager{
		// Note: In test environment use connection to Stage
		// Client: &acme.Client{
		// 	DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory",
		// },
		Prompt:     autocert.AcceptTOS,
		HostPolicy: hostPolicy,
		Cache:      autocert.DirCache(dataDir),
	}
	s.server = &http.Server{
		BaseContext: func(l net.Listener) context.Context {
			return ctx // need to track both client disconnect and app finalize
		},
		Addr:         ":" + port,
		Handler:      s.router,
		ReadTimeout:  time.Duration(s.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.WriteTimeout) * time.Second,
		TLSConfig:    &tls.Config{GetCertificate: crtMgr.GetCertificate},
	}
	s.registerHandlers(ctx)
	s.registerReverseProxyHandlers()

	go func() {
		log.Printf("Starting HTTPS server on %s\n\n", s.server.Addr)
		if err := s.server.ServeTLS(s.listener, "", ""); err != nil {
			log.Fatalf("Service.ServeTLS() failed with %s", err)
		}
	}()

	s.acmeServer = &http.Server{
		Addr:         ":80",
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	// handle Lets Encrypt callback over 80 port - only port 80 allowed
	if crtMgr != nil {
		s.acmeServer.Handler = crtMgr.HTTPHandler(s.acmeServer.Handler)
	}

	go func() {
		log.Printf("Starting HTTP server on %s\n\n", ":80")
		if err := s.acmeServer.ListenAndServe(); err != nil {
			log.Fatalf("http Service failed with %s", err)
		}
	}()
	return err
}
