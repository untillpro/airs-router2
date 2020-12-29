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
	"time"

	"github.com/stretchr/testify/require"
	ibus "github.com/untillpro/airs-ibus"
	"github.com/untillpro/godif"
)

func TestSingleResponseBasic(t *testing.T) {
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {
		ibus.SendResponse(ctx, sender, ibus.Response{
			ContentType: "text/plain",
			StatusCode:  http.StatusOK,
			Data:        []byte("test resp"),
		})
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
	require.Equal(t, "test resp", string(respBodyBytes))
	expectOKRespPlainText(t, resp)
}