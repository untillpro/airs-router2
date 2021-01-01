/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"os"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"
	ibusnats "github.com/untillpro/airs-ibusnats"
	"github.com/untillpro/godif"
)

func TestCLI(t *testing.T) {
	initialArgs = os.Args
	defer func() {
		os.Args = initialArgs
	}()
	os.Args = []string{"appPath", "-ns", "123", "-p", "8823", "-wt", "42", "-rt", "43", "-cl", "44", "-v"}
	declare()
	defer godif.Reset()

	expectedBusSrv := &ibusnats.Service{
		NATSServers: "123",
		Queues: map[string]int{
			"airs-bp": 100,
		},
		Verbose: true,
	}
	require.Equal(t, expectedBusSrv, busSrv)

	expectedRouterSrv := Service{
		Port:             8823,
		WriteTimeout:     42,
		ReadTimeout:      43,
		ConnectionsLimit: 44,
	}
	require.Equal(t, expectedRouterSrv, routerSrv)
}

func TestOSExit(t *testing.T) {
	osExitCalls := 0
	initialArgs = os.Args
	os.Args = []string{"appPath"}
	defer func() { os.Args = initialArgs }()

	patches := gomonkey.ApplyFunc(os.Exit, func(code int) {
		require.Equal(t, 1, code)
		osExitCalls++
	})
	defer patches.Reset()
	main() // should fail to start because no NATS servers available for connection
	require.Equal(t, 1, osExitCalls)
}
