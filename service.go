/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	ibusnats "github.com/untillpro/airs-ibusnats"
)

// Service s.e.
// if Service use port 443, it will be started safely with TLS and use Lets Encrypt certificate
// ReverseProxy create handlers for access to service inside security perimeter
// AllowedHost is needed for hostPolicy function, that controls for which domain the Manager will attempt to retrieve new certificates
type Service struct {
	RouterParams
	QueuePartitions ibusnats.QueuesPartitionsMap
	srvs            []interface{}
}

type reverseProxyHandler struct {
	hostProxy map[string]*httputil.ReverseProxy
}

type routerKeyType string

const routerKey = routerKeyType("router")

// Start s.e.
func (s *Service) Start(ctx context.Context) (context.Context, error) {
	s.srvs = Provide(ctx, s.RouterParams)
	for _, srvIntf := range s.srvs {
		// simulate pipeline.ServiceOperator
		srv := srvIntf.(interface {
			Prepare(work interface{}) error
			Run(ctx context.Context)
		})
		if err := srv.Prepare(nil); err != nil {
			return ctx, err
		}
		go srv.Run(ctx)
	}
	log.Println("Router started")
	return context.WithValue(ctx, routerKey, s), nil
}

// Stop s.e.
func (s *Service) Stop(ctx context.Context) {
	for _, srvIntf := range s.srvs {
		srv := srvIntf.(interface{ Stop() })
		srv.Stop()
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

func createReverseProxy(remote *url.URL) *httputil.ReverseProxy {
	targetQuery := remote.RawQuery
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
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
