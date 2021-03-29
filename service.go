/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */


package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/netutil"
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

// Start s.e.
func (s *Service) Start(ctx context.Context) (context.Context, error) {

	s.router = mux.NewRouter()

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
		BaseContext: func(l net.Listener) context.Context {
			return ctx // need to track both client disconnect and app finalize
		},
		Addr:         ":" + port,
		Handler:      s.router,
		ReadTimeout:  time.Duration(s.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(s.WriteTimeout) * time.Second,
	}

	s.registerHandlers(ctx)

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
}

func (s *Service) registerHandlers(ctx context.Context) {
	s.router.HandleFunc("/api/check", corsHandler(checkHandler())).Methods("POST", "OPTIONS")
	s.router.HandleFunc("/api", corsHandler(queueNamesHandler()))
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}", queueAliasVar, wSIDVar), corsHandler(partitionHandler(ctx))).
		Methods("POST", "OPTIONS")
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/]+}", queueAliasVar,
		wSIDVar, resourceNameVar), corsHandler(partitionHandler(ctx))).
		Methods("POST", "PATCH", "OPTIONS").Headers()
}
