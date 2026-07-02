package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type manifest struct {
	Services []service `json:"services"`
}

type service struct {
	Name    string `json:"name"`
	Spec    string `json:"spec"`
	Package string `json:"package"`
	BaseURL string `json:"baseURL"`
	OutDir  string `json:"outDir"`
}

type candidate struct {
	Name     string
	SpecPath string
	Folder   string
	Package  string
	BaseURL  string
}

var versionPattern = regexp.MustCompile(`^(.*)-v([0-9]+)\.yaml$`)

func main() {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}

	srcRoot := filepath.Join(repoRoot, "src")

	specDir := filepath.Join(repoRoot, "adyen-openapi", "yaml")
	outputRoot := filepath.Join(repoRoot, "src", "adyen-cli", "generated")
	manifestPath := filepath.Join(repoRoot, "src", "adyen-cli", "manifest.json")

	if err := os.RemoveAll(outputRoot); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		panic(err)
	}

	candidates, err := collectAll(specDir)
	if err != nil {
		panic(err)
	}

	man := manifest{Services: make([]service, 0, len(candidates))}
	for _, cand := range candidates {
		outDir := filepath.Join(outputRoot, cand.Folder)
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			panic(err)
		}

		outputPath := filepath.Join(outDir, "client.gen.go")
		cfgPath := filepath.Join(outDir, "cfg.yaml")
		if err := os.WriteFile(cfgPath, []byte(cfgContent(cand.Package, outputPath)), 0o644); err != nil {
			panic(err)
		}

		cmd := exec.Command("go", "run", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen", "-config", cfgPath, cand.SpecPath)
		cmd.Dir = srcRoot
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %s: %v\n", cand.Name, err)
			continue
		}

		man.Services = append(man.Services, service{
			Name:    cand.Name,
			Spec:    cand.SpecPath,
			Package: cand.Folder,
			BaseURL: cand.BaseURL,
			OutDir:  outDir,
		})
	}

	manifestData, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		panic(err)
	}
	manifestData = append(manifestData, '\n')
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		panic(err)
	}
}

func collectAll(specDir string) ([]candidate, error) {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return nil, err
	}

	var result []candidate
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") || strings.Contains(entry.Name(), "Notification") || strings.Contains(strings.ToLower(entry.Name()), "webhook") {
			continue
		}

		if versionPattern.FindStringSubmatch(entry.Name()) == nil {
			continue
		}

		specPath := filepath.Join(specDir, entry.Name())
		result = append(result, candidate{
			Name:     entry.Name(),
			SpecPath: specPath,
			Folder:   slugify(strings.TrimSuffix(entry.Name(), ".yaml")),
			Package:  packageName(strings.TrimSuffix(entry.Name(), ".yaml")),
			BaseURL:  firstServerURL(specPath),
		})
	}
	return result, nil
}

func slugify(name string) string {
	if idx := strings.LastIndex(name, "-v"); idx >= 0 {
		return kebabCase(name[:idx]) + name[idx:]
	}
	return kebabCase(name)
}

func kebabCase(name string) string {
	var builder strings.Builder
	for i, r := range name {
		if i > 0 {
			prev := rune(name[i-1])
			nextIsLower := i+1 < len(name) && name[i+1] >= 'a' && name[i+1] <= 'z'
			if (prev >= 'a' && prev <= 'z' && r >= 'A' && r <= 'Z') || (prev >= '0' && prev <= '9' && r >= 'A' && r <= 'Z') || (prev >= 'A' && prev <= 'Z' && r >= 'A' && r <= 'Z' && nextIsLower) {
				builder.WriteByte('-')
			}
		}
		builder.WriteRune(r)
	}
	return strings.ToLower(builder.String())
}

func packageName(name string) string {
	return strings.ReplaceAll(kebabCase(name), "-", "")
}

func firstServerURL(specPath string) string {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return ""
	}

	var doc struct {
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return ""
	}
	if len(doc.Servers) == 0 {
		return ""
	}
	return doc.Servers[0].URL
}

func cfgContent(pkg, outputPath string) string {
	return fmt.Sprintf("package: %s\noutput: %s\ngenerate:\n  client: true\n  models: true\noutput-options:\n  skip-prune: true\n  prefer-skip-optional-pointer: true\n  response-type-suffix: Resp\n", pkg, outputPath)
}
