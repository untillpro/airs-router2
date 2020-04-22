package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	ibus "github.com/untillpro/airs-ibus"
)

var (
	elem1 = map[string]interface{}{"fld1": "fld1Val"}
	elem2 = []interface{}{"e1", "e2"}
	elem3 = map[string]interface{}{"total": 1}
)

func TestSectionedRespBasicUsage(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK, Data: []byte("payload")}
		var chunksErrorRes *error
		ch := make(chan []byte)
		rsi := ibus.NewResultSender(ch)
		go func() {
			rsi.ObjectSection("obj", []string{"meta"}, elem3)
			rsi.StartMapSection("secMap", []string{"2"})
			rsi.SendElement("id1", elem1)
			rsi.StartArraySection("secArr", []string{"3"})
			rsi.SendElement("id2", elem2)
			close(ch)
		}()
		return res, ch, chunksErrorRes, nil
	}
	chunkedResp(ctx, req, ibusReq, resp, 1000*time.Millisecond)

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
				"type": "secMap",
				"path": [
					"2"
				],
				"elements": {
					"id1": {
						"fld1": "fld1Val"
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
					"e2"
				]
		 	}
		],
		"status": 200,
		"data": "payload"
	}`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestSectionedRespSendResponseChunkedError(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK}
		ch := make(chan []byte)
		rsi := ibus.NewResultSender(ch)
		var chunksErrorRes error
		go func() {
			rsi.ObjectSection("obj", []string{"meta"}, map[string]interface{}{
				"total": 1,
			})
			chunksErrorRes = errors.New("test error")
			close(ch)
		}()
		return res, ch, &chunksErrorRes, nil
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

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
			 }
		],
		"status": 500,
		"errorDescription": "test error"
	 }`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestSectionedRespSendResponseError(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK}
		err = errors.New("test error")
		return
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `
	{
		"status": 500,
		"errorDescription": "test error"
	 }`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestSectionedRespErrorInDataField(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusInternalServerError, Data: []byte("test error")}
		return
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `
	{
		"status": 500,
		"errorDescription": "test error"
	 }`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestSectionedRespSendResponseNilResponse(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		return
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `
	{
		"status": 500,
		"errorDescription": "nil response from bus"
	 }`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestSectionedRespNoSections(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK}
		ch := make(chan []byte)
		var chunksErrorRes error
		close(ch)
		return res, ch, &chunksErrorRes, nil
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `{"status": 200}`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestSectionedRespPanic(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		panic("test panic")
	}

	chunkedResp(ctx, req, ibusReq, resp, 1000*time.Millisecond)

	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))

	require.Equal(t, float64(500), actual["status"])
	require.NotEmpty(t, actual["errorDescription"])
	require.Equal(t, 2, len(actual))
}
