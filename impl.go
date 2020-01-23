/*
 * Copyright (c) 2019-present unTill Pro, Ltd. and Contributors
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/gorilla/mux"
	ibus "github.com/untillpro/airs-ibus"
	bus "github.com/untillpro/airs-ibusnats"
	config "github.com/untillpro/airs-iconfigcon"
	"github.com/untillpro/gochips"
	"github.com/untillpro/godif/services"
	"io/ioutil"
	"net/http"
	"strconv"
	"time"
)

const (
	//Gorilla mux params
	queueAliasVar   = "queue-alias"
	wSIDVar         = "partition-dividend"
	resourceNameVar = "resource-name"
	contentType     = "Content-Type"
	//Settings
	defaultRouterPort             = 8822
	defaultRouterConnectionsLimit = 10000
	//Timeouts should be greater than NATS timeouts to proper use in browser(multiply responses)
	defaultRouterReadTimeout  = 15
	defaultRouterWriteTimeout = 15
	chunkSize                 = 100000
	//Content-Type
	contentJSON      = "application/json"
	contentPlainText = "plain/text"
)

var queueNumberOfPartitions = make(map[string]int)

// PartitionedHandler handle partitioned requests
func (s *Service) PartitionedHandler(ctx context.Context) http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		vars := mux.Vars(req)
		var ok bool
		var numberOfPartitions int
		if numberOfPartitions, ok = queueNumberOfPartitions[vars[queueAliasVar]]; !ok {
			writeTextResponse(resp, "can't find queue for alias: "+vars[queueAliasVar], http.StatusBadRequest)
			return
		}
		queueRequest, err := createRequest(req.Method, req)
		if err != nil {
			writeTextResponse(resp, err.Error(), http.StatusBadRequest)
			return
		}
		queueRequest.Resource = vars[resourceNameVar]
		if queueRequest.WSID == 0 {
			writeTextResponse(resp, "partition dividend in partitioned bus must be not 0", http.StatusBadRequest)
			return
		}
		queueRequest.PartitionNumber = int(queueRequest.WSID % int64(numberOfPartitions))
		if req.Body != nil && req.Body != http.NoBody {
			var rawJSON json.RawMessage
			decoder := json.NewDecoder(req.Body)
			err := decoder.Decode(&rawJSON)
			if err != nil {
				writeTextResponse(resp, "can't read request body: "+err.Error(), http.StatusBadRequest)
				return
			}
			queueRequest.Body = rawJSON
		}
		chunkedResp(ctx, req, queueRequest, resp, ibus.DefaultTimeout)
	}
}

func chunkedResp(ctx context.Context, req *http.Request, queueRequest *ibus.Request, resp http.ResponseWriter, timeout time.Duration) {
	if req.Body != nil && req.Body != http.NoBody {
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			gochips.Error(err)
			writeTextResponse(resp, "can't read request body: "+string(body), http.StatusBadRequest)
			return
		}
		queueRequest.Body = body
	}
	respFromInvoke, outChunks, outChunksErr, err := ibus.SendRequest(ctx, queueRequest, timeout)
	if err != nil {
		writeTextResponse(resp, err.Error(), http.StatusInternalServerError)
		return
	}
	if respFromInvoke == nil {
		writeTextResponse(resp, "nil response from bus", http.StatusInternalServerError)
		return
	}
	if respFromInvoke.ContentType != "" {
		setContentType(resp, respFromInvoke.ContentType)
	} else {
		setContentType(resp, contentJSON)
	}
	if outChunks != nil {
		for respPart := range outChunks {
			if len(respPart) == 0 {
				*outChunksErr = errors.New("empty response from server")
				break
			}
			if respPart[0] == 0 {
				if len(respPart) < 2 {
					setContentType(resp, contentPlainText)
					if _, err := resp.Write([]byte("there is no chunks for given range")); err != nil {
						writeTextResponse(resp, err.Error(), http.StatusInternalServerError)
						return
					}
					break
				}
				respPart = respPart[1:]
			}
			resp.Header().Set("X-Content-Type-Options", "nosniff")
			if _, err := resp.Write(respPart); err != nil {
				setContentType(resp, contentPlainText)
				writeTextResponse(resp, err.Error(), http.StatusInternalServerError)
				return
			}
		}

		if outChunksErr != nil && *outChunksErr != nil {
			setContentType(resp, contentPlainText)
			respFromInvoke.Data = []byte((*outChunksErr).Error())
			resp.WriteHeader(http.StatusInternalServerError)
			if _, err := resp.Write(respFromInvoke.Data); err != nil {
				writeTextResponse(resp, err.Error(), http.StatusInternalServerError)
			}
		}
	} else {
		if _, err := resp.Write(respFromInvoke.Data); err != nil {
			writeTextResponse(resp, err.Error(), http.StatusInternalServerError)
		}
	}
}

// QueueNamesHandler returns registered queue names
func (s *Service) QueueNamesHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		keys := make([]string, len(queueNumberOfPartitions))
		i := 0
		for k := range queueNumberOfPartitions {
			keys[i] = k
			i++
		}
		marshaled, err := json.Marshal(keys)
		if err != nil {
			writeTextResponse(resp, "can't marshal queue aliases", http.StatusBadRequest)
		}
		_, err = fmt.Fprintf(resp, string(marshaled))
		if err != nil {
			writeTextResponse(resp, "can't write response", http.StatusBadRequest)
		}
	}
}

// Check returns ok if server works
func (s *Service) CheckHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		resp.Write([]byte("ok"))
	}
}

func createRequest(reqMethod string, req *http.Request) (*ibus.Request, error) {
	vars := mux.Vars(req)
	WSID := vars[wSIDVar]
	WSIDNum, err := strconv.ParseInt(WSID, 10, 64)
	if err != nil {
		return nil, errors.New("wrong partition dividend " + WSID)
	}
	return &ibus.Request{
		Method:      ibus.NameToHTTPMethod[reqMethod],
		QueueID:     vars[queueAliasVar],
		WSID:        WSIDNum,
		Query:       req.URL.Query(),
		Attachments: map[string]string{},
		Header:      req.Header,
	}, nil
}

// RegisterHandlers s.e.
func (s *Service) RegisterHandlers(ctx context.Context) {
	//Auth
	s.router.HandleFunc("/api/user/new", corsHandler(s.CreateAccount(ctx))).Methods("POST", "OPTIONS")
	s.router.HandleFunc("/api/user/login", corsHandler(s.Authenticate(ctx))).Methods("POST", "OPTIONS")
	//Auth
	s.router.HandleFunc("/api/check", corsHandler(s.CheckHandler())).Methods("POST", "OPTIONS")
	s.router.HandleFunc("/api", corsHandler(s.QueueNamesHandler()))
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}", queueAliasVar, wSIDVar), corsHandler(s.PartitionedHandler(ctx))).
		Methods("POST", "OPTIONS")
	s.router.HandleFunc(fmt.Sprintf("/api/{%s}/{%s:[0-9]+}/{%s:[a-zA-Z_/]+}", queueAliasVar,
		wSIDVar, resourceNameVar), corsHandler(s.PartitionedHandler(ctx))).
		Methods("POST", "PATCH", "OPTIONS")
}

func addHandlers() {
	queueNumberOfPartitions["airs-bp"] = 100
}

func main() {
	var consulHost = flag.String("ch", config.DefaultConsulHost, "Consul server URL")
	var consulPort = flag.Int("cp", config.DefaultConsulPort, "Consul port")
	var natsServers = flag.String("ns", bus.DefaultNATSHost, "The nats server URLs (separated by comma)")
	var routerPort = flag.Int("p", defaultRouterPort, "Server port")
	var routerWriteTimeout = flag.Int("wt", defaultRouterWriteTimeout, "Write timeout in seconds")
	var routerReadTimeout = flag.Int("rt", defaultRouterReadTimeout, "Read timeout in seconds")
	var routerConnectionsLimit = flag.Int("cl", defaultRouterConnectionsLimit, "Limit of incoming connections")

	flag.Parse()
	gochips.Info("nats: " + *natsServers)

	addHandlers()

	config.Declare(config.Service{Host: *consulHost, Port: uint16(*consulPort)})
	bus.Declare(bus.Service{NATSServers: *natsServers, Queues: queueNumberOfPartitions})
	Declare(Service{Port: *routerPort, WriteTimeout: *routerWriteTimeout, ReadTimeout: *routerReadTimeout,
		ConnectionsLimit: *routerConnectionsLimit})

	err := services.Run()
	if err != nil {
		gochips.Info(err)
	}
}

func corsHandler(h http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setupResponse(w)
		if r.Method == "OPTIONS" {
			return
		}
		h.ServeHTTP(w, r)
	}
}

func setupResponse(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, PATCH")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")
}

func writeTextResponse(w http.ResponseWriter, msg string, code int) {
	w.WriteHeader(code)
	w.Header().Set("Content-Type", "text/plain")
	_, err := w.Write([]byte(msg))
	if err != nil {
		gochips.Error(err)
	}
}

func setContentType(resp http.ResponseWriter, cType string) {
	resp.Header().Set(contentType, cType)
}
