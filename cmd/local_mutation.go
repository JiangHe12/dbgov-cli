package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/JiangHe12/opskit-core/v2/apperrors"
	coreaudit "github.com/JiangHe12/opskit-core/v2/audit"

	"github.com/JiangHe12/dbgov-cli/internal/dbgovctx"
)

func canonicalLocalMutationDirectory(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", apperrors.New(apperrors.CodeValidationFailed, "mutation target directory is required", nil)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", apperrors.New(apperrors.CodeValidationFailed, "failed to resolve mutation target directory", err)
	}
	absolute = filepath.Clean(absolute)
	if err := validateExistingMutationPath(absolute); err != nil {
		return "", err
	}
	if err := rejectReservedLocalMutationDirectory(absolute); err != nil {
		return "", err
	}
	return absolute, nil
}

func ensurePrivateMutationDirectory(path string) error {
	path = filepath.Clean(path)
	if err := validateExistingMutationPath(path); err != nil {
		return err
	}

	missing := make([]string, 0, 4)
	current := path
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return apperrors.New(apperrors.CodeLocalIOError, "mutation path component must be a real directory", nil)
			}
			if err := rejectAuditPathReparsePoint(current); err != nil {
				return err
			}
			if err := verifyMutationSpoolParent(current); err != nil {
				return err
			}
			break
		}
		if !os.IsNotExist(err) {
			return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation target directory", err)
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return apperrors.New(apperrors.CodeLocalIOError, "mutation target has no trusted existing parent", nil)
		}
		current = parent
	}

	for index := len(missing) - 1; index >= 0; index-- {
		if err := ensureMutationAuditParent(missing[index]); err != nil {
			return err
		}
	}
	return validateExistingMutationPath(path)
}

func writePrivateMutationFile(root, relativePath string, data []byte) (string, error) {
	return writePrivateLocalFile(root, relativePath, data, false, true)
}

func writePrivateEvidenceFile(root, relativePath string, data []byte) (string, error) {
	return writePrivateLocalFile(root, relativePath, data, true, false)
}

func writePrivateLocalFile(root, relativePath string, data []byte, allowReserved, replaceExisting bool) (string, error) {
	target, err := preflightPrivateLocalFile(root, relativePath, allowReserved)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(target)
	if err := ensurePrivateMutationDirectory(parent); err != nil {
		return "", err
	}
	target, err = preflightPrivateLocalFile(root, relativePath, allowReserved)
	if err != nil {
		return "", err
	}

	temp, err := os.CreateTemp(parent, ".dbgov-write-*")
	if err != nil {
		return "", apperrors.New(apperrors.CodeLocalIOError, "failed to create temporary mutation file", err)
	}
	tempPath := temp.Name()
	committed := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
		}
	}()
	if err := secureMutationSpoolFile(tempPath); err != nil {
		return "", err
	}
	if _, err := temp.Write(data); err != nil {
		return "", apperrors.New(apperrors.CodeLocalIOError, "failed to write temporary mutation file", err)
	}
	if err := temp.Sync(); err != nil {
		return "", apperrors.New(apperrors.CodeLocalIOError, "failed to sync temporary mutation file", err)
	}
	if err := temp.Close(); err != nil {
		return "", apperrors.New(apperrors.CodeLocalIOError, "failed to close temporary mutation file", err)
	}
	commit := commitMutationSpoolFile
	if replaceExisting {
		commit = replacePrivateMutationFile
	}
	if err := commit(tempPath, target); err != nil {
		return "", apperrors.New(apperrors.CodeLocalIOError, "failed to commit mutation target file", err)
	}
	committed = true
	if err := verifyMutationSpoolFile(target); err != nil {
		return "", err
	}
	if err := syncMutationSpoolDirectory(parent); err != nil {
		return "", err
	}
	return target, nil
}

func preflightPrivateMutationFiles(root string, relativePaths []string) error {
	for _, relativePath := range relativePaths {
		if _, err := preflightPrivateLocalFile(root, relativePath, false); err != nil {
			return err
		}
	}
	return nil
}

func preflightPrivateLocalFile(root, relativePath string, allowReserved bool) (string, error) {
	cleanRelative, err := cleanMutationRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	target := filepath.Join(root, cleanRelative)
	if !pathWithin(root, target) {
		return "", apperrors.New(apperrors.CodeValidationFailed, "mutation target escapes the selected directory", nil)
	}
	if !allowReserved {
		if err := rejectReservedLocalMutationFile(target); err != nil {
			return "", err
		}
	}
	parent := filepath.Dir(target)
	if err := validateExistingMutationPath(parent); err != nil {
		return "", err
	}
	if _, err := os.Lstat(target); err == nil {
		if err := verifyMutationSpoolFile(target); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation target file", err)
	}
	return target, nil
}

func cleanMutationRelativePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return "", apperrors.New(apperrors.CodeValidationFailed, "mutation target must be a relative path", nil)
	}
	converted := filepath.FromSlash(path)
	cleaned := filepath.Clean(converted)
	if cleaned != converted || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", apperrors.New(apperrors.CodeValidationFailed, "mutation target contains path traversal", nil)
	}
	for _, part := range strings.Split(cleaned, string(filepath.Separator)) {
		if err := validateMutationPathSegment(part); err != nil {
			return "", err
		}
	}
	return cleaned, nil
}

func validateMutationBasename(name string) error {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return apperrors.New(apperrors.CodeValidationFailed, "mutation target name must be a base filename", nil)
	}
	return validateMutationPathSegment(name)
}

func validateMutationPathSegment(segment string) error {
	if segment == "" || segment == "." || segment == ".." || strings.TrimRight(segment, " .") != segment {
		return apperrors.New(apperrors.CodeValidationFailed, "mutation target contains an invalid path segment", nil)
	}
	for _, char := range segment {
		if char == 0 || char == ':' || char == '/' || char == '\\' || unicode.IsControl(char) {
			return apperrors.New(apperrors.CodeValidationFailed, "mutation target contains an invalid path segment", nil)
		}
	}
	upper := strings.ToUpper(segment)
	base := upper
	if index := strings.IndexByte(base, '.'); index >= 0 {
		base = base[:index]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return apperrors.New(apperrors.CodeValidationFailed, "mutation target uses a reserved filename", nil)
	}
	return nil
}

func validateExistingMutationPath(path string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return apperrors.New(apperrors.CodeLocalIOError, "mutation path must not contain symbolic links", nil)
			}
			if err := rejectAuditPathReparsePoint(current); err != nil {
				return err
			}
			if !info.IsDir() {
				return apperrors.New(apperrors.CodeLocalIOError, "mutation path component must be a directory", nil)
			}
		} else if !os.IsNotExist(err) {
			return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect mutation path", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
}

func verifyPrivateMutationFileIfExists(path string) error {
	if err := validateExistingMutationPath(filepath.Dir(path)); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		return verifyMutationSpoolFile(path)
	} else if !os.IsNotExist(err) {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect private mutation file", err)
	}
	return nil
}

func validateSnapshotEvidenceDirectory(path string, allowMissing bool) error {
	if err := validateExistingMutationPath(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err != nil {
		if allowMissing && os.IsNotExist(err) {
			return nil
		}
		return apperrors.New(apperrors.CodeLocalIOError, "failed to inspect snapshot directory", err)
	}
	if err := verifyMutationSpoolDirectory(path); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return apperrors.New(apperrors.CodeLocalIOError, "failed to list snapshot directory", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return apperrors.New(apperrors.CodeLocalIOError, "snapshot directory contains an unexpected entry", nil)
		}
		if err := verifyMutationSpoolFile(filepath.Join(path, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func validateAuditEvidencePath(path string) error {
	auditPath, err := absoluteCleanPath(path)
	if err != nil {
		return err
	}
	configPath, err := dbgovctx.ConfigPath()
	if err != nil {
		return err
	}
	configPath, err = absoluteCleanPath(configPath)
	if err != nil {
		return err
	}
	configLock := filepath.Join(filepath.Dir(configPath), "config.lock")
	spoolDir := auditPath + mutationAuditSpoolSuffix
	snapshotDir := filepath.Join(filepath.Dir(auditPath), "snapshots")
	auditArtifacts := []string{
		auditPath,
		auditPath + ".lock",
		auditPath + ".mutation-audit.lock",
		auditPath + mutationAuditSpoolSuffix + ".lock",
		auditPath + ".checkpoint",
	}
	for _, configArtifact := range []string{configPath, configLock} {
		for _, auditArtifact := range auditArtifacts {
			if pathsOverlap(configArtifact, auditArtifact) {
				return reservedMutationPathError()
			}
		}
		if pathWithin(spoolDir, configArtifact) || pathWithin(snapshotDir, configArtifact) ||
			looksLikeStrictRotatedAuditPath(auditPath, configArtifact) {
			return reservedMutationPathError()
		}
	}
	return nil
}

func rejectReservedLocalMutationDirectory(path string) error {
	reservedFiles, reservedDirs, err := localMutationReservedPaths()
	if err != nil {
		return err
	}
	for _, reserved := range reservedFiles {
		if pathsOverlap(path, reserved) {
			return reservedMutationPathError()
		}
	}
	for _, reserved := range reservedDirs {
		if pathsOverlap(path, reserved) {
			return reservedMutationPathError()
		}
	}
	return nil
}

func rejectReservedLocalMutationFile(path string) error {
	reservedFiles, reservedDirs, err := localMutationReservedPaths()
	if err != nil {
		return err
	}
	for _, reserved := range reservedFiles {
		if samePath(path, reserved) {
			return reservedMutationPathError()
		}
	}
	for _, reserved := range reservedDirs {
		if pathWithin(reserved, path) {
			return reservedMutationPathError()
		}
	}
	return nil
}

func localMutationReservedPaths() ([]string, []string, error) {
	auditPath, err := coreaudit.DefaultPath()
	if err != nil {
		return nil, nil, err
	}
	auditPath, err = absoluteCleanPath(auditPath)
	if err != nil {
		return nil, nil, err
	}
	configPath, err := dbgovctx.ConfigPath()
	if err != nil {
		return nil, nil, err
	}
	configPath, err = absoluteCleanPath(configPath)
	if err != nil {
		return nil, nil, err
	}
	files := []string{
		auditPath,
		auditPath + ".lock",
		auditPath + ".mutation-audit.lock",
		auditPath + mutationAuditSpoolSuffix + ".lock",
		auditPath + ".checkpoint",
		configPath,
		filepath.Join(filepath.Dir(configPath), "config.lock"),
	}
	dirs := []string{
		auditPath + mutationAuditSpoolSuffix,
		filepath.Join(filepath.Dir(auditPath), "snapshots"),
	}
	return files, dirs, nil
}

func absoluteCleanPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", apperrors.New(apperrors.CodeValidationFailed, "failed to resolve local path", err)
	}
	return filepath.Clean(absolute), nil
}

func pathsOverlap(first, second string) bool {
	return pathWithin(first, second) || pathWithin(second, first)
}

func pathWithin(parent, child string) bool {
	parentKey := mutationPathKey(filepath.Clean(parent))
	childKey := mutationPathKey(filepath.Clean(child))
	if parentKey == childKey {
		return true
	}
	separator := string(filepath.Separator)
	if parentKey == mutationPathKey(filepath.VolumeName(parent)+separator) {
		return strings.HasPrefix(childKey, parentKey)
	}
	return strings.HasPrefix(childKey, strings.TrimRight(parentKey, separator)+separator)
}

func samePath(first, second string) bool {
	return mutationPathKey(filepath.Clean(first)) == mutationPathKey(filepath.Clean(second))
}

func mutationPathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func reservedMutationPathError() error {
	return apperrors.New(
		apperrors.CodeValidationFailed,
		"mutation target conflicts with reserved audit, config, lock, spool, or snapshot evidence",
		nil,
	)
}

func mutationPathCollisionError(path string) error {
	return apperrors.New(
		apperrors.CodeValidationFailed,
		fmt.Sprintf("multiple mutation items resolve to the same path: %s", path),
		nil,
	)
}
