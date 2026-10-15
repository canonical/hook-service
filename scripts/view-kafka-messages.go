// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	v1 "github.com/canonical/authorization-service/api/v1"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// parseBrokers splits a comma-delimited broker list, trims whitespace, and drops empty elements.
func parseBrokers(raw string) []string {
	var brokers []string
	for b := range strings.SplitSeq(raw, ",") {
		if trimmed := strings.TrimSpace(b); trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	if len(brokers) == 0 {
		return []string{"localhost:9092"}
	}
	return brokers
}

// formatEnvelope unmarshals raw protobuf bytes into PermissionUpdateEnvelope and formats it as JSON.
func formatEnvelope(val []byte, marshaller protojson.MarshalOptions) (string, error) {
	var env v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(val, &env); err != nil {
		return "", err
	}
	jsonBytes, err := marshaller.Marshal(&env)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

func main() {
	defaultBroker := cmp.Or(os.Getenv("KAFKA_BROKERS"), "localhost:9092")

	brokerFlag := flag.String("broker", defaultBroker, "Kafka broker address (comma-separated)")
	topicFlag := flag.String("topic", "permissions.hook-service", "Kafka topic to consume")
	followFlag := flag.Bool("follow", false, "Keep listening for new messages (follow mode)")
	limitFlag := flag.Int("limit", 0, "Maximum number of messages to consume (0 = unlimited)")
	jsonOnlyFlag := flag.Bool("json-only", false, "Print only JSON payloads (suitable for jq piping)")
	tlsFlag := flag.Bool("tls", os.Getenv("KAFKA_TLS_ENABLED") == "true", "Enable TLS encryption")
	caFileFlag := flag.String("tls-ca", os.Getenv("KAFKA_TLS_CA_FILE"), "Path to TLS CA certificate file")
	timeoutFlag := flag.Duration("timeout", 3*time.Second, "Poll timeout when not in follow mode")

	flag.Parse()

	brokers := parseBrokers(*brokerFlag)

	opts := []kgo.Opt{
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics(*topicFlag),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}

	if *tlsFlag {
		tlsCfg := &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
		if *caFileFlag != "" {
			caCert, err := os.ReadFile(*caFileFlag)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read TLS CA certificate from %s: %v\n", *caFileFlag, err)
				os.Exit(1)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caCert) {
				fmt.Fprintf(os.Stderr, "failed to parse TLS CA certificate from %s\n", *caFileFlag)
				os.Exit(1)
			}
			tlsCfg.RootCAs = pool
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize Kafka client: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if !*jsonOnlyFlag {
		fmt.Printf("Consuming from topic %q at %s (press Ctrl+C to stop)...\n", *topicFlag, strings.Join(brokers, ", "))
	}

	marshaller := protojson.MarshalOptions{
		Indent:          "  ",
		EmitUnpopulated: false,
	}

	count := 0
	emptyPolls := 0
	const maxEmptyPolls = 2

	for ctx.Err() == nil {

		pollCtx, pollCancel := context.WithTimeout(ctx, *timeoutFlag)
		fetches := client.PollFetches(pollCtx)
		pollCancel()

		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fetchErr := range errs {
				if !errors.Is(fetchErr.Err, context.DeadlineExceeded) && !errors.Is(fetchErr.Err, context.Canceled) {
					fmt.Fprintf(os.Stderr, "fetch error on %s[%d]: %v\n", fetchErr.Topic, fetchErr.Partition, fetchErr.Err)
				}
			}
		}

		records := fetches.Records()
		if len(records) == 0 {
			if !*followFlag {
				emptyPolls++
				if emptyPolls >= maxEmptyPolls {
					break
				}
			}
			continue
		}
		emptyPolls = 0

		for _, rec := range records {
			count++
			formattedJSON, formatErr := formatEnvelope(rec.Value, marshaller)

			if *jsonOnlyFlag {
				if formatErr == nil {
					fmt.Println(formattedJSON)
				} else {
					fmt.Println(string(rec.Value))
				}
			} else {
				fmt.Printf("================================================================================\n")
				fmt.Printf("Message #%d | Partition: %d | Offset: %d | Key: %s | Time: %s\n",
					count, rec.Partition, rec.Offset, string(rec.Key), rec.Timestamp.UTC().Format(time.RFC3339))
				fmt.Printf("================================================================================\n")
				if formatErr == nil {
					fmt.Println(formattedJSON)
				} else {
					fmt.Printf("[Raw payload (not a valid PermissionUpdateEnvelope):]\n%s\n", string(rec.Value))
				}
				fmt.Println()
			}

			if *limitFlag > 0 && count >= *limitFlag {
				break
			}
		}

		if *limitFlag > 0 && count >= *limitFlag {
			break
		}
	}

	if !*jsonOnlyFlag {
		fmt.Printf("--------------------------------------------------------------------------------\n")
		fmt.Printf("Completed. Total messages consumed: %d\n", count)
	}
}
