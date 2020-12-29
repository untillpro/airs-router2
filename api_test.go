/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package router

import (
	"bytes"
	"context"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	ibus "github.com/untillpro/airs-ibus"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

func TestCheck(t *testing.T) {
	setUpHTTPOnly()
	defer services.StopAndReset(ctx)

	bodyReader := bytes.NewReader(nil)
	resp, err := http.Post("http://127.0.0.1:8822/api/check", "", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()
	respBodyBytes, err := ioutil.ReadAll(resp.Body)
	require.Nil(t, err)
	require.Equal(t, "ok", string(respBodyBytes))
	expectOKRespPlainText(t, resp)
}

func TestQueueNames(t *testing.T) {
	services.SetVerbose(false)
	ibusnats.DeclareTest(1)
	declare()
	ctx, err := services.ResolveAndStart()
	require.Nil(t, err, err)
	defer services.StopAndReset(ctx)

	bodyReader := bytes.NewReader(nil)
	resp, err := http.Post("http://127.0.0.1:8822/api", "", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()
	respBodyBytes, err := ioutil.ReadAll(resp.Body)
	require.Nil(t, err)
	require.Equal(t, `["airs-bp"]`, string(respBodyBytes))
	expectOKRespPlainText(t, resp)
}

func TestNoResource(t *testing.T) {
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		require.Empty(t, request.Resource)
		resp := ibus.CreateResponse(http.StatusOK, "ok")
		resp.ContentType = "text/plain"
		ibus.SendResponse(ctx, sender, resp)
	})
	godif.Require(&ibus.RequestHandler)
	godif.Require(&ibus.SendResponse)
	services.SetVerbose(false)
	currentQueueName = "airs-bp"
	airsBPPartitionsAmount = 1
	ibusnats.DeclareTest(1)
	declare()
	ctx, err := services.ResolveAndStart()
	require.Nil(t, err, err)
	defer services.StopAndReset(ctx)

	bodyReader := bytes.NewReader(nil)
	resp, err := http.Post("http://127.0.0.1:8822/api/airs-bp/1", "application/json", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()
	respBodyBytes, err := ioutil.ReadAll(resp.Body)
	require.Nil(t, err)
	require.Equal(t, "ok", string(respBodyBytes))
	expectOKRespPlainText(t, resp)
}

func Test404(t *testing.T) {
	setUpHTTPOnly()
	defer services.StopAndReset(ctx)

	bodyReader := bytes.NewReader(nil)
	resp, err := http.Post("http://127.0.0.1:8822/api/wrong", "", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func setUpHTTPOnly() {
	godif.ProvideSliceElement(&services.Services, &Service{
		Port:             defaultRouterPort,
		WriteTimeout:     defaultRouterWriteTimeout,
		ReadTimeout:      defaultRouterReadTimeout,
		ConnectionsLimit: defaultRouterConnectionsLimit,
	})
	var err error
	services.SetVerbose(false)
	if ctx, err = services.ResolveAndStart(); err != nil {
		panic(err)
	}
}
