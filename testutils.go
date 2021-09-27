/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import "context"

// GetService use in tests
func GetService(ctx context.Context) *Service {
	return ctx.Value(routerKey).(*Service)
}
