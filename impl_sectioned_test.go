/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	ibus "github.com/untillpro/airs-ibus"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

var (
	elem1       = map[string]interface{}{"fld1": "fld1Val"}
	elem11      = map[string]interface{}{"fld2": `哇"呀呀`}
	elem21      = "e1"
	elem22      = `哇"呀呀`
	elem3       = map[string]interface{}{"total": 1}
	ctx         context.Context
	cancel      context.CancelFunc
	initialArgs []string
)

func TestSectionedBasic(t *testing.T) {
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		require.Equal(t, "test body", string(request.Body))
		require.Equal(t, ibus.HTTPMethodPOST, request.Method)
		require.Equal(t, 0, request.PartitionNumber)
		require.Equal(t, int64(1), request.WSID)
		require.Equal(t, "somefunc", request.Resource)
		require.Equal(t, 0, len(request.Attachments))
		require.Equal(t, map[string][]string{
			"Accept-Encoding": {"gzip"},
			"Content-Length":  {"9"}, // len("test body")
			"Content-Type":    {"application/json"},
			"User-Agent":      {"Go-http-client/1.1"},
		}, request.Header)
		require.Equal(t, 0, len(request.Query))
		require.Equal(t, "airs-bp", request.QueueID)

		rs := ibus.SendParallelResponse2(ctx, sender)
		rs.ObjectSection("obj", []string{"meta"}, elem3)
		rs.StartMapSection(`哇"呀呀Map`, []string{`哇"呀呀`, "21"})
		rs.SendElement("id1", elem1)
		rs.SendElement(`哇"呀呀2`, elem11)
		rs.StartArraySection("secArr", []string{"3"})
		rs.SendElement("", elem21)
		rs.SendElement("", elem22)
		rs.Close(errors.New("test error"))
	})

	setUp()
	defer tearDown()

	body := []byte("test body")
	bodyReader := bytes.NewReader(body)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1/somefunc", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()

	expectedJSON := `
		{
			"sections": [
			   {
				  "elements": {
					 "total": 1
				  },
				  "path": [
					 "meta"
				  ],
				  "type": "obj"
			   },
				{
					"type": "哇\"呀呀Map",
					"path": [
						"哇\"呀呀",
						"21"
					],
					"elements": {
						"id1": {
							"fld1": "fld1Val"
						},
						"哇\"呀呀2": {
							"fld2": "哇\"呀呀"
						}
					}
				},
				{
					"type": "secArr",
					"path": [
						"3"
					],
					"elements": [
						"e1",
						"哇\"呀呀"
					]
			 	}
			],
			"status": 500,
			"errorDescription": "test error"
		}`
	expectJSONBody(t, expectedJSON, resp.Body)
	expectOKRespJSON(t, resp)
}

func TestSimpleOKSectionedResponse(t *testing.T) {
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		rs := ibus.SendParallelResponse2(ctx, sender)
		rs.Close(nil)
	})

	setUp()
	defer tearDown()

	body := []byte("")
	bodyReader := bytes.NewReader(body)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1/somefunc", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()

	expectedJSON := `{}`

	expectJSONBody(t, expectedJSON, resp.Body)
	expectOKRespJSON(t, resp)
}

func TestSectionedSendResponseError(t *testing.T) {
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		// no response -> nats timeout on requester side
	})

	setUp()
	defer tearDown()

	busTimeout = 100 * time.Millisecond

	body := []byte("")
	bodyReader := bytes.NewReader(body)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1/somefunc", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()

	respBodyBytes, err := ioutil.ReadAll(resp.Body)
	require.Nil(t, err)
	require.Equal(t, "first response read failed: Timeout expired", string(respBodyBytes))
	expect500RespPlainText(t, resp)
}

func TestHandlerPanic(t *testing.T) {
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		panic("test panic")
	})

	setUp()
	defer tearDown()

	busTimeout = 100 * time.Millisecond

	body := []byte("")
	bodyReader := bytes.NewReader(body)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1/somefunc", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()

	respBodyBytes, err := ioutil.ReadAll(resp.Body)
	require.Nil(t, err)
	require.Contains(t, string(respBodyBytes), "test panic")
	expect500RespPlainText(t, resp)
}

func TestStopReadSectionsOnClientDisconnect(t *testing.T) {
	ch := make(chan struct{})
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		rs := ibus.SendParallelResponse2(ctx, sender)
		rs.StartMapSection("secMap", []string{"2"})   // sent, read by router
		require.Nil(t, rs.SendElement("id1", elem1))  // sent, read by router
		<-ch                                          // wait for client disconnect
		require.Nil(t, rs.SendElement("id2", elem11)) // sent but there is nobody to read
		rs.StartMapSection("secMap", []string{"2"})   // sent but there is nobody to read
		require.Nil(t, rs.SendElement("id3", elem11)) // sent but there is nobody to read
		rs.Close(nil)                                 // sent but there is nobody to read
		ch <- struct{}{}
	})

	setUp()
	defer tearDown()

	// send request
	body := []byte("")
	bodyReader := bytes.NewReader(body)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1/somefunc", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()

	// read currently available sections
	entireResp := []byte{}
	for string(entireResp) != `{"sections":[{"type":"secMap","path":["2"],"elements":{"id1":{"fld1":"fld1Val"}` {
		buf := make([]byte, 512)
		n, err := resp.Body.Read(buf)
		require.Nil(t, err)
		entireResp = append(entireResp, buf[:n]...)
	}
	// now handler is stopped on <-ch

	// client closes the connection -> ibusnats should stop read from NATS and close `sections` channel
	// router sees `sections` is closed an finishes handling the request
	// note: router does not checks ctx.Done(). Cancellation condition - `sections` closed only.
	// note: ctx.Done() is checked by ibusnats only
	resp.Body.Close()

	// signal for Handler to continue sending sections.
	ch <- struct{}{}

	// wait for handler to send further sections. That should do nothing because ibusnats unsubscribed from the NATS inbox already
	<-ch
}

func TestStopReadSectionsOnContextDone(t *testing.T) {
	ch := make(chan struct{})
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		rs := ibus.SendParallelResponse2(ctx, sender)
		rs.StartMapSection("secMap", []string{"2"})   // sent, read by router
		require.Nil(t, rs.SendElement("id1", elem1))  // sent, read by router
		<-ch                                          // wait for context done
		require.Nil(t, rs.SendElement("id2", elem11)) // sent but there is nobody to read
		rs.StartMapSection("secMap", []string{"2"})   // sent but there is nobody to read
		require.Nil(t, rs.SendElement("id3", elem11)) // sent but there is nobody to read
		rs.Close(nil)                                 // sent but there is nobody to read
		ch <- struct{}{}
	})

	setUp()
	defer tearDown()

	body := []byte("")
	bodyReader := bytes.NewReader(body)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1/somefunc", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()

	// read until sec + 2 elems are received to ensure router read out from NATS currently available sections
	// further sections will be sent after ch <- struct{}{}
	entireResp := []byte{}
	for string(entireResp) != `{"sections":[{"type":"secMap","path":["2"],"elements":{"id1":{"fld1":"fld1Val"}` {
		buf := make([]byte, 512)
		n, err := resp.Body.Read(buf)
		require.Nil(t, err)
		entireResp = append(entireResp, buf[:n]...)
	}

	// context closed -> ibusnats should stop read from NATS and close `sections` channel
	// router sees `sections` is closed an finishes handling the request
	// note: router does not checks ctx.Done(). Cancellation condition - `sections` closed only.
	// note: ctx.Done() is checked by ibusnats only
	cancel()

	// signal for Handler to continue sending sections.
	ch <- struct{}{}

	// wait for handler to send further sections. That should do nothing because ibusnats unsubscribed from the NATS inbox already
	<-ch
}

func TestFaileToWriteRespone(t *testing.T) {
	ch := make(chan struct{})
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		rs := ibus.SendParallelResponse2(ctx, sender)
		rs.StartMapSection("secMap", []string{"2"})
		require.Nil(t, rs.SendElement("id1", elem1))
		<-ch
		rs.Close(nil)
	})
	onResponseWriteFailed = func() {
		ch <- struct{}{}
	}

	setUp()
	defer tearDown()

	body := []byte("")
	bodyReader := bytes.NewReader(body)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1/somefunc", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()

	// read frist chunk. Normally it is "{"
	buf := make([]byte, 512)
	n, err := resp.Body.Read(buf)
	require.Nil(t, err)
	require.True(t, n > 0)

	// disconnect the client.
	resp.Body.Close()

	ch <- struct{}{} // send more elements. Router will receive according bus packets but will fail to write it

	// wait for fail to write response
	<-ch
}

func expect500RespPlainText(t *testing.T, resp *http.Response) {
	expectResp(t, resp, "text/plain", http.StatusInternalServerError)
}

func expectOKRespPlainText(t *testing.T, resp *http.Response) {
	expectResp(t, resp, "text/plain", http.StatusOK)
}

func expectResp(t *testing.T, resp *http.Response, contentType string, statusCode int) {
	require.Equal(t, statusCode, resp.StatusCode)
	require.Contains(t, resp.Header["Content-Type"][0], contentType, resp.Header)
	require.Equal(t, []string{"*"}, resp.Header["Access-Control-Allow-Origin"])
	require.Equal(t, []string{"true"}, resp.Header["Access-Control-Allow-Credentials"])
	require.Equal(t, []string{"POST, GET, OPTIONS, PUT, PATCH"}, resp.Header["Access-Control-Allow-Methods"])
	require.Equal(t, []string{"Accept, Content-Type, Content-Length, Accept-Encoding, Authorization"}, resp.Header["Access-Control-Allow-Headers"])
}

func expectOKRespJSON(t *testing.T, resp *http.Response) {
	expectResp(t, resp, "application/json", http.StatusOK)
}

func expectJSONBody(t *testing.T, expectedJSON string, body io.Reader) {
	respBody, err := ioutil.ReadAll(body)
	require.Nil(t, err, err)
	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(respBody, &actual), string(respBody))
	require.Equal(t, expected, actual)
}

func tearDown() {
	services.StopAndReset(ctx)
	currentQueueName = ""
	airsBPPartitionsAmount = 100
	os.Args = initialArgs
	busTimeout = ibus.DefaultTimeout
	onResponseWriteFailed = nil
}

func setUp() {
	currentQueueName = "airs-bp"
	airsBPPartitionsAmount = 1
	ibusnats.DeclareTest(1)
	declare()
	godif.Require((&ibus.RequestHandler))
	godif.Require(&ibus.SendParallelResponse2)
	godif.Require(&ibus.SendResponse)

	var err error
	ctx, cancel = context.WithCancel(context.Background())
	if ctx, err = services.ResolveAndStartCtx(ctx); err != nil {
		panic(err)
	}
	services.SetVerbose(false)
}
