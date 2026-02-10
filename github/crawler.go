package github

import (
	"dalec-mapping/global"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
)

type GitHubCrawler struct{}

// FindFiles searches for files under a root directory matching struct field names
// The struct should have fields with names corresponding to files (e.g., Dockerfiles, Makefiles, OnboardYml)
// Each field should be a []string slice to hold found file contents (not paths)
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

	targetFiles := make(map[string]string) 
	for i := 0; i < resultVal.NumField(); i++ {
		field := resultVal.Type().Field(i)
		fieldType := field.Type
		
		// Check if field is a []string slice
		if fieldType.Kind() != reflect.Slice || fieldType.Elem().Kind() != reflect.String {
			continue
		}

		// Convert field name to lowercase for file matching
		lowerFieldName := strings.ToLower(field.Name)
		targetFiles[lowerFieldName] = field.Name
	}

	if len(targetFiles) == 0 {
		return fmt.Errorf("struct has no []string fields to populate")
	}

	owner, repo, branch, subdir := global.ExtractRepositorySegments(root)

	// Get the tree SHA for the branch
	branchURL := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", owner, repo, branch)
	
	data, err := global.MakeGitHubRequest[map[string]interface{}](branchURL)
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

		// Check if this file matches any of our search criteria
		var matchedFieldName string
		for targetFile, fieldName := range targetFiles {
			// Match exact filename or handle special cases like dockerfile
			if lowerName == targetFile || (targetFile == "dockerfile" && (lowerName == "dockerfile" || strings.HasSuffix(lowerName, ".dockerfile"))) || (targetFile == "makefile" && lowerName == "makefile") {
				matchedFieldName = fieldName
				break
			}
		}

		if matchedFieldName == "" {
			continue
		}

		// Fetch content from GitHub
		content, err := fetchFileContent(owner, repo, branch, path)
		if err != nil {
			fmt.Printf("  ⚠️  Warning: failed to fetch content of %s: %v\n", path, err)
			continue
		}

		// Append content to the struct field
		field := resultVal.FieldByName(matchedFieldName)
		field.Set(reflect.Append(field, reflect.ValueOf(content)))
		fmt.Printf("  📄 Found %s: %s (%d bytes)\n", matchedFieldName, path, len(content))
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
