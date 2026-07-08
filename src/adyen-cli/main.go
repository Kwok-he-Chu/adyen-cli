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
	case "setup-integration":
		err = runSetupIntegration(os.Args[2:])
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
	// ResolveReference replaces the entire path when the reference starts with "/",
	// which would strip the version segment from the base URL (e.g. /v72).
	// Instead, join the base path and the relative path explicitly.
	resolved := *base
	resolved.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(relative.Path, "/")
	resolved.RawQuery = relative.RawQuery
	resolved.Fragment = relative.Fragment
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
	fmt.Fprintf(os.Stderr, "usage: %s <command> [flags]\n", program)
	fmt.Fprintln(os.Stderr, "  specs              list generated Adyen specs")
	fmt.Fprintln(os.Stderr, "  request            issue a raw request against one generated spec")
	fmt.Fprintln(os.Stderr, "  setup-integration  run all Management API setup steps in one shot")
}

// runSetupIntegration performs the full Adyen Checkout integration setup sequence:
//  1. Create merchant API credential  (→ saves apiKey + credentialId)
//  2. Generate client key             (→ saves clientKey)
//  3. Register allowed origin
//  4. Create standard webhook         (→ saves webhookId)
//  5. Generate HMAC key               (→ saves hmacKey, waits 1 s for propagation)
//  6. List configured payment methods
//
// On success it writes a ready-to-use .env block to --env-file (default: .env).
func runSetupIntegration(args []string) error {
	flags := flag.NewFlagSet("setup-integration", flag.ContinueOnError)
	mgmtKey := flags.String("mgmt-api-key", os.Getenv("ADYEN_MGMT_API_KEY"), "Management API key")
	merchantID := flags.String("merchant-id", "", "Merchant account ID")
	webhookURL := flags.String("webhook-url", "", "Webhook endpoint URL (e.g. https://tunnel.ngrok.io/api/webhook)")
	origin := flags.String("origin", "http://localhost:3000", "Allowed origin domain for the Drop-in SDK")
	baseURL := flags.String("base-url", "https://management-test.adyen.com/v3", "Management API base URL")
	description := flags.String("description", "AgentX integration credential", "Credential description")
	envFile := flags.String("env-file", ".env", "Path to the .env file to write credentials into")
	timeout := flags.Duration("timeout", 30*time.Second, "per-request timeout")

	if err := flags.Parse(args); err != nil {
		return err
	}
	if *mgmtKey == "" {
		return errors.New("missing --mgmt-api-key (or set ADYEN_MGMT_API_KEY)")
	}
	if *merchantID == "" {
		return errors.New("missing --merchant-id")
	}
	if *webhookURL == "" {
		return errors.New("missing --webhook-url")
	}

	client := &http.Client{Timeout: *timeout}
	call := func(method, path, body string) (map[string]any, error) {
		u, err := url.Parse(*baseURL + path)
		if err != nil {
			return nil, err
		}
		var bodyReader io.Reader
		if body != "" {
			bodyReader = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(context.Background(), method, u.String(), bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-API-Key", *mgmtKey)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("request failed: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
		}
		var result map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &result); err != nil {
				return nil, fmt.Errorf("could not parse response: %w", err)
			}
		}
		return result, nil
	}

	strField := func(m map[string]any, key string) (string, error) {
		v, ok := m[key]
		if !ok {
			return "", fmt.Errorf("field %q missing in response", key)
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("field %q is not a string", key)
		}
		return s, nil
	}

	// ── Step 1: Create merchant API credential ───────────────────────────────
	fmt.Fprintln(os.Stderr, "→ Step 1: Creating merchant API credential...")
	credBody, err := json.Marshal(map[string]any{
		"roles":       []string{"Checkout webservice role"},
		"description": *description,
	})
	if err != nil {
		return err
	}
	credResp, err := call(http.MethodPost,
		fmt.Sprintf("/merchants/%s/apiCredentials", *merchantID),
		string(credBody))
	if err != nil {
		return fmt.Errorf("step 1 (create credential): %w", err)
	}
	credentialID, err := strField(credResp, "id")
	if err != nil {
		return fmt.Errorf("step 1: %w", err)
	}
	checkoutAPIKey, err := strField(credResp, "apiKey")
	if err != nil {
		return fmt.Errorf("step 1: %w", err)
	}
	fmt.Fprintf(os.Stderr, "   credential ID: %s\n", credentialID)
	fmt.Fprintln(os.Stderr, "   apiKey captured (written to output)")

	// ── Step 2: Generate client key ──────────────────────────────────────────
	fmt.Fprintln(os.Stderr, "→ Step 2: Generating client key...")
	ckResp, err := call(http.MethodPost,
		fmt.Sprintf("/merchants/%s/apiCredentials/%s/generateClientKey", *merchantID, credentialID),
		"")
	if err != nil {
		return fmt.Errorf("step 2 (generate client key): %w", err)
	}
	clientKey, err := strField(ckResp, "clientKey")
	if err != nil {
		return fmt.Errorf("step 2: %w", err)
	}
	fmt.Fprintf(os.Stderr, "   clientKey: %s\n", clientKey)

	// ── Step 3: Register allowed origin ──────────────────────────────────────
	fmt.Fprintf(os.Stderr, "→ Step 3: Registering allowed origin %q...\n", *origin)
	originBody, err := json.Marshal(map[string]string{"domain": *origin})
	if err != nil {
		return err
	}
	if _, err = call(http.MethodPost,
		fmt.Sprintf("/merchants/%s/apiCredentials/%s/allowedOrigins", *merchantID, credentialID),
		string(originBody)); err != nil {
		return fmt.Errorf("step 3 (register origin): %w", err)
	}
	fmt.Fprintln(os.Stderr, "   origin registered")

	// ── Step 4: Create standard webhook ──────────────────────────────────────
	fmt.Fprintf(os.Stderr, "→ Step 4: Creating webhook for %q...\n", *webhookURL)
	whBody, err := json.Marshal(map[string]any{
		"type":                "standard",
		"url":                 *webhookURL,
		"active":              true,
		"communicationFormat": "json",
	})
	if err != nil {
		return err
	}
	whResp, err := call(http.MethodPost,
		fmt.Sprintf("/merchants/%s/webhooks", *merchantID),
		string(whBody))
	if err != nil {
		return fmt.Errorf("step 4 (create webhook): %w", err)
	}
	webhookID, err := strField(whResp, "id")
	if err != nil {
		return fmt.Errorf("step 4: %w", err)
	}
	fmt.Fprintf(os.Stderr, "   webhook ID: %s\n", webhookID)

	// ── Step 5: Generate HMAC key (wait 1 s for propagation) ─────────────────
	fmt.Fprintln(os.Stderr, "→ Step 5: Waiting 1 s then generating HMAC key...")
	time.Sleep(1 * time.Second)
	hmacResp, err := call(http.MethodPost,
		fmt.Sprintf("/merchants/%s/webhooks/%s/generateHmac", *merchantID, webhookID),
		"")
	if err != nil {
		return fmt.Errorf("step 5 (generate HMAC): %w", err)
	}
	hmacKey, err := strField(hmacResp, "hmacKey")
	if err != nil {
		return fmt.Errorf("step 5: %w", err)
	}
	fmt.Fprintln(os.Stderr, "   hmacKey captured (written to output)")

	// ── Step 6: List configured payment methods ───────────────────────────────
	fmt.Fprintln(os.Stderr, "→ Step 6: Listing configured payment methods...")
	pmResp, err := call(http.MethodGet,
		fmt.Sprintf("/merchants/%s/paymentMethodSettings", *merchantID),
		"")
	if err != nil {
		// Non-fatal: the "Payment methods read" role may not be on this key.
		fmt.Fprintf(os.Stderr, "   skipped (error: %v)\n", err)
		fmt.Fprintln(os.Stderr, "   (ensure 'Management API—Payment methods read' role is granted if you need this)")
	} else {
		cardNetworkTypes := map[string]bool{
			"visa": true, "mc": true, "amex": true, "maestro": true,
			"diners": true, "jcb": true, "discover": true, "cup": true,
			"cartesbancaires": true, "bcmc": true,
		}
		if data, ok := pmResp["data"].([]any); ok {
			var cards, other []string
			for _, item := range data {
				m, ok := item.(map[string]any)
				if !ok {
					continue
				}
				enabled, _ := m["enabled"].(bool)
				if !enabled {
					continue
				}
				t, _ := m["type"].(string)
				if cardNetworkTypes[strings.ToLower(t)] {
					cards = append(cards, t)
				} else {
					other = append(other, t)
				}
			}
			fmt.Fprintf(os.Stderr, "   card-network types (→ scheme): %v\n", cards)
			fmt.Fprintf(os.Stderr, "   other payment methods:          %v\n", other)
		}
	}

	// ── Write .env file ───────────────────────────────────────────────────────
	envContent := fmt.Sprintf(
		"ADYEN_API_KEY=%s\nADYEN_CLIENT_KEY=%s\nADYEN_HMAC_KEY=%s\nADYEN_MERCHANT_ACCOUNT=%s\n",
		checkoutAPIKey, clientKey, hmacKey, *merchantID,
	)
	if err := os.WriteFile(*envFile, []byte(envContent), 0600); err != nil {
		return fmt.Errorf("writing %s: %w", *envFile, err)
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Printf("ADYEN_API_KEY=%s\n", checkoutAPIKey[len(checkoutAPIKey)-4:])
	fmt.Printf("ADYEN_CLIENT_KEY=%s\n", clientKey)
	fmt.Printf("ADYEN_HMAC_KEY=%s\n", hmacKey[len(hmacKey)-4:])
	fmt.Printf("ADYEN_MERCHANT_ACCOUNT=%s\n", *merchantID)
	fmt.Fprintf(os.Stderr, "Written credentials to %s\n", *envFile)
	return nil
}
