package cmd

import (
	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/printer"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
)

type doctorOptions struct {
	Fake bool
}

type DoctorReport struct {
	Checks []DoctorCheck `json:"checks"`
}

type DoctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func newDoctorCmd(f *cliFlags) *cobra.Command {
	var opts doctorOptions
	cmd := &cobra.Command{
		Use:   "doctor [config|network|auth]",
		Short: "Run static and read-only diagnostics",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target := "config"
			if len(args) > 0 {
				target = args[0]
			}
			return runDoctor(f, opts, target)
		},
	}
	cmd.Flags().BoolVar(&opts.Fake, "fake", false, "Use in-memory fake backend")
	return cmd
}

func runDoctor(f *cliFlags, opts doctorOptions, target string) error {
	if err := authorizeRead(f); err != nil {
		return err
	}
	report := DoctorReport{}
	backend, meta, err := buildBackend(f, backendOptions(opts))
	if target == "config" {
		report.Checks = append(report.Checks, checkFromError("config", err))
		emitDoctorAudit(f, meta, err)
		return printDoctorReport(f, report)
	}
	if err == nil {
		switch target {
		case "network":
			err = ping(commandContext(f), backend)
			report.Checks = append(report.Checks, checkFromError("network", err))
		case "auth":
			_, err = backend.Query(commandContext(f), "SELECT 1")
			report.Checks = append(report.Checks, checkFromError("auth", err))
		default:
			report.Checks = append(report.Checks, DoctorCheck{Name: target, Status: "fail", Message: "unknown doctor check"})
		}
	} else {
		report.Checks = append(report.Checks, checkFromError(target, err))
	}
	emitDoctorAudit(f, meta, err)
	return printDoctorReport(f, report)
}

func checkFromError(name string, err error) DoctorCheck {
	if err != nil {
		return DoctorCheck{Name: name, Status: "fail", Message: err.Error()}
	}
	return DoctorCheck{Name: name, Status: "ok"}
}

func emitDoctorAudit(f *cliFlags, meta contextMeta, err error) {
	event := dbgaudit.New(dbgaudit.EventTypeDoctor, currentOperator(f), auditContext(meta), auditTarget(meta, "doctor", "doctor"))
	event.Risk = "R0"
	emitAudit(f, event, err)
}

func printDoctorReport(f *cliFlags, report DoctorReport) error {
	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "DoctorReport", Data: report})
	}
	rows := make([][]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		rows = append(rows, []string{check.Name, check.Status, check.Message})
	}
	p.Table([]string{"CHECK", "STATUS", "MESSAGE"}, rows)
	return nil
}
