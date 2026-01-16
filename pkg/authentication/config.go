// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"strings"
)

type Config struct {
	Issuer          string
	AllowedSubjects []string
	RequiredScope   string
}

func NewConfig(issuer, allowedSubjects, requiredScope string) *Config {
	c := &Config{
		Issuer:        issuer,
		RequiredScope: requiredScope,
	}

	if allowedSubjects != "" {
		c.AllowedSubjects = strings.Split(allowedSubjects, ",")
		for i := range c.AllowedSubjects {
			c.AllowedSubjects[i] = strings.TrimSpace(c.AllowedSubjects[i])
		}
	}

	return c
}
