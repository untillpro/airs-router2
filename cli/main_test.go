/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	ibusnats "github.com/untillpro/airs-ibusnats"
	router "github.com/untillpro/airs-router2"
)

func TestCLI(t *testing.T) {
	initialArgs := os.Args
	defer func() {
		os.Args = initialArgs
	}()
	os.Args = []string{"appPath", "--ns", "123", "--p", "8823", "--wt", "42", "--rt", "43", "--cl", "44", "--v"}
	actualRP := router.ProvideRouterParamsFromCmdLine()
	expectedRP := router.RouterParams{
		NATSServers:          ibusnats.NATSServers{"123"},
		Port:                 8823,
		WriteTimeout:         42,
		ReadTimeout:          43,
		ConnectionsLimit:     44,
		Verbose:              true,
		CertDir:              ".",
		HTTP01ChallengeHosts: []string{},
	}
	require.Equal(t, expectedRP, actualRP)
}
