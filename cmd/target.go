package cmd

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/JiangHe12/opskit-core/printer"
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

type targetedJSONData struct {
	data   any
	target commandTarget
}

func printTargetHeader(p *printer.Printer, meta contextMeta, mode targetMode) {
	if p.Format == printer.FormatJSON {
		return
	}
	label := "TARGET"
	if mode == targetWrite {
		label = "WRITE TARGET"
	}
	p.KV([][2]string{{label, formatTargetSummary(meta)}})
	_, _ = fmt.Fprintln(p.Out)
}

func dataWithTarget(data any, meta contextMeta, mode targetMode) any {
	return targetedJSONData{data: data, target: commandTargetFromMeta(meta, mode)}
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

func formatTargetSummary(meta contextMeta) string {
	target := commandTargetFromMeta(meta, targetRead)
	return fmt.Sprintf("context=%s | engine=%s | host=%s | database=%s", target.Context, target.Engine, target.HostPort, target.Database)
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

func (d targetedJSONData) MarshalJSON() ([]byte, error) {
	dataBytes, err := json.Marshal(d.data)
	if err != nil {
		return nil, err
	}
	targetBytes, err := json.Marshal(d.target)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(dataBytes, &object); err == nil && object != nil {
		object["target"] = targetBytes
		return json.Marshal(object)
	}
	return json.Marshal(struct {
		Target commandTarget `json:"target"`
		Value  any           `json:"value"`
	}{
		Target: d.target,
		Value:  d.data,
	})
}
