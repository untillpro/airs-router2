/*
 * Copyright (c) 2019-present unTill Pro, Ltd. and Contributors
 *
 * This source code is licensed under the MIT license found in the
 * LICENSE file in the root directory of this source tree.
 */

package main

import (
	ibus "github.com/untillpro/airs-ibus"
	iconfig "github.com/untillpro/airs-iconfig"
	"github.com/untillpro/godif"
	"github.com/untillpro/godif/services"
)

// Declare s.e.
func Declare(service Service) {
	godif.ProvideSliceElement(&services.Services, &service)
	godif.Require(&iconfig.PutConfig)
	godif.Require(&iconfig.GetConfig)
	godif.Require(&ibus.SendResponse)
	godif.Require(&ibus.SendRequest)
}
