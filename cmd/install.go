package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/apperrors"
)

var agentPaths = map[string]string{
	"claude":    ".claude/skills",
	"codex":     ".codex/skills",
	"opencode":  ".opencode/skills",
	"copilot":   ".copilot/skills",
	"cursor":    ".cursor/skills",
	"cc-switch": ".cc-switch/skills",
	"windsurf":  ".windsurf/skills",
	"aider":     ".aider/skills",
}

var skillFS fs.FS

// SetSkillFS injects the embedded skill file system from main.
func SetSkillFS(fsys fs.FS) {
	skillFS = fsys
}

func newInstallCmd(f *cliFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "install <agent>",
		Short: "Install skill to AI agent working directory",
		Long: `Install dbgov-cli skill to the specified AI agent's skills directory.

Preset agents:
  claude      -> ~/.claude/skills/
  codex       -> ~/.codex/skills/
  opencode    -> ~/.opencode/skills/
  copilot     -> ~/.copilot/skills/
  cursor      -> ~/.cursor/skills/
  cc-switch   -> ~/.cc-switch/skills/
  windsurf    -> ~/.windsurf/skills/
  aider       -> ~/.aider/skills/

Custom path:
  dbgov-cli install /my/path --skills  -> /my/path/dbgov-cli/`,
		Example: `  dbgov-cli install claude --skills
  dbgov-cli install codex --skills
  dbgov-cli install /custom/path --skills`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			skills, _ := cmd.Flags().GetBool("skills")
			if !skills {
				return apperrors.New(apperrors.CodeUsageError, "please specify --skills flag", nil)
			}
			return installSkills(f, args[0])
		},
	}
	cmd.Flags().Bool("skills", false, "Install skill files")
	_ = cmd.MarkFlagRequired("skills")
	return cmd
}

func installSkills(f *cliFlags, target string) error {
	installDir, err := resolveInstallDir(target)
	if err != nil {
		return err
	}

	dstDir := filepath.Join(installDir, "dbgov-cli")
	if err := copyEmbeddedSkill(skillFS, "skills/dbgov-cli", dstDir); err != nil {
		return err
	}

	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("InstallResult", map[string]string{"path": dstDir})
	}
	p.Success(fmt.Sprintf("skill installed to %s", dstDir))
	return nil
}

func resolveInstallDir(target string) (string, error) {
	if skillsDir, ok := agentPaths[strings.ToLower(target)]; ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		return filepath.Join(home, skillsDir), nil
	}
	return target, nil
}

func copyEmbeddedSkill(fsys fs.FS, srcDir, dstDir string) error {
	if fsys == nil {
		return apperrors.New(apperrors.CodeLocalIOError, "embedded skill filesystem is not initialized", nil)
	}
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to create skill directory", err)
	}
	entries, err := fs.ReadDir(fsys, srcDir)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to read embedded skill", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		srcPath := path.Join(srcDir, entry.Name())
		dstPath := filepath.Join(dstDir, entry.Name())
		if entry.IsDir() {
			if err := copyEmbeddedSkill(fsys, srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := fs.ReadFile(fsys, srcPath)
		if err != nil {
			return apperrors.New(apperrors.CodeLocalIOError, "failed to read embedded skill file", err)
		}
		if err := os.WriteFile(dstPath, data, 0o600); err != nil { //nolint:gosec // dstPath is the user-selected install destination.
			return apperrors.New(apperrors.CodeLocalIOError, "failed to write skill file", err)
		}
	}

	return nil
}
