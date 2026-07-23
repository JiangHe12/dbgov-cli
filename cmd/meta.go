package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/printer"
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
				return p.Info(info.Version)
			}
			return p.Info(fmt.Sprintf("dbgov-cli %s (commit: %s, built: %s)", info.Version, info.Commit, info.Built))
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
	ContextAPIVersions       []string      `json:"contextApiVersions"`
	AuditAPIVersions         []string      `json:"auditApiVersions"`
	MutationAuditAPIVersions []string      `json:"mutationAuditApiVersions"`
	ErrorCodes               []string      `json:"errorCodes"`
	Engines                  []CapEngine   `json:"engines"`
	Schema                   string        `json:"schema"`
	RiskModel                []CapRisk     `json:"riskModel"`
	AllowFlags               []string      `json:"allowFlags"`
	Governance               CapGovernance `json:"governance"`
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
			MutationAuditAPIVersions: []string{
				mutationAuditAPIVersion,
			},
			ErrorCodes: []string{
				string(codeAuditIncomplete),
			},
			Engines: []CapEngine{
				{
					Name:   "mysql",
					Status: "available",
					Rollback: CapRollback{
						DDL:  "bounded-structural-snapshot",
						Data: "irreversible",
						Notes: []string{
							"Direct schema apply supports the simple parsed column subset; real SHOW CREATE exports are opaque.",
							"Import/reconcile/rollback support exact no-op or one isolated missing InnoDB table recreation; existing opaque tables cannot be changed in place.",
							"Opaque creation is R3; MySQL DDL uses implicit commit.",
							"Zero-statement operations are R0 and write no snapshot.",
							"Deleted data cannot be reconstructed by dbgov.",
						},
					},
				},
				{
					Name:   "postgres",
					Status: "available",
					Rollback: CapRollback{
						DDL:  "bounded-structural-snapshot",
						Data: "irreversible",
						Notes: []string{
							"Direct schema apply supports the simple parsed column subset; rich exports support exact no-op or one isolated missing-table recreation.",
							"Snapshot/export rejects serial/identity, sequence dependencies, non-catalog types/default dependencies, comments, standalone indexes, advanced constraints, partitioning/inheritance, non-default FK actions, triggers/policies, custom storage, and non-default collation.",
							"Zero-statement operations are R0 and write no snapshot.",
							"Deleted data cannot be reconstructed by dbgov.",
						},
					},
				},
			},
			Schema: "Direct diff/plan/apply manages only the parsed column subset; PostgreSQL supports bounded type/identity changes while MySQL existing-column type/autoIncrement changes fail closed. Rich safe CREATE TABLE definitions are opaque R3 restores that must be the plan's only change.",
			RiskModel: []CapRisk{
				{Level: "R0", Authorization: "free"},
				{Level: "R1", Authorization: "--yes or interactive confirmation"},
				{Level: "R2", Authorization: "--yes plus --ticket"},
				{Level: "R3", Authorization: "--yes plus --ticket plus operation-specific --allow-* flag"},
			},
			AllowFlags: []string{
				"--allow-destructive",
				"--allow-no-where",
				"--allow-production-prune",
				"--allow-context-change",
				"--allow-context-delete",
				"--allow-role-change",
				"--allow-audit-prune",
			},
			Governance: CapGovernance{
				Audit:  "sanitized append-only JSONL; mutations use durable intent/outcome records and outcome replay",
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
					if err := p.Info(command); err != nil {
						return err
					}
				}
				return nil
			}
			rows := [][]string{
				{"contextApiVersions", strings.Join(data.Supported.ContextAPIVersions, ", ")},
				{"auditApiVersions", strings.Join(data.Supported.AuditAPIVersions, ", ")},
				{"mutationAuditApiVersions", strings.Join(data.Supported.MutationAuditAPIVersions, ", ")},
				{"errorCodes", strings.Join(data.Supported.ErrorCodes, ", ")},
				{"engines", "mysql available; postgres available"},
				{"schema", data.Supported.Schema},
				{"authorization", "R1/R2/R3 require --yes; R2/R3 require --ticket; R3 requires --allow-*"},
				{"governance", "audit, RBAC, dry-run, OTel"},
			}
			return p.Table([]string{"KEY", "VALUE"}, rows)
		},
	}
}
