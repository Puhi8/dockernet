package app

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var defaultIgnorePaths = []string{
	".vscode",
	".vscode-server",
}

func discoverComposeFiles(roots []string, ignorePaths []string) ([]string, []string) {
	ignoreRules := normalizeIgnorePaths(append(append([]string(nil), defaultIgnorePaths...), ignorePaths...))

	warnings := make([]string, 0)
	files := make(map[string]struct{})
	visitedDirs := make(map[string]struct{})

	var walkRoot func(string)
	walkRoot = func(root string) {
		root = strings.TrimSpace(root)
		if root == "" {
			return
		}

		absRoot, err := filepath.Abs(root)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("resolve root %q: %v", root, err))
			return
		}

		realRoot, err := filepath.EvalSymlinks(absRoot)
		if err == nil {
			absRoot = realRoot
		}

		if _, seen := visitedDirs[absRoot]; seen {
			return
		}
		visitedDirs[absRoot] = struct{}{}

		walkErr := filepath.WalkDir(absRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				if !os.IsNotExist(err) {
					warnings = append(warnings, fmt.Sprintf("scan path %q: %v", path, err))
				}
				return nil
			}

			cleanPath := filepath.Clean(path)
			if shouldIgnorePath(cleanPath, ignoreRules) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if entry.Type()&os.ModeSymlink != 0 {
				resolved, err := filepath.EvalSymlinks(path)
				if err != nil {
					if !os.IsNotExist(err) {
						warnings = append(warnings, fmt.Sprintf("resolve symlink %q: %v", path, err))
					}
					return nil
				}

				info, err := os.Stat(resolved)
				if err != nil {
					if !os.IsNotExist(err) {
						warnings = append(warnings, fmt.Sprintf("stat symlink target %q: %v", resolved, err))
					}
					return nil
				}

				if info.IsDir() {
					walkRoot(resolved)
					if entry.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}

				if isComposeFile(path) || isComposeFile(resolved) {
					absFile, absErr := filepath.Abs(resolved)
					if absErr == nil {
						files[absFile] = struct{}{}
					} else {
						files[filepath.Clean(resolved)] = struct{}{}
					}
				}
				return nil
			}

			if entry.IsDir() {
				return nil
			}

			if isComposeFile(path) {
				absFile, absErr := filepath.Abs(path)
				if absErr == nil {
					files[absFile] = struct{}{}
				} else {
					files[filepath.Clean(path)] = struct{}{}
				}
			}
			return nil
		})
		if walkErr != nil {
			warnings = append(warnings, fmt.Sprintf("scan root %q: %v", absRoot, walkErr))
		}
	}

	for _, root := range roots {
		walkRoot(root)
	}

	discovered := make([]string, 0, len(files))
	for path := range files {
		discovered = append(discovered, path)
	}
	sort.Strings(discovered)
	return discovered, warnings
}

func filterComposeFilesByVolumePaths(files []string, volumePaths []string) []string {
	if len(files) == 0 || len(volumePaths) == 0 {
		return files
	}

	filterRules := normalizeIgnorePaths(volumePaths)
	filtered := make([]string, 0, len(files))
	for _, file := range files {
		if !shouldIgnorePath(file, filterRules) {
			filtered = append(filtered, file)
		}
	}
	sort.Strings(filtered)
	return filtered
}

func normalizeIgnorePaths(ignore []string) []string {
	normalized := make([]string, 0, len(ignore))
	for _, raw := range ignore {
		rule := strings.TrimSpace(raw)
		if rule == "" {
			continue
		}
		rule = strings.TrimSuffix(filepath.Clean(rule), string(os.PathSeparator))
		if rule != "." {
			normalized = append(normalized, rule)
		}
	}
	return dedupeStrings(normalized)
}

func shouldIgnorePath(path string, ignoreRules []string) bool {
	cleanPath := filepath.Clean(path)
	for _, rule := range ignoreRules {
		if ignoreRuleMatches(cleanPath, rule) {
			return true
		}
	}
	return false
}

func ignoreRuleMatches(path, rule string) bool {
	sep := string(os.PathSeparator)
	if filepath.IsAbs(rule) {
		return path == rule || strings.HasPrefix(path, rule+sep)
	}
	return (path == rule || strings.HasSuffix(path, sep+rule) || strings.Contains(path, sep+rule+sep))
}

func isComposeFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yml" || ext == ".yaml"
}

func stripCIDR(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if ip, _, found := strings.Cut(value, "/"); found {
		return strings.TrimSpace(ip)
	}
	return value
}
