/*
 * Copyright (c) 2021-present unTill Pro, Ltd.
 */

package router2

import (
	"errors"
	"strings"
)

func ParseRoutes(routes []string, routesMap map[string]string) error {
	for _, r := range routes {
		fromTo := strings.Split(r, "=")
		if len(fromTo) != 2 {
			return errors.New("wrong route value: " + r)
		}
		routesMap[fromTo[0]] = fromTo[1]
	}
	return nil
}
