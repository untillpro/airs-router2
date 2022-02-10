/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	logger "github.com/heeus/core-logger"
	ibus "github.com/untillpro/airs-ibus"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"github.com/valyala/bytebufferpool"
)

const (
	queueAliasVar                 = "queue-alias"
	wSIDVar                       = "partition-dividend"
	resourceNameVar               = "resource-name"
	bp3AppOwner                   = "app-owner"
	bp3AppName                    = "app-name"
	bp3BLOBID                     = "blobID"
	bp3PrincipalToken             = "principalToken"
	DefaultRouterPort             = 8822
	DefaultRouterConnectionsLimit = 10000
	//Timeouts should be greater than NATS timeouts to proper use in browser(multiply responses)
	DefaultRouterReadTimeout  = 15
	DefaultRouterWriteTimeout = 15
)

var (
	queueNamesJSON         []byte
	airsBPPartitionsAmount int                         = 100                 // changes in tests
	busTimeout             time.Duration               = ibus.DefaultTimeout // changes in tests
	onRequestCtxClosed     func()                      = nil                 // used in tests
	onAfterSectionWrite    func(w http.ResponseWriter) = nil                 // used in tests
)

func partitionHandler(queueNumberOfPartitions ibusnats.QueuesPartitionsMap) http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		if logger.IsDebug() {
			logger.Debug("serving ", req.Method, " ", req.URL.Path)
		}
		vars := mux.Vars(req)
		queueRequest, err := createRequest(req.Method, req)
		if err != nil {
			log.Println("failed to read body:", err)
			return
		}

		if len(queueNumberOfPartitions) > 0 {
			// note: Partition here is not used in BP3
			numberOfPartitions := queueNumberOfPartitions[vars[queueAliasVar]]
			queueRequest.PartitionNumber = int(queueRequest.WSID % int64(numberOfPartitions))
		}
		queueRequest.Resource = vars[resourceNameVar]

		// req's BaseContext is router service's context. See service.Start()
		// router app closing or client disconnected -> req.Context() is done
		// will create new cancellable context and cancel it if http section send is failed.
		// requestCtx.Done() -> SendRequest2 implementation will notify the handler that the consumer has left us
		requestCtx, cancel := context.WithCancel(req.Context())
		defer cancel() // to avoid context leak
		res, sections, secErr, err := ibus.SendRequest2(requestCtx, queueRequest, busTimeout)
		if err != nil {
			writeTextResponse(resp, err.Error(), http.StatusInternalServerError)
			return
		}

		if sections == nil {
			setContentType(resp, res.ContentType)
			resp.WriteHeader(res.StatusCode)
			writeResponse(resp, string(res.Data))
			return
		}
		writeSectionedResponse(requestCtx, resp, sections, secErr, cancel)
	}
}

func writeSectionedResponse(requestCtx context.Context, w http.ResponseWriter, sections <-chan ibus.ISection, secErr *error, onSendFailed func()) {
	ok := true
	var iSection ibus.ISection
	defer func() {
		if !ok {
			onSendFailed()
			// consume all pending sections or elems to avoid hanging on ibusnats side
			// normally should one pending elem or section because ibusnats implementation
			// will terminate on next elem or section because `onSendFailed()` actually closes the context
			switch t := iSection.(type) {
			case nil:
			case ibus.IObjectSection:
				t.Value()
			case ibus.IMapSection:
				for _, _, ok := t.Next(); ok; _, _, ok = t.Next() {
				}
			case ibus.IArraySection:
				for _, ok := t.Next(); ok; _, ok = t.Next() {
				}
			}
			for range sections {
			}
		}
	}()

	setContentType(w, "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if ok = writeResponse(w, "{"); !ok {
		return
	}
	sectionsOpened := false

	closer := ""
	// ctx done -> sections will be closed by ibusnats implementation
	for iSection = range sections {
		// possible: ctx is done but on select {sections<-section, <-ctx.Done()} write to sections channel is triggered.
		// ctx.Done() must have the priority
		if requestCtx.Err() != nil {
			break
		}

		if !sectionsOpened {
			if ok = writeResponse(w, `"sections":[`); !ok {
				return
			}
			closer = "]"
			sectionsOpened = true
		} else {
			if ok = writeResponse(w, ","); !ok {
				return
			}
		}
		if ok = writeSection(w, iSection); !ok {
			return
		}
		if onAfterSectionWrite != nil {
			// happens in tests
			onAfterSectionWrite(w)
		}
	}

	if requestCtx.Err() != nil {
		if onRequestCtxClosed != nil {
			onRequestCtxClosed()
		}
		log.Println("client disconnected during sections sending")
		return
	}

	if *secErr != nil {
		if sectionsOpened {
			closer = "],"
		}
		writeResponse(w, fmt.Sprintf(`%s"status":%d,"errorDescription":"%s"}`, closer, http.StatusInternalServerError, *secErr))
	} else {
		writeResponse(w, fmt.Sprintf(`%s}`, closer))
	}
}

func queueNamesHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		if _, err := resp.Write(queueNamesJSON); err != nil {
			log.Println("failed to write queues names", err)
		}
	}
}

func checkHandler() http.HandlerFunc {
	return func(resp http.ResponseWriter, req *http.Request) {
		if _, err := resp.Write([]byte("ok")); err != nil {
			log.Println("failed to write 'ok' response:", err)
		}
	}
}

func createRequest(reqMethod string, req *http.Request) (res ibus.Request, err error) {
	vars := mux.Vars(req)
	WSID := vars[wSIDVar]
	// no need to check to err because of regexp in a handler
	WSIDNum, _ := strconv.ParseInt(WSID, 10, 64)
	res = ibus.Request{
		Method:   ibus.NameToHTTPMethod[reqMethod],
		WSID:     WSIDNum,
		Query:    req.URL.Query(),
		Header:   req.Header,
		QueueID:  vars[queueAliasVar],
		AppQName: vars[bp3AppOwner] + "/" + vars[bp3AppName],
	}
	if req.Body != nil && req.Body != http.NoBody {
		res.Body, err = ioutil.ReadAll(req.Body)
	}
	return
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
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, Authorization")
}

func writeTextResponse(w http.ResponseWriter, msg string, code int) bool {
	setContentType(w, "text/plain")
	w.WriteHeader(code)
	return writeResponse(w, msg)
}

func writeUnauthorized(rw http.ResponseWriter) {
	writeTextResponse(rw, "not authorized", http.StatusUnauthorized)
}

func writeResponse(w http.ResponseWriter, data string) bool {
	if _, err := w.Write([]byte(data)); err != nil {
		stack := debug.Stack()
		log.Println("failed to write response:", err, "\n", string(stack))
		return false
	}
	w.(http.Flusher).Flush()
	return true
}

func setContentType(resp http.ResponseWriter, cType string) {
	resp.Header().Set("Content-Type", cType)
}

func writeSectionHeader(w http.ResponseWriter, sec ibus.IDataSection) bool {
	buf := bytebufferpool.Get()
	defer bytebufferpool.Put(buf)
	_, _ = buf.WriteString(fmt.Sprintf(`{"type":%q`, sec.Type())) // error impossible
	if len(sec.Path()) > 0 {
		_, _ = buf.WriteString(`,"path":[`) // error impossible
		for i, p := range sec.Path() {
			if i > 0 {
				_, _ = buf.WriteString(",") // error impossible
			}
			_, _ = buf.WriteString(fmt.Sprintf(`%q`, p)) // error impossible
		}
		_, _ = buf.WriteString("]") // error impossible
	}
	if !writeResponse(w, string(buf.Bytes())) {
		return false
	}
	return true
}

func writeSection(w http.ResponseWriter, isec ibus.ISection) bool {
	switch sec := isec.(type) {
	case ibus.IArraySection:
		if !writeSectionHeader(w, sec) {
			return false
		}
		isFirst := true
		closer := "}"
		// ctx.Done() is tracked by ibusnats implementation: writting to section elem channel -> read here, ctxdone -> close elem channel
		for val, ok := sec.Next(); ok; val, ok = sec.Next() {
			if isFirst {
				if !writeResponse(w, fmt.Sprintf(`,"elements":[%s`, string(val))) {
					return false
				}
				isFirst = false
				closer = "]}"
			} else {
				if !writeResponse(w, fmt.Sprintf(`,%s`, string(val))) {
					return false
				}
			}
		}
		if !writeResponse(w, closer) {
			return false
		}
	case ibus.IObjectSection:
		if !writeSectionHeader(w, sec) {
			return false
		}
		val := sec.Value()
		if !writeResponse(w, fmt.Sprintf(`,"elements":%s}`, string(val))) {
			return false
		}
	case ibus.IMapSection:
		if !writeSectionHeader(w, sec) {
			return false
		}
		isFirst := true
		closer := "}"
		// ctx.Done() is tracked by ibusnats implementation: writting to section elem channel -> read here, ctxdone -> close elem channel
		for name, val, ok := sec.Next(); ok; name, val, ok = sec.Next() {
			if isFirst {
				if !writeResponse(w, fmt.Sprintf(`,"elements":{%q:%s`, name, string(val))) {
					return false
				}
				isFirst = false
				closer = "}}"
			} else {
				if !writeResponse(w, fmt.Sprintf(`,%q:%s`, name, string(val))) {
					return false
				}
			}
		}
		if !writeResponse(w, closer) {
			return false
		}
	}
	return true
}
