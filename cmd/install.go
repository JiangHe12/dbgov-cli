package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JiangHe12/opskit-core/v2/apperrors"

	dbgaudit "github.com/JiangHe12/dbgov-cli/internal/audit"
	"github.com/JiangHe12/dbgov-cli/internal/safety"
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
  dbgov-cli install /my/path --skills --yes  -> /my/path/dbgov-cli/`,
		Example: `  dbgov-cli install claude --skills --yes
  dbgov-cli install codex --skills --yes
  dbgov-cli install /custom/path --skills --yes`,
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

	dstDir, err := canonicalLocalMutationDirectory(filepath.Join(installDir, "dbgov-cli"))
	if err != nil {
		return err
	}
	files, err := prepareEmbeddedSkill(skillFS, "skills/dbgov-cli", "")
	if err != nil {
		return err
	}
	relativePaths, err := embeddedSkillRelativePaths(files)
	if err != nil {
		return err
	}
	if err := preflightPrivateMutationFiles(dstDir, relativePaths); err != nil {
		return err
	}
	event := dbgaudit.New(
		dbgaudit.EventTypeInstallSkill,
		currentOperator(f),
		dbgaudit.Context{},
		dbgaudit.Target{ObjectType: "skill", Object: dstDir},
	)
	event.Risk = "R1"
	if err := authorizeWrite(f, safety.R1, contextMeta{}, nil, nil); err != nil {
		event.Status = dbgaudit.StatusDenied
		setAuditError(&event, err)
		emitAudit(f, event, nil)
		return err
	}
	metadata := mutationValueMetadata(string(dbgaudit.EventTypeInstallSkill), files)
	metadata.Items = len(files)
	handle, err := beginMutationAudit(f, mutationAuditSpec{
		Action:   string(dbgaudit.EventTypeInstallSkill),
		Event:    event,
		Metadata: metadata,
	})
	if err != nil {
		return err
	}
	written, attempted, opErr := writeEmbeddedSkill(files, dstDir)
	if auditErr := finishMutationAuditProgress(handle, len(files), written, attempted, opErr); auditErr != nil {
		return auditErr
	}

	p := newPrinter(f)
	if f.Output == "json" {
		return p.JSONData("InstallResult", map[string]string{"path": dstDir})
	}
	return p.Success(fmt.Sprintf("skill installed to %s", dstDir))
}

func resolveInstallDir(target string) (string, error) {
	if skillsDir, ok := agentPaths[strings.ToLower(target)]; ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", apperrors.New(apperrors.CodeLocalIOError, "failed to get home directory", err)
		}
		return filepath.Join(home, skillsDir), nil
	}
	return target, nil
}

type embeddedSkillFile struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
}

func prepareEmbeddedSkill(fsys fs.FS, srcDir, relativeDir string) ([]embeddedSkillFile, error) {
	if fsys == nil {
		return nil, apperrors.New(apperrors.CodeLocalIOError, "embedded skill filesystem is not initialized", nil)
	}
	entries, err := fs.ReadDir(fsys, srcDir)
	if err != nil {
		return nil, apperrors.New(apperrors.CodeLocalIOError, "failed to read embedded skill", err)
	}

	files := make([]embeddedSkillFile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}

		srcPath := path.Join(srcDir, entry.Name())
		relativePath := path.Join(relativeDir, entry.Name())
		if entry.IsDir() {
			nested, err := prepareEmbeddedSkill(fsys, srcPath, relativePath)
			if err != nil {
				return nil, err
			}
			files = append(files, nested...)
			continue
		}
		data, err := fs.ReadFile(fsys, srcPath)
		if err != nil {
			return nil, apperrors.New(apperrors.CodeLocalIOError, "failed to read embedded skill file", err)
		}
		files = append(files, embeddedSkillFile{Path: relativePath, Data: data})
	}
	return files, nil
}

func writeEmbeddedSkill(files []embeddedSkillFile, dstDir string) (int, int, error) {
	written := 0
	relativePaths, err := embeddedSkillRelativePaths(files)
	if err != nil {
		return written, 0, err
	}
	if err := ensurePrivateMutationDirectory(dstDir); err != nil {
		return 0, 0, err
	}
	attempted := 0
	for index, file := range files {
		attempted++
		if _, err := writePrivateMutationFile(dstDir, relativePaths[index], file.Data); err != nil {
			return written, attempted, err
		}
		written++
	}
	return written, attempted, nil
}

func embeddedSkillRelativePaths(files []embeddedSkillFile) ([]string, error) {
	relativePaths := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		relativePath, err := cleanMutationRelativePath(file.Path)
		if err != nil {
			return nil, err
		}
		key := mutationPathKey(relativePath)
		if _, exists := seen[key]; exists {
			return nil, mutationPathCollisionError(relativePath)
		}
		seen[key] = struct{}{}
		relativePaths = append(relativePaths, relativePath)
	}
	return relativePaths, nil
}
