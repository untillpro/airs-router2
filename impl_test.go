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
	parallel "github.com/untillpro/airs-icoreimpl/parallel"
)

func TestChunkedRespISections(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK}
		var chunksErrorRes *error
		rsi := parallel.NewResultSender()
		go func() {
			rsi.StartMapSection("articles", []string{"classifier", "2"})
			rsi.SendElement("id1", map[string]interface{}{
				"fld1": "fld1Val",
			})
			rsi.SendElement("id2", map[string]interface{}{
				"fld2": "fld2Val",
			})
			rsi.StartArraySection("arrSec", []string{"classifier", "4"})
			rsi.SendElement("", "arrEl1")
			rsi.SendElement("", "arrEl2")
			rsi.ObjectSection("obj", []string{"meta"}, map[string]interface{}{
				"total": 1,
			})
			rsi.StartMapSection("deps", []string{"classifier", "3"})
			rsi.SendElement("id3", map[string]interface{}{
				"fld3": "fld3Val",
			})
			rsi.SendElement("id4", map[string]interface{}{
				"fld4": "fld4Val",
			})
			close(rsi.Chunks)
		}()
		return res, rsi.Chunks, chunksErrorRes, nil
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `
	{
		"sections": [
		   {
			  "elements": {
				 "id1": {
					"fld1": "fld1Val"
				 },
				 "id2": {
					"fld2": "fld2Val"
				 }
			  },
			  "path": [
				 "classifier",
				 "2"
			  ],
			  "type": "articles"
		   },
		   {
			  "elements": [
				 "arrEl1",
				 "arrEl2"
			  ],
			  "path": [
				 "classifier",
				 "4"
			  ],
			  "type": "arrSec"
		   },
		   {
			  "elements": {
				 "total": 1
			  },
			  "path": [
				 "meta"
			  ]
		   },
		   {
			  "elements": {
				 "id3": {
					"fld3": "fld3Val"
				 },
				 "id4": {
					"fld4": "fld4Val"
				 }
			  },
			  "path": [
				 "classifier",
				 "3"
			  ],
			  "type": "deps"
		   }
		],
		"status": 200
	 }`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestChunkedRespISectionsError(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK}
		rsi := parallel.NewResultSender()
		var chunksErrorRes error
		go func() {
			rsi.StartMapSection("articles", []string{"classifier", "2"})
			rsi.SendElement("id1", map[string]interface{}{
				"fld1": "fld1Val",
			})
			chunksErrorRes = errors.New("test error")
			close(rsi.Chunks)
		}()
		return res, rsi.Chunks, &chunksErrorRes, nil
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `
	{
		"sections": [
		   {
			  "elements": {
				 "id1": {
					"fld1": "fld1Val"
				 }
			  },
			  "path": [
				 "classifier",
				 "2"
			  ],
			  "type": "articles"
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

func TestChunkedRespNoSections(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK}
		rsi := parallel.NewResultSender()
		var chunksErrorRes error
		close(rsi.Chunks)
		return res, rsi.Chunks, &chunksErrorRes, nil
	}
	chunkedResp(ctx, req, ibusReq, resp, 100000*time.Millisecond)

	expectedJSON := `{"status": 200}`

	expected := map[string]interface{}{}
	require.Nil(t, json.Unmarshal([]byte(expectedJSON), &expected))
	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))
	require.Equal(t, expected, actual)
}

func TestPanicOnConvertToISections(t *testing.T) {
	ctx := context.Background()
	req := &http.Request{Body: http.NoBody}
	ibusReq := &ibus.Request{}
	resp := httptest.NewRecorder()
	ibus.SendRequest = func(ctx context.Context, request *ibus.Request, timeout time.Duration) (res *ibus.Response, chunks <-chan []byte, chunksError *error, err error) {
		res = &ibus.Response{StatusCode: http.StatusOK}
		var chunksErrorRes error
		ch := make(chan []byte)
		go func() {
			ch <- []byte{255} // unknown bus packet type
			close(ch)
		}()
		return res, ch, &chunksErrorRes, nil
	}

	chunkedResp(ctx, req, ibusReq, resp, 1000*time.Millisecond)

	actual := map[string]interface{}{}
	require.Nil(t, json.Unmarshal(resp.Body.Bytes(), &actual))

	require.Equal(t, float64(500), actual["status"])
	require.NotEmpty(t, actual["errorDescription"])
	require.Equal(t, 2, len(actual))
}
