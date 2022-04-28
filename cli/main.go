/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"context"
	"log"

	ibus "github.com/untillpro/airs-ibus"
	router "github.com/untillpro/airs-router2"
	"github.com/untillpro/godif/services"
)

func main() {
	router.Declare(context.Background(), "", ibus.DefaultTimeout) // do not subscribe on any queue. Router does not handle messages from NATS.
	if err := services.Run(); err != nil {
		log.Fatal(err)
	}
}
