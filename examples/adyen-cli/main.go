package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed manifest.json
var manifestFS embed.FS

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

type multiValue []string

func (m *multiValue) String() string { return strings.Join(*m, ",") }
func (m *multiValue) Set(value string) error {
	*m = append(*m, value)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "specs":
		err = runSpecs()
	case "request":
		err = runRequest(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		err = errors.New("unknown command")
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSpecs() error {
	man, err := loadManifest()
	if err != nil {
		return err
	}
	for _, svc := range man.Services {
		if svc.BaseURL != "" {
			fmt.Printf("%s\t%s\t%s\n", svc.Name, svc.BaseURL, svc.OutDir)
			continue
		}
		fmt.Printf("%s\t%s\n", svc.Name, svc.OutDir)
	}
	return nil
}

func runRequest(args []string) error {
	man, err := loadManifest()
	if err != nil {
		return err
	}

	flags := flag.NewFlagSet("request", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	serviceName := flags.String("spec", "", "spec name from the manifest")
	baseURL := flags.String("base-url", "", "override base URL")
	method := flags.String("method", http.MethodGet, "HTTP method")
	path := flags.String("path", "", "request path")
	apiKey := flags.String("api-key", os.Getenv("ADYEN_API_KEY"), "Adyen API key")
	body := flags.String("body", "", "request body")
	bodyFile := flags.String("body-file", "", "path to a file containing the request body")
	timeout := flags.Duration("timeout", 30*time.Second, "request timeout")
	var headers multiValue
	var queries multiValue
	flags.Var(&headers, "header", "additional request header in Key:Value form")
	flags.Var(&queries, "query", "query parameter in key=value form")

	if err := flags.Parse(args); err != nil {
		return err
	}
	if *serviceName == "" {
		return errors.New("missing --spec")
	}
	if *path == "" {
		return errors.New("missing --path")
	}

	svc, ok := findService(man.Services, *serviceName)
	if !ok {
		return fmt.Errorf("unknown spec %q", *serviceName)
	}
	if *baseURL == "" {
		*baseURL = svc.BaseURL
	}
	if strings.TrimSpace(*baseURL) == "" {
		return errors.New("missing base URL; pass --base-url or use a spec with a server URL")
	}

	bodyText, err := readBody(*body, *bodyFile)
	if err != nil {
		return err
	}
	requestURL, err := buildURL(*baseURL, *path, queries)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(context.Background(), *method, requestURL, strings.NewReader(bodyText))
	if err != nil {
		return err
	}
	if bodyText == "" {
		req.Body = nil
	}
	if *apiKey != "" {
		req.Header.Set("X-API-Key", *apiKey)
	}
	for _, header := range headers {
		name, value, ok := strings.Cut(header, ":")
		if !ok {
			return fmt.Errorf("invalid header %q, expected Key:Value", header)
		}
		req.Header.Add(strings.TrimSpace(name), strings.TrimSpace(value))
	}
	if bodyText != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := (&http.Client{Timeout: *timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("request failed: %s: %s", resp.Status, strings.TrimSpace(string(responseBody)))
	}

	formatted, err := formatJSON(responseBody)
	if err != nil {
		_, writeErr := os.Stdout.Write(responseBody)
		if writeErr != nil {
			return writeErr
		}
		if len(responseBody) > 0 && responseBody[len(responseBody)-1] != '\n' {
			_, writeErr = os.Stdout.Write([]byte("\n"))
			if writeErr != nil {
				return writeErr
			}
		}
		return nil
	}

	_, err = os.Stdout.Write(formatted)
	return err
}

func loadManifest() (manifest, error) {
	data, err := manifestFS.ReadFile("manifest.json")
	if err != nil {
		return manifest{}, err
	}
	var man manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return manifest{}, err
	}
	return man, nil
}

func findService(services []service, name string) (service, bool) {
	cleaned := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	for _, svc := range services {
		if svc.Name == name || svc.Name == cleaned || strings.TrimSuffix(filepath.Base(svc.Spec), filepath.Ext(svc.Spec)) == cleaned {
			return svc, true
		}
	}
	return service{}, false
}

func readBody(body, bodyFile string) (string, error) {
	if bodyFile != "" {
		data, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return body, nil
}

func buildURL(baseURL, path string, queries []string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	relative, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(relative)
	params := resolved.Query()
	for _, query := range queries {
		key, value, ok := strings.Cut(query, "=")
		if !ok {
			return "", fmt.Errorf("invalid query %q, expected key=value", query)
		}
		params.Add(strings.TrimSpace(key), strings.TrimSpace(value))
	}
	resolved.RawQuery = params.Encode()
	return resolved.String(), nil
}

func formatJSON(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, err
	}
	formatted, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(formatted, '\n'), nil
}

func usage() {
	program := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, "usage: %s <specs|request> [flags]\n", program)
	fmt.Fprintln(os.Stderr, "  specs    list generated Adyen specs")
	fmt.Fprintln(os.Stderr, "  request  issue a raw request against one generated spec")
}
