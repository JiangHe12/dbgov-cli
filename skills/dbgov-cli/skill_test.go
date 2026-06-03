// Package skilltest validates the dbgov-cli AI Skill against the cobra surface.
package skilltest

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	cmdpkg "github.com/JiangHe12/dbgov-cli/cmd"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not find repo root")
	return ""
}

func skillRoot(t *testing.T) string {
	return filepath.Join(repoRoot(t), "skills", "dbgov-cli")
}

func loadSkillBody(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(skillRoot(t), "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md: %v", err)
	}
	return string(data)
}

func TestSkillFrontmatter(t *testing.T) {
	body := loadSkillBody(t)
	for _, want := range []string{
		"---\nname: dbgov-cli",
		"description: Governed database operations via CLI",
		"allowed-tools: Bash(dbgov:*), Bash(go:*)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SKILL.md frontmatter missing %q", want)
		}
	}
}

func TestSkillGovernanceRules(t *testing.T) {
	body := loadSkillBody(t)
	for _, want := range []string{
		"Always use `-o json`",
		"MySQL only",
		"Never estimate blast radius yourself",
		"`--yes` only confirms R1",
		"Never auto-supply `--ticket`, `--allow-*`, or high-risk `--yes`",
		"reader | R0",
		"writer | R2",
		"admin | R3",
		"Rollback is structure-level only",
		"dbgov audit prune",
		"`--allow-destructive`",
		"`--allow-no-where`",
		"`--allow-production-prune`",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("SKILL.md missing governance rule %q", want)
		}
	}
}

func TestSkillCommandsExistInCobraTree(t *testing.T) {
	known := collectCommandPaths(cmdpkg.NewRootCmd())
	for _, path := range []string{
		"version",
		"capabilities",
		"doctor",
		"query",
		"explain",
		"export",
		"import",
		"reconcile",
		"install",
		"ctx list",
		"ctx current",
		"ctx set",
		"ctx use",
		"ctx delete",
		"ctx role",
		"ctx migrate-credentials",
		"schema list",
		"schema describe",
		"schema dump",
		"schema diff",
		"schema plan",
		"schema apply",
		"data exec",
		"rollback list",
		"audit query",
		"audit verify",
		"audit prune",
	} {
		if !known[path] {
			t.Errorf("SKILL.md command path `dbgov %s` is not in the cobra command tree", path)
		}
	}
}

func TestSkillFlagsExistInCobraTree(t *testing.T) {
	body := loadSkillBody(t)
	flags := collectFlags(cmdpkg.NewRootCmd())
	for _, flag := range markdownFlags(body) {
		if !flags[flag] {
			t.Errorf("SKILL.md references flag %q but cobra does not define it", flag)
		}
	}
}

func TestSkillReferencesAllExist(t *testing.T) {
	body := loadSkillBody(t)
	link := regexp.MustCompile(`references/([A-Za-z0-9_./-]+\.md)`)
	for _, match := range link.FindAllStringSubmatch(body, -1) {
		full := filepath.Join(skillRoot(t), "references", match[1])
		if _, err := os.Stat(full); err != nil {
			t.Errorf("SKILL.md links to references/%s but file is missing: %v", match[1], err)
		}
	}
}

func collectCommandPaths(root *cobra.Command) map[string]bool {
	out := map[string]bool{}
	var walk func(cmd *cobra.Command, parts []string)
	walk = func(cmd *cobra.Command, parts []string) {
		if len(parts) > 0 {
			out[strings.Join(parts, " ")] = true
		}
		for _, child := range cmd.Commands() {
			if child.Name() == "" {
				continue
			}
			walk(child, append(parts, child.Name()))
		}
	}
	walk(root, nil)
	return out
}

func collectFlags(root *cobra.Command) map[string]bool {
	out := map[string]bool{}
	var walk func(cmd *cobra.Command)
	walk = func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			out["--"+flag.Name] = true
			if flag.Shorthand != "" {
				out["-"+flag.Shorthand] = true
			}
		})
		cmd.PersistentFlags().VisitAll(func(flag *pflag.Flag) {
			out["--"+flag.Name] = true
			if flag.Shorthand != "" {
				out["-"+flag.Shorthand] = true
			}
		})
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	return out
}

func markdownFlags(body string) []string {
	re := regexp.MustCompile(`--[a-z][a-z0-9-]*[a-z0-9]|-[of]`)
	matches := re.FindAllString(body, -1)
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if match == "--allow" {
			continue
		}
		if !seen[match] {
			seen[match] = true
			out = append(out, match)
		}
	}
	return out
}
