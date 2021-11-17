/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"context"
	"log"

	router "github.com/untillpro/airs-router2"
	"github.com/untillpro/godif/services"
)

func main() {
	router.Declare(context.Background(), "") // do not subscribe on any queue. Router does not handle messages from NATS.
	if err := services.Run(); err != nil {
		log.Fatal(err)
	}
}
