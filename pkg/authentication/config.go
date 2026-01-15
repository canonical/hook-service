// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package authentication

import (
	"strings"
)

type Config struct {
	Enabled         bool
	Issuer          string
	JwksURL         string
	AllowedSubjects []string
	RequiredScope   string
}

func NewConfig(enabled bool, issuer, jwksURL, allowedSubjects, requiredScope string) *Config {
	c := &Config{
		Enabled:       enabled,
		Issuer:        issuer,
		JwksURL:       jwksURL,
		RequiredScope: requiredScope,
	}

	if allowedSubjects != "" {
		c.AllowedSubjects = strings.Split(allowedSubjects, ",")
		// Trim whitespace from each subject
		for i := range c.AllowedSubjects {
			c.AllowedSubjects[i] = strings.TrimSpace(c.AllowedSubjects[i])
		}
	}

	return c
}
