package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/printer"
)

var (
	version = "dev"
	commit  = unknownBuildValue
	built   = unknownBuildValue
)

const unknownBuildValue = "unknown"

type versionInfo struct {
	Built   string `json:"built"`
	Commit  string `json:"commit"`
	Version string `json:"version"`
}

func SetVersionInfo(v, c, b string) {
	if v != "" {
		version = v
	}
	commit = buildMetadataValue(c)
	built = buildMetadataValue(b)
}

func buildMetadataValue(value string) string {
	if value == "" {
		return unknownBuildValue
	}
	return value
}

func newVersionCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			info := versionInfo{Built: built, Commit: commit, Version: version}
			p := newPrinter(f)
			if f.Output == "json" {
				return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "VersionInfo", Data: info})
			}
			if f.Output == "plain" {
				_, _ = fmt.Fprintln(p.Out, info.Version)
				return nil
			}
			_, _ = fmt.Fprintf(p.Out, "dbgov-cli %s (commit: %s, built: %s)\n", info.Version, info.Commit, info.Built)
			return nil
		},
	}
}

type CapabilitiesData struct {
	Tool      CapTool      `json:"tool"`
	Supported CapSupported `json:"supported"`
}

type CapTool struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type CapSupported struct {
	ContextAPIVersions []string      `json:"contextApiVersions"`
	AuditAPIVersions   []string      `json:"auditApiVersions"`
	Engines            []CapEngine   `json:"engines"`
	Schema             string        `json:"schema"`
	RiskModel          []CapRisk     `json:"riskModel"`
	AllowFlags         []string      `json:"allowFlags"`
	Governance         CapGovernance `json:"governance"`
}

type CapEngine struct {
	Name     string      `json:"name"`
	Status   string      `json:"status"`
	Rollback CapRollback `json:"rollback"`
}

type CapRollback struct {
	DDL   string   `json:"ddl"`
	Data  string   `json:"data"`
	Notes []string `json:"notes"`
}

type CapRisk struct {
	Level         string `json:"level"`
	Authorization string `json:"authorization"`
}

type CapGovernance struct {
	Audit  string `json:"audit"`
	RBAC   string `json:"rbac"`
	DryRun bool   `json:"dryRun"`
	OTel   bool   `json:"otel"`
}

func capabilitiesData() CapabilitiesData {
	return CapabilitiesData{
		Tool: CapTool{Name: "dbgov", Version: version},
		Supported: CapSupported{
			ContextAPIVersions: []string{"dbgov-cli.io/context/v1"},
			AuditAPIVersions:   []string{"dbgov-cli.io/audit/v1"},
			Engines: []CapEngine{
				{
					Name:   "mysql",
					Status: "available",
					Rollback: CapRollback{
						DDL:  "none",
						Data: "irreversible",
						Notes: []string{
							"DDL uses implicit commit; rollback uses structural snapshot guidance only.",
							"Deleted data cannot be reconstructed by dbgov.",
						},
					},
				},
				{
					Name:   "postgres",
					Status: "available",
					Rollback: CapRollback{
						DDL:  "structural-snapshot",
						Data: "irreversible",
						Notes: []string{
							"PostgreSQL supports query, explain, schema workflows, governed DML, import, reconcile, and rollback.",
							"Rollback restores schema structure from snapshots only.",
							"Deleted data cannot be reconstructed by dbgov.",
						},
					},
				},
			},
			Schema: "MySQL and PostgreSQL schema diff/plan/apply manage normalized autoIncrement columns; serial-vs-identity, ALWAYS-vs-BY-DEFAULT, and sequence options are not preserved.",
			RiskModel: []CapRisk{
				{Level: "R0", Authorization: "free"},
				{Level: "R1", Authorization: "--yes or interactive confirmation"},
				{Level: "R2", Authorization: "--yes plus --ticket"},
				{Level: "R3", Authorization: "--yes plus --ticket plus operation-specific --allow-* flag"},
			},
			AllowFlags: []string{"--allow-destructive", "--allow-no-where", "--allow-production-prune"},
			Governance: CapGovernance{
				Audit:  "append-only JSONL with optional age encryption",
				RBAC:   "opt-in roles reader/writer/admin",
				DryRun: true,
				OTel:   true,
			},
		},
	}
}

func capabilityPlainCommands() []string {
	return []string{
		"ctx",
		"schema",
		"data",
		"export",
		"import",
		"reconcile",
		"rollback",
		"audit",
		"install",
		"version",
		"capabilities",
		"doctor",
		"query",
		"explain",
	}
}

func newCapabilitiesCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "capabilities",
		Short: "Show static dbgov capabilities",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			data := capabilitiesData()
			p := newPrinter(f)
			if f.Output == "json" {
				return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "Capabilities", Data: data})
			}
			if f.Output == "plain" {
				for _, command := range capabilityPlainCommands() {
					_, _ = fmt.Fprintln(p.Out, command)
				}
				return nil
			}
			rows := [][]string{
				{"contextApiVersions", strings.Join(data.Supported.ContextAPIVersions, ", ")},
				{"auditApiVersions", strings.Join(data.Supported.AuditAPIVersions, ", ")},
				{"engines", "mysql available; postgres available"},
				{"schema", data.Supported.Schema},
				{"authorization", "R1/R2/R3 require --yes; R2/R3 require --ticket; R3 requires --allow-*"},
				{"governance", "audit, RBAC, dry-run, OTel"},
			}
			p.Table([]string{"KEY", "VALUE"}, rows)
			return nil
		},
	}
}
