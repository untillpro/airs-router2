package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	ibus "github.com/untillpro/airs-ibus"
)

var (
	elem1  = map[string]interface{}{"fld1": "fld1Val"}
	elem11  = map[string]interface{}{"fld2": "fld2Val"}
	elem21 = "e1"
	elem22 = "e2"
	elem3  = map[string]interface{}{"total": 1}
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
			rsi.SendElement("id2", elem11)
			rsi.StartArraySection("secArr", []string{"3"})
			rsi.SendElement("", elem21)
			rsi.SendElement("", elem22)
			close(ch)
		}()
		return res, ch, chunksErrorRes, nil
	}
	processResponse(ctx, req, ibusReq, resp, 1000*time.Millisecond)

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
					},
					"id2": {
						"fld2": "fld2Val"
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
		"status": 200
	}`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
	require.Equal(t, http.StatusOK, resp.Code)
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
	processResponse(ctx, req, ibusReq, resp, 100000*time.Millisecond)

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
	require.Equal(t, http.StatusOK, resp.Code)
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
	processResponse(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	require.Equal(t, "test error", string(resp.Body.Bytes()))
	require.Equal(t, http.StatusInternalServerError, resp.Code)
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
	processResponse(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	require.Equal(t, "test error", string(resp.Body.Bytes()))
	require.Equal(t, http.StatusInternalServerError, resp.Code)
}

func TestSectionedRespSendResponseNilResponse(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		return
	}
	processResponse(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	require.Equal(t, "nil response from bus", string(resp.Body.Bytes()))
	require.Equal(t, http.StatusInternalServerError, resp.Code)

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
	processResponse(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `{"status": 200}`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
	require.Equal(t, http.StatusOK, resp.Code)
}

func TestSectionedRespPanic(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		panic("test panic")
	}

	processResponse(ctx, req, ibusReq, resp, 1000*time.Millisecond)

	fmt.Println(string(resp.Body.Bytes()))
	require.Contains(t, string(resp.Body.Bytes()), "test panic" )
	require.Equal(t, http.StatusInternalServerError, resp.Code)
}

func TestMapSectionFailures (t*testing.T) {
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
			rsi.StartMapSection("secMap", []string{"2"})
			rsi.SendElement("id1", elem1)
			close(ch)
		}()
		return res, ch, chunksErrorRes, nil
	}
	processResponse(ctx, req, ibusReq, resp, 1000*time.Millisecond)

	expectedJSON := `
	{
		"sections": [
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
			}
		],
		"status": 200
	}`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
	require.Equal(t, http.StatusOK, resp.Code)


}
