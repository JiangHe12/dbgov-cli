package cmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"

	"github.com/JiangHe12/opskit-core/v2/telemetry"
)

func TestManagedTelemetryCoversCommandAndFlushesAfterward(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "https://trace.example.test")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "https://metrics.example.test")

	oldInitTelemetry := initTelemetry
	oldInitMetrics := initMetrics
	oldRecordCommand := recordCommand
	t.Cleanup(func() {
		initTelemetry = oldInitTelemetry
		initMetrics = oldInitMetrics
		recordCommand = oldRecordCommand
	})

	traceStops := 0
	metricStops := 0
	recordedStatus := ""
	initTelemetry = func(_ context.Context, endpoint string, _ bool, _ string) telemetry.ShutdownFunc {
		if endpoint != "https://trace.example.test" {
			t.Errorf("trace endpoint = %q", endpoint)
		}
		return func(ctx context.Context) {
			traceStops++
			assertBoundedShutdownContext(ctx, t)
		}
	}
	initMetrics = func(_ context.Context, endpoint string, _ bool, _ string) telemetry.ShutdownFunc {
		if endpoint != "https://metrics.example.test" {
			t.Errorf("metrics endpoint = %q", endpoint)
		}
		return func(ctx context.Context) {
			metricStops++
			assertBoundedShutdownContext(ctx, t)
		}
	}
	recordCommand = func(_ context.Context, command, status string, _ time.Duration, _ []attribute.KeyValue) {
		if command != "dbgov-cli.telemetry-probe" {
			t.Errorf("command = %q", command)
		}
		recordedStatus = status
	}

	f := &cliFlags{Output: "table", manageTelemetry: true}
	root := newRootCmdWith(f)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.AddCommand(&cobra.Command{
		Use: "telemetry-probe",
		RunE: func(_ *cobra.Command, _ []string) error {
			if f.activeSpan == nil || f.telemetryStop == nil || f.metricsStop == nil {
				t.Fatal("telemetry was not active during command execution")
			}
			if traceStops != 0 || metricStops != 0 {
				t.Fatal("telemetry stopped before command execution completed")
			}
			return nil
		},
	})
	root.SetArgs([]string{"telemetry-probe"})

	err := root.ExecuteContext(context.Background())
	if err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if traceStops != 0 || metricStops != 0 {
		t.Fatal("telemetry stopped inside Cobra command scope")
	}
	finishTelemetry(context.Background(), f, err)
	if traceStops != 1 || metricStops != 1 {
		t.Fatalf("shutdown calls = trace:%d metrics:%d", traceStops, metricStops)
	}
	if recordedStatus != "success" {
		t.Fatalf("recorded status = %q", recordedStatus)
	}
}

func assertBoundedShutdownContext(ctx context.Context, t *testing.T) {
	t.Helper()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Error("telemetry shutdown context has no deadline")
		return
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > 5*time.Second {
		t.Errorf("telemetry shutdown deadline remaining = %s", remaining)
	}
}
