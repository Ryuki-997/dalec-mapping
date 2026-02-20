package github

import (
	"dalec-mapping/domain/repository"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
)

type GitHubCrawler struct{}

// ClearResultDirectory removes all contents from the result directory
func ClearResultDirectory(resultDir string) error {
	// Check if directory exists
	if _, err := os.Stat(resultDir); os.IsNotExist(err) {
		// Directory doesn't exist, nothing to clear
		return nil
	}

	// Remove all contents
	err := os.RemoveAll(resultDir)
	if err != nil {
		return fmt.Errorf("failed to clear result directory: %w", err)
	}

	fmt.Printf("🗑️  Cleared result directory: %s\n", resultDir)
	return nil
}

// FindFiles searches for files under a root directory matching struct field tags or names.
// The struct should have []string fields with yaml/json tags corresponding to filenames
// (e.g., `yaml:"Dockerfile"`, `yaml:"onboard.yml"`). Tag values fall back to field names.
// Each field holds the found file contents (not paths).
func FindFiles(root string, result interface{}) error {
	if result == nil {
		return fmt.Errorf("result struct cannot be nil")
	}

	resultVal := reflect.ValueOf(result)
	if resultVal.Kind() != reflect.Ptr {
		return fmt.Errorf("result must be a pointer to a struct")
	}

	resultVal = resultVal.Elem()
	if resultVal.Kind() != reflect.Struct {
		return fmt.Errorf("result must be a pointer to a struct")
	}

	// Build map of lowercase tag values (or field names) to field names
	targetFiles := make(map[string]string)
	for i := 0; i < resultVal.NumField(); i++ {
		field := resultVal.Type().Field(i)
		if field.Type.Kind() != reflect.Slice || field.Type.Elem().Kind() != reflect.String {
			continue
		}
		
		matchKey := strings.ToLower(resolveFieldTagKey(field))
		if matchKey == "" {
			continue
		}
		targetFiles[matchKey] = field.Name
	}
	
	if len(targetFiles) == 0 {
		return fmt.Errorf("struct has no []string fields with yaml/json tags")
	}
	
	owner, repo, branch, subdir := ExtractRepositorySegments(root)

	// Get the tree SHA for the branch
	branchURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", owner, repo, branch)

	data, err := MakeGitHubRequest[map[string]interface{}](repository.GithubRequest{URL: branchURL})
	if err != nil {
		return fmt.Errorf("failed to fetch tree: %w", err)
	}

	tree, ok := data["tree"].([]interface{})
	if !ok {
		return fmt.Errorf("unexpected tree response format")
	}

	skipDirs := map[string]bool{
		"vendor":       true,
		"node_modules": true,
		".git":         true,
		".github":      true,
		"testdata":     true,
		"test":         true,
		"tests":        true,
		"examples":     true,
		"docs":         true,
		"doc":          true,
		"charts":       true,
		"hack":         true,
		"scripts":      true,
	}

	// Initialize each field as empty slice
	for _, fieldName := range targetFiles {
		field := resultVal.FieldByName(fieldName)
		field.Set(reflect.MakeSlice(field.Type(), 0, 0))
	}

	for _, item := range tree {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		path, ok := itemMap["path"].(string)
		if !ok {
			continue
		}

		itemType, _ := itemMap["type"].(string)
		if itemType != "blob" { // Only process files, not trees
			continue
		}

		// Skip if in subdirectory and path doesn't start with it
		if subdir != "" && !strings.HasPrefix(path, subdir+"/") && path != subdir {
			continue
		}

		// Check if path is in a skip directory
		pathParts := strings.Split(path, "/")
		skip := false
		for _, part := range pathParts {
			if skipDirs[part] {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		fileName := pathParts[len(pathParts)-1]
		lowerName := strings.ToLower(fileName)

		// Match filename against target files (handling special cases like dockerfile/makefile)
		var matchedFieldName string
		for targetFile, fieldName := range targetFiles {
			// Exact match or special handling for dockerfile/makefile
			if lowerName == targetFile {
				matchedFieldName = fieldName
				break
			}
			// Handle dockerfile(s) tag matching Dockerfile files
			if strings.Contains(targetFile, "dockerfile") && (lowerName == "dockerfile" || strings.HasSuffix(lowerName, ".dockerfile")) {
				matchedFieldName = fieldName
				break
			}
			// Handle makefile(s) tag matching Makefile files  
			if strings.Contains(targetFile, "makefile") && lowerName == "makefile" {
				matchedFieldName = fieldName
				break
			}
		}

		if matchedFieldName == "" {
			continue
		}

		content, err := fetchFileContent(owner, repo, branch, path)
		if err != nil {
			fmt.Printf("  ⚠️  Warning: failed to fetch content of %s: %v\n", path, err)
			continue
		}
		field := resultVal.FieldByName(matchedFieldName)
		field.Set(reflect.Append(field, reflect.ValueOf(content)))
	}

	return nil
}

// fetchFileContent fetches the raw content of a file from GitHub
func fetchFileContent(owner, repo, branch, path string) (string, error) {
	url := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, path)
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch file: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

// resolveFieldTagKey returns the yaml/json tag value or falls back to field name
func resolveFieldTagKey(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")

	if tag != "" && tag != "-" {
		parts := strings.Split(tag, ",")
		return strings.TrimSpace(parts[0])
	}

	return field.Name
}