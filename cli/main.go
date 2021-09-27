/*
 * Copyright (c) 2020-present unTill Pro, Ltd.
 */

package main

import (
	"log"
	"os"

	router2 "github.com/untillpro/airs-router2"
	"github.com/untillpro/godif/services"
)

func main() {
	router2.Declare()
	if err := services.Run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}
