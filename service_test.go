/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"context"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	ibus "github.com/untillpro/airs-ibus"
	"github.com/untillpro/godif"
)

func TestServiceStartError(t *testing.T) {
	listener, err := net.Listen("tcp", ":"+strconv.Itoa(DefaultRouterPort))
	require.Nil(t, err)
	defer func() {
		require.Nil(t, listener.Close())
	}()
	godif.Provide(&ibus.RequestHandler, func(ctx context.Context, sender interface{}, request ibus.Request) {})
	require.Panics(t, func() { setUp() })
}
