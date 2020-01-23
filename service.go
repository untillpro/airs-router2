/*
 * Copyright (c) 2019-present unTill Pro, Ltd. and Contributors
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */

package main

import (
	"context"
	"github.com/gorilla/mux"
	"github.com/untillpro/gochips"
	"golang.org/x/net/netutil"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Service s.e.
type Service struct {
	Port, WriteTimeout, ReadTimeout, ConnectionsLimit int
	router                                            *mux.Router
	server                                            *http.Server
	listener                                          net.Listener
}

type routerKeyType string

const routerKey = routerKeyType("router")

func getService(ctx context.Context) *Service {
	return ctx.Value(routerKey).(*Service)
}

// Start s.e.
func (s *Service) Start(ctx context.Context) (context.Context, error) {
	s.router = mux.NewRouter()
	s.router.Use(JwtAuthentication)

	port := strconv.Itoa(s.Port)

	var err error
	s.listener, err = net.Listen("tcp", ":"+port)
	if err != nil {
		return ctx, nil
	}

	if s.ConnectionsLimit > 0 {
		s.listener = netutil.LimitListener(s.listener, s.ConnectionsLimit)
	}

	s.server = &http.Server{
		Addr:         ":" + port,
		Handler:      s.router,
		ReadTimeout:  time.Duration(s.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.WriteTimeout) * time.Second,
	}

	s.RegisterHandlers(ctx)

	gochips.Info("Router started")
	go func() {
		if err := s.server.Serve(s.listener); err != nil {
			gochips.Info(err)
		}
	}()
	return context.WithValue(ctx, routerKey, s), nil
}

// Stop s.e.
func (s *Service) Stop(ctx context.Context) {
	err := s.server.Shutdown(ctx)
	if err != nil {
		s.listener.Close()
		s.server.Close()
	}
}
