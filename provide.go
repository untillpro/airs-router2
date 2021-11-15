/*
 * Copyright (c) 2021-present Sigma-Soft, Ltd.
 * Aleksei Ponomarev
 */

package router2

func Provide(rp RouterParams, urlMapping map[string]string, allowedHost string) (Service, error) {
	return Service{
		Port:             rp.RouterPort,
		WriteTimeout:     rp.RouterWriteTimeout,
		ReadTimeout:      rp.RouterReadTimeout,
		ConnectionsLimit: rp.RouterConnectionsLimit,
		ReverseProxy:     NewReverseProxy(urlMapping),
		AllowedHost:      allowedHost,
	}, nil
}
