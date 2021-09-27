/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	ibusnats "github.com/untillpro/airs-ibusnats"
	router2 "github.com/untillpro/airs-router2"
)

func TestCLI(t *testing.T) {
	initialArgs := os.Args
	defer func() {
		os.Args = initialArgs
	}()
	os.Args = []string{"appPath", "-ns", "123", "-p", "8823", "-wt", "42", "-rt", "43", "-cl", "44", "-v"}
	cliParams := router2.ProvideCliParams()
	actualIBusNATSSrv := router2.ProvideIBusNATSSrv(cliParams, router2.QueuesPartitionsMap{
		"airs-bp": 100,
	})
	expectedBusSrv := &ibusnats.Service{
		NATSServers: "123",
		Queues: map[string]int{
			"airs-bp": 100,
		},
		Verbose: true,
	}
	require.Equal(t, expectedBusSrv, actualIBusNATSSrv)

	actualRouterSrv := router2.ProvideRouterSrv(cliParams)
	expectedRouterSrv := router2.Service{
		Port:             8823,
		WriteTimeout:     42,
		ReadTimeout:      43,
		ConnectionsLimit: 44,
	}
	require.Equal(t, expectedRouterSrv, actualRouterSrv)
}
