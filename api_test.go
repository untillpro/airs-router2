/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package router2

import (
	"bytes"
	"context"
	"io/ioutil"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	ibus "github.com/untillpro/airs-ibus"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

func TestCheck(t *testing.T) {
	setUpHTTPOnly()
	defer tearDown()

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
	ibusnats.DeclareEmbeddedNATSServer()
	initialArgs = os.Args
	os.Args = []string{"appPath"}
	Declare(context.Background(), "airs-bp", ibus.DefaultTimeout)
	var err error
	ctx, err = services.ResolveAndStart()
	require.Nil(t, err, err)
	defer tearDown()

	bodyReader := bytes.NewReader(nil)
	resp, err := http.Post("http://127.0.0.1:8822/api", "", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()
	respBodyBytes, err := ioutil.ReadAll(resp.Body)
	require.Nil(t, err)
	require.Equal(t, `["airs-bp"]`, string(respBodyBytes))
	expectOKRespPlainText(t, resp)
}

func Test404(t *testing.T) {
	setUpHTTPOnly()
	defer tearDown()

	bodyReader := bytes.NewReader(nil)
	resp, err := http.Post("http://127.0.0.1:8822/api/wrong", "", bodyReader)
	require.Nil(t, err, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func setUpHTTPOnly() {
	initialArgs = os.Args
	os.Args = []string{"appPath"}
	godif.ProvideSliceElement(&services.Services, &Service{
		RouterParams: RouterParams{
			Port:             DefaultRouterPort,
			WriteTimeout:     DefaultRouterWriteTimeout,
			ReadTimeout:      DefaultRouterReadTimeout,
			ConnectionsLimit: DefaultRouterConnectionsLimit,
		},
	})
	var err error
	services.SetVerbose(false)
	if ctx, err = services.ResolveAndStart(); err != nil {
		panic(err)
	}
}
