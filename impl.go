/*
 * Copyright (c) 2019-present unTill Pro, Ltd. and Contributors
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	ibus "github.com/untillpro/airs-ibus"
	bus "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/gochips"
	"github.com/untillpro/godif/services"
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
	//Content-Type
	contentJSON = "application/json"
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
		queueRequest.PartitionNumber = int(queueRequest.WSID % int64(numberOfPartitions))
		chunkedResp(ctx, req, queueRequest, resp, ibus.DefaultTimeout)
	}
}

func getRespData(ctx context.Context, req *http.Request, queueRequest *ibus.Request, resp http.ResponseWriter, timeout time.Duration) (sections []byte, status int, errorDesc string, data []byte) {
	defer func() {
		if r := recover(); r != nil {
			errorDesc = fmt.Sprintf("%s\n%s", r, string(debug.Stack()))
			gochips.Error(errorDesc)
			status = http.StatusInternalServerError
			return
		}
	}()
	respFromInvoke, outChunks, outChunksErr, err := ibus.SendRequest(ctx, queueRequest, timeout)
	setContentType(resp, contentJSON)
	if err != nil {
		return nil, http.StatusInternalServerError, err.Error(), nil
	}
	if respFromInvoke == nil {
		return nil, http.StatusInternalServerError, "nil response from bus", nil
	}

	buf := bytes.NewBufferString("")
	if len(errorDesc) == 0 && outChunks != nil {
		sections := ibus.BytesToSections(outChunks, outChunksErr)
		resp.Header().Set("X-Content-Type-Options", "nosniff")
		sectionsOpened := false
		for iSection := range sections {
			if !sectionsOpened {
				buf.WriteString(`"sections":[`)
				sectionsOpened = true
			}
			buf.Write(sectionToJSON(iSection))
			buf.WriteString(",")
		}
		if sectionsOpened {
			buf.Truncate(buf.Len() - 1)
			buf.WriteString("]")
		}
	}
	if outChunksErr != nil && *outChunksErr != nil {
		errorDesc = (*outChunksErr).Error()
		gochips.Error("error in chunks: " + errorDesc)
		return buf.Bytes(), http.StatusInternalServerError, errorDesc, nil
	}
	return buf.Bytes(), respFromInvoke.StatusCode, "", respFromInvoke.Data
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

	sections, status, errorDesc, data := getRespData(ctx, req, queueRequest, resp, timeout)
	dataJSON := ""
	if status != http.StatusOK && len(data) != 0 {
		errorDesc = string(data)
		data = []byte{}
	} else {
		if len(data) > 0 && string(data) != "null" {
			dataJSONBytes, _ := json.Marshal(string(data)) // errors are impossible here
			dataJSON = string(dataJSONBytes)
		}
	}
	setContentType(resp, "application/json")
	resp.WriteHeader(http.StatusOK)

	buf := bytes.NewBufferString("{")
	if len(sections) > 0 {
		buf.Write(sections)
		buf.WriteString(",")
	}
	buf.WriteString(fmt.Sprintf(`"status":%d`, status))
	if len(errorDesc) > 0 {
		replacer := strings.NewReplacer("\n", "\\n", "\t", "\\t", "\r", "\\r")
		buf.WriteString(fmt.Sprintf(`,"errorDescription": "%s"`, replacer.Replace(errorDesc)))
	}
	if len(dataJSON) > 0 {
		buf.WriteString(fmt.Sprintf(`,"data":%s`, dataJSON))
	}
	buf.WriteString("}")
	if _, err := resp.Write(buf.Bytes()); err != nil {
		gochips.Error("can't write response part", err)
	}
}

// QueueNamesHandler returns registered queue names
func (s *Service) QueueNamesHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		keys := make([]string, 0, len(queueNumberOfPartitions))
		for k := range queueNumberOfPartitions {
			keys = append(keys, k)
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

// CheckHandler returns ok if server works
func (s *Service) CheckHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		resp.Write([]byte("ok"))
	}
}

func createRequest(reqMethod string, req *http.Request) (*ibus.Request, error) {
	vars := mux.Vars(req)
	var WSID string
	var ok bool
	if WSID, ok = vars[wSIDVar]; !ok {
		return nil, errors.New("WSID is missed")
	}
	// no need to check to err because of regexp in a handler
	WSIDNum, _ := strconv.ParseInt(WSID, 10, 64)
	return &ibus.Request{
		Method:  ibus.NameToHTTPMethod[reqMethod],
		QueueID: vars[queueAliasVar],
		WSID:    WSIDNum,
		Query:   req.URL.Query(),
		Header:  req.Header,
	}, nil
}

// RegisterHandlers s.e.
func (s *Service) RegisterHandlers(ctx context.Context) {
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
	var natsServers = flag.String("ns", bus.DefaultNATSHost, "The nats server URLs (separated by comma)")
	var routerPort = flag.Int("p", defaultRouterPort, "Server port")
	var routerWriteTimeout = flag.Int("wt", defaultRouterWriteTimeout, "Write timeout in seconds")
	var routerReadTimeout = flag.Int("rt", defaultRouterReadTimeout, "Read timeout in seconds")
	var routerConnectionsLimit = flag.Int("cl", defaultRouterConnectionsLimit, "Limit of incoming connections")

	flag.Parse()
	gochips.Info("nats: " + *natsServers)

	addHandlers()

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

func writeHeader(sec ibus.IDataSection) (res [][]byte) {
	if len(sec.Type()) > 0 {
		res = append(res, []byte(fmt.Sprintf(`"type":"%s"`, sec.Type())))
	}
	if len(sec.Path()) > 0 {
		buf := bytes.NewBufferString("")
		buf.WriteString(`"path":[`)
		for _, p := range sec.Path() {
			buf.WriteString(fmt.Sprintf(`"%s",`, p))
		}
		buf.Truncate(buf.Len() - 1)
		buf.WriteString("]")
		res = append(res, buf.Bytes())
	}
	return
}

func sectionToJSON(isec ibus.ISection) []byte {
	sections := [][]byte{}
	buf := bytes.NewBufferString("")
	switch sec := isec.(type) {
	case ibus.IArraySection:
		sections = writeHeader(sec)
		val, ok := sec.Next()
		if ok {
			buf.WriteString(`"elements":`)
			for ok {
				buf.Write(val)
				buf.WriteString(",")
				val, ok = sec.Next()
			}
			buf.Truncate(buf.Len() - 1)
			sections = append(sections, buf.Bytes())
		}
	case ibus.IObjectSection:
		sections = writeHeader(sec)
		if len(sec.Value()) > 0 {
			buf.WriteString(`"elements":`)
			buf.Write(sec.Value())
			sections = append(sections, buf.Bytes())
		}
	case ibus.IMapSection:
		sections = writeHeader(sec)
		name, val, ok := sec.Next()
		if ok {
			buf.WriteString(`"elements":{`)
			for ok {
				buf.WriteString(fmt.Sprintf(`"%s":`, name))
				buf.Write(val)
				buf.WriteString(",")
				name, val, ok = sec.Next()
			}
			buf.Truncate(buf.Len() - 1)
			buf.WriteString("}")
			sections = append(sections, buf.Bytes())
		}
	}
	buf = bytes.NewBufferString("")
	buf.WriteString("{")
	if len(sections) > 0 {
		for _, sec := range sections {
			buf.Write(sec)
			buf.WriteString(",")
		}
		buf.Truncate(buf.Len() - 1)
	}
	buf.WriteString("}")
	return buf.Bytes()
}
