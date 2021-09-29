/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"log"

	router "github.com/untillpro/airs-router2"
	"github.com/untillpro/godif/services"
)

func main() {
	router.Declare()
	if err := services.Run(); err != nil {
		log.Fatal(err)
	}
}
