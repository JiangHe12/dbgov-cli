package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/JiangHe12/opskit-core/apperrors"

	"github.com/JiangHe12/dbgov-cli/cmd"
)

var (
	version string
	commit  string
	built   string
)

func main() {
	cmd.SetVersionInfo(version, commit, built)
	cmd.SetSkillFS(skillEmbedFS)
	if err := cmd.NewRootCmd().Execute(); err != nil {
		if outputFlagFromArgs(os.Args[1:]) == "json" {
			_ = apperrors.WriteJSON(os.Stderr, err)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(apperrors.ExitCode(err))
	}
}

func outputFlagFromArgs(args []string) string {
	for i, arg := range args {
		if (arg == "-o" || arg == "--output") && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, "--output=") {
			return strings.TrimPrefix(arg, "--output=")
		}
		if strings.HasPrefix(arg, "-o=") {
			return strings.TrimPrefix(arg, "-o=")
		}
	}
	return ""
}
