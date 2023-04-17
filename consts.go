/*
 * Copyright (c) 2021-present Sigma-Soft, Ltd. Aleksei Ponomarev
 */

package router2

import (
	"time"

	coreutils "github.com/voedger/voedger/pkg/utils"
)

const (
	HTTPSPort                       = 443
	DefaultACMEServerReadTimeout    = 5 * time.Second
	DefaultACMEServerWriteTimeout   = 5 * time.Second
	subscriptionsCloseCheckInterval = 100 * time.Millisecond
	localhost                       = "127.0.0.1"
	parseInt64Base                  = 10
	parseInt64Bits                  = 64
)

var bearerPrefixLen = len(coreutils.BearerPrefix)
