package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/printer"
)

var (
	version = "dev"
	commit  = ""
	date    = ""
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Date    string `json:"date,omitempty"`
}

func SetVersionInfo(v, c, d string) {
	if v != "" {
		version = v
	}
	commit = c
	date = d
}

func newVersionCmd(f *cliFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			info := versionInfo{Version: version, Commit: commit, Date: date}
			p := newPrinter(f)
			if f.Output == "json" {
				return p.JSONDataEnvelope(printer.JSONDataEnvelope{Kind: "VersionInfo", Data: info})
			}
			if f.Output == "plain" {
				_, _ = fmt.Fprintln(p.Out, info.Version)
				return nil
			}
			rows := [][2]string{{"version", info.Version}}
			if info.Commit != "" {
				rows = append(rows, [2]string{"commit", info.Commit})
			}
			if info.Date != "" {
				rows = append(rows, [2]string{"date", info.Date})
			}
			p.KV(rows)
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
	ContextAPIVersion string        `json:"contextApiVersion"`
	AuditAPIVersion   string        `json:"auditApiVersion"`
	Engines           []CapEngine   `json:"engines"`
	RiskModel         []CapRisk     `json:"riskModel"`
	AllowFlags        []string      `json:"allowFlags"`
	Governance        CapGovernance `json:"governance"`
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
			ContextAPIVersion: "dbgov.io/context/v1",
			AuditAPIVersion:   "dbgov.io/audit/v1",
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
					Status: "read/explain/schema",
					Rollback: CapRollback{
						DDL:  "transactional",
						Data: "generally-irreversible",
						Notes: []string{
							"PostgreSQL supports connect, read query, explain, schema inspect, diff, plan, and apply.",
							"Governed DML and GitOps workflows are planned for later phases.",
							"Data rollback requires explicit undo capture in future phases.",
						},
					},
				},
			},
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
				for _, engine := range data.Supported.Engines {
					_, _ = fmt.Fprintf(p.Out, "%s\t%s\n", engine.Name, engine.Status)
				}
				return nil
			}
			rows := [][]string{
				{"contextApiVersion", data.Supported.ContextAPIVersion},
				{"auditApiVersion", data.Supported.AuditAPIVersion},
				{"engines", "mysql available; postgres read/explain/schema"},
				{"authorization", "R1/R2/R3 require --yes; R2/R3 require --ticket; R3 requires --allow-*"},
				{"governance", "audit, RBAC, dry-run, OTel"},
			}
			p.Table([]string{"KEY", "VALUE"}, rows)
			return nil
		},
	}
}
