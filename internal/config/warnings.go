// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"fmt"
	"strings"
)

// Warnings returns operator advisories for a configuration that is valid but likely
// unintended. A warning never blocks startup, Validate owns that. The order is stable
// so startup logs and tests are deterministic.
//
// Advisories are mode-aware: settings that a mode cannot act on are reported once, as
// a single "configured but ignored" notice, rather than as the per-setting diagnostics
// that only make sense where the setting is actually consumed.
func Warnings(specs *EnvSpec) []string {
	if specs == nil {
		return nil
	}

	if specs.DeploymentMode() == ModeStandalone {
		return standaloneWarnings(specs)
	}
	return platformWarnings(specs)
}

// standaloneWarnings reports settings that standalone mode will never act on because
// no broker connection is made.
func standaloneWarnings(specs *EnvSpec) []string {
	var warnings []string

	if specs.FederatedServiceName != DefaultFederatedServiceName {
		warnings = append(warnings, fmt.Sprintf(
			"Federated service name %q is configured but no brokers are set; running in %s mode and publishing nothing",
			specs.FederatedServiceName, ModeStandalone,
		))
	}

	if specs.hasKafkaSecuritySettings() {
		warnings = append(warnings, fmt.Sprintf(
			"Kafka authentication/TLS settings are configured but no brokers are set; running in %s mode and publishing nothing",
			ModeStandalone,
		))
	}

	return warnings
}

// platformWarnings reports broker security settings that are contradictory or weaken
// transport protection, plus attribution of events to the default service name.
func platformWarnings(specs *EnvSpec) []string {
	var warnings []string

	if specs.FederatedServiceName == DefaultFederatedServiceName {
		warnings = append(warnings, fmt.Sprintf(
			"Running in %s mode under the default federated service name %q; permission events will be attributed to the standalone service",
			ModePlatform, specs.FederatedServiceName,
		))
	}

	if specs.KafkaTLSEnabled {
		if specs.KafkaTLSInsecureSkipVerify {
			warnings = append(warnings,
				"Kafka broker TLS certificate verification is disabled (KAFKA_TLS_INSECURE_SKIP_VERIFY=true); broker certificates will not be validated")
		}
		return warnings
	}

	if NormalizeKafkaAuthType(specs.KafkaAuthType) == KafkaAuthTypeOAuthBearer {
		warnings = append(warnings,
			"Kafka SASL/OAUTHBEARER authentication is enabled without TLS (KAFKA_TLS_ENABLED=false); OAuth tokens will be transmitted in plaintext (SASL_PLAINTEXT)")
	}
	if strings.TrimSpace(specs.KafkaTLSCAFile) != "" {
		warnings = append(warnings,
			"KAFKA_TLS_CA_FILE is configured but KAFKA_TLS_ENABLED is false; broker TLS is disabled and the CA file will be ignored")
	}
	if specs.KafkaTLSInsecureSkipVerify {
		warnings = append(warnings,
			"KAFKA_TLS_INSECURE_SKIP_VERIFY is enabled but KAFKA_TLS_ENABLED is false; broker TLS is disabled and this setting will be ignored")
	}

	return warnings
}
