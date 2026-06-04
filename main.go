package main

import (
	"fmt"
	"os"

	"github.com/JiangHe12/opskit-core/apperrors"

	"github.com/JiangHe12/dbgov-cli/cmd"
)

var (
	version string
	commit  string
	date    string
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	cmd.SetSkillFS(skillEmbedFS)
	if err := cmd.NewRootCmd().Execute(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(apperrors.ExitCode(err))
	}
}
