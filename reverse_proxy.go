/*
 * Copyright (c) 2022-present unTill Pro, Ltd.
 */

package router2

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
	logger "github.com/heeus/core-logger"
	"github.com/valyala/bytebufferpool"
)

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