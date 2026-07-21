package cmd

import (
	"net"

	"github.com/JiangHe12/opskit-core/v2/printer"
)

type targetMode string

const (
	targetRead  targetMode = "read"
	targetWrite targetMode = "write"
)

type commandTarget struct {
	Context   string `json:"context"`
	Engine    string `json:"engine"`
	Host      string `json:"host"`
	Port      int    `json:"port,omitempty"`
	HostPort  string `json:"hostPort,omitempty"`
	Database  string `json:"database"`
	Operation string `json:"operation"`
}

func printTargetHeader(p *printer.Printer, meta contextMeta, mode targetMode) error {
	label := "TARGET"
	if mode == targetWrite {
		label = "WRITE TARGET"
	}
	return p.TargetHeader(label, targetFields(meta))
}

func dataWithTarget(data any, meta contextMeta, mode targetMode) any {
	return printer.WithTarget(data, commandTargetFromMeta(meta, mode))
}

func commandTargetFromMeta(meta contextMeta, mode targetMode) commandTarget {
	return commandTarget{
		Context:   meta.Name,
		Engine:    meta.Engine,
		Host:      meta.Host,
		Port:      meta.Port,
		HostPort:  hostPort(meta.Host, meta.Port),
		Database:  meta.Database,
		Operation: string(mode),
	}
}

func targetFields(meta contextMeta) [][2]string {
	return [][2]string{
		{"context", meta.Name},
		{"engine", meta.Engine},
		{"host", hostPort(meta.Host, meta.Port)},
		{"database", meta.Database},
	}
}

func hostPort(host string, port int) string {
	if host == "" {
		return ""
	}
	if port <= 0 {
		return host
	}
	return net.JoinHostPort(host, itoa(port))
}
