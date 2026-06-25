package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	clientID              = "app_EMoamEEZ73f0CkXaXp7hrann"
	authBaseURL           = "https://auth.openai.com"
	deviceUserCodeURL     = authBaseURL + "/api/accounts/deviceauth/usercode"
	deviceTokenURL        = authBaseURL + "/api/accounts/deviceauth/token"
	deviceVerificationURI = authBaseURL + "/codex/device"
	deviceRedirectURI     = authBaseURL + "/deviceauth/callback"
	tokenURL              = authBaseURL + "/oauth/token"
	codexBaseURL          = "https://chatgpt.com/backend-api"
	jwtClaimPath          = "https://api.openai.com/auth"
	defaultPrompt         = "Improve this transcript for readability and accuracy. Fix obvious ASR mistakes, punctuation, capitalization, speaker-flow issues, and paragraphing. Preserve meaning, names, technical terms, and the original language. Do not summarize. Output only the enhanced transcript."
)

type config struct {
	provider        string
	model           string
	baseURL         string
	apiKey          string
	codexHome       string
	blitzHome       string
	skillsDir       string
	skill           string
	prompt          string
	serviceTier     string
	reasoningEffort string
	maxOutputTokens int
	timeout         time.Duration
	stream          bool
	fast            bool
	input           string
}

type authFile struct {
	Tokens      tokenData `json:"tokens"`
	LastRefresh string    `json:"last_refresh,omitempty"`
}

type tokenData struct {
	IDToken      any    `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id,omitempty"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "blitz:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "login":
			return loginCommand(args[1:])
		case "logout":
			return logoutCommand(args[1:])
		case "auth":
			return authCommand(args[1:])
		case "help", "-h", "--help":
			printUsage()
			return nil
		}
	}

	cfg, err := parseRunFlags(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	return enhance(ctx, cfg, os.Stdout)
}

func parseRunFlags(args []string) (config, error) {
	home, _ := os.UserHomeDir()
	blitzHome := preFlagString(args, "blitz-home", envDefault("BLITZ_HOME", filepath.Join(home, ".blitz")))
	skillsDir := preFlagString(args, "skills-dir", envDefault("BLITZ_SKILLS_DIR", filepath.Join(blitzHome, "skills")))
	cfg := config{
		provider:        envDefault("BLITZ_PROVIDER", "codex"),
		model:           envDefault("BLITZ_MODEL", "gpt-5.4-mini"),
		baseURL:         os.Getenv("BLITZ_BASE_URL"),
		apiKey:          os.Getenv("OPENAI_API_KEY"),
		codexHome:       envDefault("CODEX_HOME", filepath.Join(home, ".codex")),
		blitzHome:       blitzHome,
		skillsDir:       skillsDir,
		prompt:          envDefault("BLITZ_PROMPT", defaultPrompt),
		serviceTier:     os.Getenv("BLITZ_SERVICE_TIER"),
		reasoningEffort: envDefault("BLITZ_REASONING_EFFORT", "low"),
		timeout:         10 * time.Minute,
		stream:          true,
		fast:            true,
	}

	fs := flag.NewFlagSet("blitz", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.provider, "provider", cfg.provider, "codex, responses, or chat")
	fs.StringVar(&cfg.model, "model", cfg.model, "model name")
	fs.StringVar(&cfg.baseURL, "base-url", cfg.baseURL, "OpenAI-compatible base URL")
	fs.StringVar(&cfg.apiKey, "api-key", cfg.apiKey, "API key for responses/chat providers")
	fs.StringVar(&cfg.codexHome, "codex-home", cfg.codexHome, "Codex home containing auth.json")
	fs.StringVar(&cfg.blitzHome, "blitz-home", cfg.blitzHome, "Blitz home containing auth.json")
	fs.StringVar(&cfg.skillsDir, "skills-dir", cfg.skillsDir, "directory of skill markdown prompts")
	fs.StringVar(&cfg.prompt, "prompt", cfg.prompt, "transcript enhancement instruction")
	fs.StringVar(&cfg.serviceTier, "service-tier", cfg.serviceTier, "Responses service_tier, for example priority")
	fs.StringVar(&cfg.reasoningEffort, "reasoning", cfg.reasoningEffort, "reasoning effort for Responses/Codex")
	fs.IntVar(&cfg.maxOutputTokens, "max-output-tokens", 0, "optional max output tokens")
	fs.DurationVar(&cfg.timeout, "timeout", cfg.timeout, "request timeout")
	fs.BoolVar(&cfg.stream, "stream", cfg.stream, "stream output as it arrives")
	fs.BoolVar(&cfg.fast, "fast", cfg.fast, "for codex, request priority service tier when unset")

	skills, err := discoverSkills(cfg.skillsDir)
	if err != nil {
		return cfg, err
	}
	skillFlags := make(map[string]*bool, len(skills))
	for _, skill := range skills {
		if fs.Lookup(skill.Name) != nil {
			continue
		}
		skillFlags[skill.Name] = fs.Bool(skill.Name, false, "use "+skill.Name+" skill prompt")
	}

	if err := fs.Parse(args); err != nil {
		printUsage()
		return cfg, err
	}

	explicitPrompt := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "prompt" {
			explicitPrompt = true
		}
	})
	var selectedSkills []skillPrompt
	for _, skill := range skills {
		selected, ok := skillFlags[skill.Name]
		if ok && *selected {
			selectedSkills = append(selectedSkills, skill)
		}
	}
	if len(selectedSkills) > 1 {
		return cfg, fmt.Errorf("choose only one skill prompt, got %s", joinSkillNames(selectedSkills))
	}
	if len(selectedSkills) == 1 {
		if explicitPrompt {
			return cfg, errors.New("use either a skill flag or -prompt, not both")
		}
		prompt, err := loadSkillPrompt(selectedSkills[0])
		if err != nil {
			return cfg, err
		}
		cfg.skill = selectedSkills[0].Name
		cfg.prompt = prompt
	}

	cfg.input = strings.TrimSpace(strings.Join(fs.Args(), " "))
	if cfg.input == "" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return cfg, err
		}
		cfg.input = strings.TrimSpace(string(data))
	}
	if cfg.input == "" {
		return cfg, errors.New("provide transcript text as args or stdin")
	}
	return cfg, nil
}

type skillPrompt struct {
	Name string
	Path string
}

func discoverSkills(dir string) ([]skillPrompt, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills dir: %w", err)
	}

	var skills []skillPrompt
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		if strings.HasPrefix(filename, ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(filename))
		if ext != ".md" && ext != ".markdown" {
			continue
		}
		name := strings.TrimSuffix(filename, filepath.Ext(filename))
		if !validSkillFlagName(name) {
			continue
		}
		skills = append(skills, skillPrompt{
			Name: name,
			Path: filepath.Join(dir, filename),
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

func validSkillFlagName(name string) bool {
	return name != "" && !strings.HasPrefix(name, "-") && !strings.ContainsAny(name, "= \t\r\n")
}

func loadSkillPrompt(skill skillPrompt) (string, error) {
	data, err := os.ReadFile(skill.Path)
	if err != nil {
		return "", fmt.Errorf("read skill %q: %w", skill.Name, err)
	}
	prompt := strings.TrimSpace(string(data))
	if prompt == "" {
		return "", fmt.Errorf("skill %q is empty", skill.Name)
	}
	return prompt, nil
}

func joinSkillNames(skills []skillPrompt) string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, "--"+skill.Name)
	}
	return strings.Join(names, ", ")
}

func preFlagString(args []string, name, fallback string) string {
	long := "--" + name
	short := "-" + name
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, long+"=") {
			return strings.TrimPrefix(arg, long+"=")
		}
		if strings.HasPrefix(arg, short+"=") {
			return strings.TrimPrefix(arg, short+"=")
		}
		if arg == long || arg == short {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return fallback
}

func enhance(ctx context.Context, cfg config, out io.Writer) error {
	switch strings.ToLower(cfg.provider) {
	case "codex", "openai-codex":
		if cfg.fast && cfg.serviceTier == "" {
			cfg.serviceTier = "priority"
		}
		return callCodex(ctx, cfg, out)
	case "responses", "openai-responses":
		return callResponses(ctx, cfg, out)
	case "chat", "chat-completions", "openai-chat":
		return callChat(ctx, cfg, out)
	default:
		return fmt.Errorf("unknown provider %q", cfg.provider)
	}
}

func callCodex(ctx context.Context, cfg config, out io.Writer) error {
	auth, path, err := loadCodexLikeAuth(cfg.blitzHome, cfg.codexHome)
	if err != nil {
		return err
	}
	if shouldRefresh(auth.Tokens.AccessToken) {
		if auth.Tokens.RefreshToken == "" {
			return errors.New("Codex access token is expired and no refresh token is available; run `blitz login` or `codex login`")
		}
		refreshed, err := refreshToken(ctx, auth.Tokens.RefreshToken)
		if err != nil {
			return err
		}
		auth.Tokens.AccessToken = refreshed.AccessToken
		auth.Tokens.RefreshToken = refreshed.RefreshToken
		auth.Tokens.AccountID = accountIDFromToken(refreshed.AccessToken)
		auth.LastRefresh = time.Now().UTC().Format(time.RFC3339)
		if strings.Contains(path, string(filepath.Separator)+".blitz"+string(filepath.Separator)) {
			_ = saveAuth(path, auth)
		}
	}

	accountID := auth.Tokens.AccountID
	if accountID == "" {
		accountID = accountIDFromToken(auth.Tokens.AccessToken)
	}
	if accountID == "" {
		accountID = accountIDFromIDToken(auth.Tokens.IDToken)
	}
	if accountID == "" {
		return errors.New("could not find chatgpt account id in Codex auth")
	}

	endpoint := resolveCodexURL(cfg.baseURL)
	body := codexRequestBody(cfg)

	headers := map[string]string{
		"Authorization":       "Bearer " + auth.Tokens.AccessToken,
		"chatgpt-account-id":  accountID,
		"originator":          "blitz",
		"OpenAI-Beta":         "responses=experimental",
		"Accept":              "text/event-stream",
		"Content-Type":        "application/json",
		"User-Agent":          "blitz/0.1",
		"X-Client-Request-Id": randomID(),
	}
	return postJSON(ctx, endpoint, headers, body, cfg.stream, out)
}

func codexRequestBody(cfg config) map[string]any {
	body := map[string]any{
		"model":        cfg.model,
		"instructions": cfg.prompt,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]string{
					{"type": "input_text", "text": cfg.input},
				},
			},
		},
		"store":  false,
		"stream": cfg.stream,
	}
	addResponsesOptions(body, cfg)
	return body
}

func callResponses(ctx context.Context, cfg config, out io.Writer) error {
	if cfg.apiKey == "" {
		return errors.New("OPENAI_API_KEY or -api-key is required for responses provider")
	}
	endpoint := joinEndpoint(envDefault("BLITZ_BASE_URL", cfg.baseURL), "https://api.openai.com/v1", "/responses")
	body := map[string]any{
		"model":        cfg.model,
		"instructions": cfg.prompt,
		"input":        cfg.input,
		"stream":       cfg.stream,
	}
	addResponsesOptions(body, cfg)
	headers := bearerHeaders(cfg.apiKey, cfg.stream)
	return postJSON(ctx, endpoint, headers, body, cfg.stream, out)
}

func callChat(ctx context.Context, cfg config, out io.Writer) error {
	if cfg.apiKey == "" {
		return errors.New("OPENAI_API_KEY or -api-key is required for chat provider")
	}
	endpoint := joinEndpoint(envDefault("BLITZ_BASE_URL", cfg.baseURL), "https://api.openai.com/v1", "/chat/completions")
	body := map[string]any{
		"model": cfg.model,
		"messages": []map[string]string{
			{"role": "system", "content": cfg.prompt},
			{"role": "user", "content": cfg.input},
		},
		"stream": cfg.stream,
	}
	if cfg.maxOutputTokens > 0 {
		body["max_completion_tokens"] = cfg.maxOutputTokens
	}
	headers := bearerHeaders(cfg.apiKey, cfg.stream)
	return postJSON(ctx, endpoint, headers, body, cfg.stream, out)
}

func addResponsesOptions(body map[string]any, cfg config) {
	if cfg.serviceTier != "" {
		body["service_tier"] = cfg.serviceTier
	}
	if cfg.reasoningEffort != "" {
		body["reasoning"] = map[string]string{"effort": cfg.reasoningEffort}
	}
	if cfg.maxOutputTokens > 0 {
		body["max_output_tokens"] = cfg.maxOutputTokens
	}
}

func postJSON(ctx context.Context, endpoint string, headers map[string]string, body map[string]any, stream bool, out io.Writer) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(resp)
	}
	if stream || isEventStream(resp.Header.Get("Content-Type")) {
		return readSSE(resp.Body, out)
	}
	return readJSONResponse(resp.Body, out)
}

func readSSE(r io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var event strings.Builder
	wrote := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if text := textFromSSEEvent(event.String()); text != "" {
				_, _ = io.WriteString(out, text)
				wrote = true
			}
			event.Reset()
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}
			event.WriteString(data)
			event.WriteByte('\n')
		}
	}
	if event.Len() > 0 {
		if text := textFromSSEEvent(event.String()); text != "" {
			_, _ = io.WriteString(out, text)
			wrote = true
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if wrote {
		_, _ = io.WriteString(out, "\n")
	}
	return nil
}

func textFromSSEEvent(data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(data), &obj); err != nil {
		return ""
	}
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if delta, ok := choice["delta"].(map[string]any); ok {
				if content, ok := delta["content"].(string); ok {
					return content
				}
			}
		}
	}
	if delta, ok := obj["delta"].(string); ok {
		return delta
	}
	if text, ok := obj["text"].(string); ok && strings.Contains(fmt.Sprint(obj["type"]), "delta") {
		return text
	}
	return ""
}

func readJSONResponse(r io.Reader, out io.Writer) error {
	var obj map[string]any
	if err := json.NewDecoder(r).Decode(&obj); err != nil {
		return err
	}
	text := extractFinalText(obj)
	if text == "" {
		pretty, _ := json.MarshalIndent(obj, "", "  ")
		_, _ = out.Write(pretty)
		_, _ = io.WriteString(out, "\n")
		return nil
	}
	_, _ = io.WriteString(out, text)
	if !strings.HasSuffix(text, "\n") {
		_, _ = io.WriteString(out, "\n")
	}
	return nil
}

func extractFinalText(obj map[string]any) string {
	if text, ok := obj["output_text"].(string); ok {
		return text
	}
	if choices, ok := obj["choices"].([]any); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]any); ok {
			if msg, ok := choice["message"].(map[string]any); ok {
				if content, ok := msg["content"].(string); ok {
					return content
				}
			}
		}
	}
	var b strings.Builder
	if output, ok := obj["output"].([]any); ok {
		for _, item := range output {
			m, _ := item.(map[string]any)
			content, _ := m["content"].([]any)
			for _, c := range content {
				cm, _ := c.(map[string]any)
				if text, ok := cm["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
	}
	return b.String()
}

func loginCommand(args []string) error {
	home, _ := os.UserHomeDir()
	blitzHome := envDefault("BLITZ_HOME", filepath.Join(home, ".blitz"))
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&blitzHome, "blitz-home", blitzHome, "Blitz home for auth.json")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	device, err := startDeviceAuth(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Open %s and enter code: %s\n", deviceVerificationURI, device.UserCode)
	code, verifier, err := pollDeviceAuth(ctx, device)
	if err != nil {
		return err
	}
	tok, err := exchangeDeviceCode(ctx, code, verifier)
	if err != nil {
		return err
	}
	auth := authFile{
		Tokens: tokenData{
			AccessToken:  tok.AccessToken,
			RefreshToken: tok.RefreshToken,
			AccountID:    accountIDFromToken(tok.AccessToken),
		},
		LastRefresh: time.Now().UTC().Format(time.RFC3339),
	}
	if auth.Tokens.AccountID == "" {
		return errors.New("login succeeded but access token did not include account id")
	}
	path := filepath.Join(blitzHome, "auth.json")
	if err := saveAuth(path, auth); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved Codex subscription auth to %s\n", path)
	return nil
}

func logoutCommand(args []string) error {
	home, _ := os.UserHomeDir()
	blitzHome := envDefault("BLITZ_HOME", filepath.Join(home, ".blitz"))
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&blitzHome, "blitz-home", blitzHome, "Blitz home for auth.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := filepath.Join(blitzHome, "auth.json")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Fprintf(os.Stderr, "Removed %s\n", path)
	return nil
}

func authCommand(args []string) error {
	home, _ := os.UserHomeDir()
	blitzHome := envDefault("BLITZ_HOME", filepath.Join(home, ".blitz"))
	codexHome := envDefault("CODEX_HOME", filepath.Join(home, ".codex"))
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&blitzHome, "blitz-home", blitzHome, "Blitz home containing auth.json")
	fs.StringVar(&codexHome, "codex-home", codexHome, "Codex home containing auth.json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	auth, path, err := loadCodexLikeAuth(blitzHome, codexHome)
	if err != nil {
		return err
	}
	accountID := auth.Tokens.AccountID
	if accountID == "" {
		accountID = accountIDFromToken(auth.Tokens.AccessToken)
	}
	fmt.Printf("auth_file=%s\naccount_id=%s\naccess_token_expired=%t\n", path, accountID, shouldRefresh(auth.Tokens.AccessToken))
	return nil
}

type deviceAuth struct {
	DeviceAuthID   string
	UserCode       string
	IntervalSecond int
}

func startDeviceAuth(ctx context.Context) (deviceAuth, error) {
	reqBody := strings.NewReader(`{"client_id":"` + clientID + `"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceUserCodeURL, reqBody)
	if err != nil {
		return deviceAuth{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return deviceAuth{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return deviceAuth{}, responseError(resp)
	}
	var raw struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		Interval     any    `json:"interval"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return deviceAuth{}, err
	}
	interval := 5
	switch v := raw.Interval.(type) {
	case float64:
		interval = int(v)
	case string:
		fmt.Sscanf(v, "%d", &interval)
	}
	if raw.DeviceAuthID == "" || raw.UserCode == "" {
		return deviceAuth{}, errors.New("invalid device auth response")
	}
	return deviceAuth{DeviceAuthID: raw.DeviceAuthID, UserCode: raw.UserCode, IntervalSecond: interval}, nil
}

func pollDeviceAuth(ctx context.Context, d deviceAuth) (authorizationCode string, codeVerifier string, err error) {
	interval := time.Duration(d.IntervalSecond) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-timer.C:
		}

		body := fmt.Sprintf(`{"device_auth_id":%q,"user_code":%q}`, d.DeviceAuthID, d.UserCode)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceTokenURL, strings.NewReader(body))
		if err != nil {
			return "", "", err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", "", err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 200 {
			var ok struct {
				AuthorizationCode string `json:"authorization_code"`
				CodeVerifier      string `json:"code_verifier"`
			}
			if err := json.Unmarshal(raw, &ok); err != nil {
				return "", "", err
			}
			if ok.AuthorizationCode == "" || ok.CodeVerifier == "" {
				return "", "", errors.New("invalid device token response")
			}
			return ok.AuthorizationCode, ok.CodeVerifier, nil
		}
		if resp.StatusCode == 403 || resp.StatusCode == 404 || bytes.Contains(raw, []byte("authorization_pending")) {
			timer.Reset(interval)
			continue
		}
		if bytes.Contains(raw, []byte("slow_down")) {
			interval += 5 * time.Second
			timer.Reset(interval)
			continue
		}
		return "", "", fmt.Errorf("device auth failed: %s", strings.TrimSpace(string(raw)))
	}
}

func exchangeDeviceCode(ctx context.Context, code, verifier string) (oauthTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {deviceRedirectURI},
	}
	return requestToken(ctx, form)
}

func refreshToken(ctx context.Context, refresh string) (oauthTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {clientID},
		"refresh_token": {refresh},
	}
	return requestToken(ctx, form)
}

func requestToken(ctx context.Context, form url.Values) (oauthTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return oauthTokenResponse{}, responseError(resp)
	}
	var tok oauthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return oauthTokenResponse{}, err
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		return oauthTokenResponse{}, errors.New("token response missing access_token or refresh_token")
	}
	return tok, nil
}

func loadCodexLikeAuth(blitzHome, codexHome string) (authFile, string, error) {
	paths := []string{
		filepath.Join(blitzHome, "auth.json"),
		filepath.Join(codexHome, "auth.json"),
	}
	var lastErr error
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}
		var auth authFile
		if err := json.Unmarshal(data, &auth); err != nil {
			return authFile{}, path, err
		}
		if auth.Tokens.AccessToken != "" {
			return auth, path, nil
		}
		lastErr = fmt.Errorf("%s did not contain Codex tokens", path)
	}
	return authFile{}, "", fmt.Errorf("no Codex subscription auth found; run `blitz login` or `codex login` first (%v)", lastErr)
}

func saveAuth(path string, auth authFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func shouldRefresh(jwt string) bool {
	exp := jwtExp(jwt)
	if exp.IsZero() {
		return false
	}
	return time.Until(exp) < 2*time.Minute
}

func jwtExp(jwt string) time.Time {
	var payload map[string]any
	if !decodeJWTPayload(jwt, &payload) {
		return time.Time{}
	}
	exp, ok := payload["exp"].(float64)
	if !ok {
		return time.Time{}
	}
	return time.Unix(int64(exp), 0)
}

func accountIDFromToken(jwt string) string {
	var payload map[string]any
	if !decodeJWTPayload(jwt, &payload) {
		return ""
	}
	if auth, ok := payload[jwtClaimPath].(map[string]any); ok {
		if accountID, ok := auth["chatgpt_account_id"].(string); ok {
			return accountID
		}
	}
	return ""
}

func accountIDFromIDToken(value any) string {
	switch v := value.(type) {
	case string:
		return accountIDFromToken(v)
	case map[string]any:
		if accountID, ok := v["chatgpt_account_id"].(string); ok {
			return accountID
		}
	}
	return ""
}

func decodeJWTPayload(token string, dst any) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	return json.Unmarshal(data, dst) == nil
}

func bearerHeaders(apiKey string, stream bool) map[string]string {
	headers := map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Content-Type":  "application/json",
		"User-Agent":    "blitz/0.1",
	}
	if stream {
		headers["Accept"] = "text/event-stream"
	}
	return headers
}

func resolveCodexURL(base string) string {
	raw := strings.TrimRight(base, "/")
	if raw == "" {
		raw = codexBaseURL
	}
	if strings.HasSuffix(raw, "/codex/responses") {
		return raw
	}
	if strings.HasSuffix(raw, "/codex") {
		return raw + "/responses"
	}
	return raw + "/codex/responses"
}

func joinEndpoint(base, fallback, path string) string {
	raw := strings.TrimRight(base, "/")
	if raw == "" {
		raw = fallback
	}
	if strings.HasSuffix(raw, path) {
		return raw
	}
	return raw + path
}

func responseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		msg = resp.Status
	}
	return fmt.Errorf("HTTP %s: %s", resp.Status, msg)
}

func isEventStream(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	return err == nil && mediaType == "text/event-stream"
}

func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func randomID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("blitz-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `blitz - fast transcript enhancement CLI

Usage:
  blitz [flags] "raw transcript text"
  blitz --transcript "raw transcript text"
  cat transcript.txt | blitz [flags]
  blitz login
  blitz auth
  blitz logout

Defaults to Codex subscription auth. Run blitz login or codex login first.
Skill files in ~/.blitz/skills become flags: transcript.md -> --transcript.

Flags:
  -provider codex|responses|chat
  -model gpt-5.4-mini
  -base-url URL
  -prompt TEXT
  -skills-dir DIR
  -stream=true
  -fast=true
  -service-tier priority
  -reasoning low`)
}
