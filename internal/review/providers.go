package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// httpClient is shared by the cloud providers. Reviews can take a while, so the
// timeout is generous; the caller's context still governs cancellation.
var httpClient = &http.Client{Timeout: 5 * time.Minute}

// postJSON marshals body, POSTs it to url with the given headers, and returns
// the raw response body. Non-2xx responses are turned into errors.
func postJSON(ctx context.Context, url string, headers map[string]string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return respBody, nil
}

// ── Anthropic ─────────────────────────────────────────────────────────────────

type anthropicProvider struct {
	opts   Options
	apiKey string
	model  string
}

func (p *anthropicProvider) Name() string { return "anthropic" }

func (p *anthropicProvider) Review(ctx context.Context, req Request) ([]Finding, error) {
	baseURL := p.opts.BaseURL
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}

	payload := map[string]any{
		"model":      p.model,
		"max_tokens": p.opts.MaxTokens,
		"system":     p.opts.SystemPrompt,
		"messages": []map[string]any{
			{"role": "user", "content": buildUserMessage(req)},
		},
	}
	headers := map[string]string{
		"x-api-key":         p.apiKey,
		"anthropic-version": "2023-06-01",
	}

	body, err := postJSON(ctx, baseURL+"/v1/messages", headers, payload)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("anthropic response decode: %w", err)
	}

	var text strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	return parseFindings(text.String())
}

// ── OpenAI ───────────────────────────────────────────────────────────────────

type openAIProvider struct {
	opts   Options
	apiKey string
	model  string
}

func (p *openAIProvider) Name() string { return "openai" }

func (p *openAIProvider) Review(ctx context.Context, req Request) ([]Finding, error) {
	baseURL := p.opts.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}

	payload := map[string]any{
		"model":      p.model,
		"max_tokens": p.opts.MaxTokens,
		"messages": []map[string]any{
			{"role": "system", "content": p.opts.SystemPrompt},
			{"role": "user", "content": buildUserMessage(req)},
		},
	}
	headers := map[string]string{
		"Authorization": "Bearer " + p.apiKey,
	}

	body, err := postJSON(ctx, baseURL+"/v1/chat/completions", headers, payload)
	if err != nil {
		return nil, fmt.Errorf("openai request failed: %w", err)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("openai response decode: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, nil
	}
	return parseFindings(out.Choices[0].Message.Content)
}

// ── Ollama (local) ───────────────────────────────────────────────────────────

type ollamaProvider struct {
	opts  Options
	model string
}

func (p *ollamaProvider) Name() string { return "ollama" }

func (p *ollamaProvider) Review(ctx context.Context, req Request) ([]Finding, error) {
	baseURL := p.opts.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	payload := map[string]any{
		"model":  p.model,
		"stream": false,
		"messages": []map[string]any{
			{"role": "system", "content": p.opts.SystemPrompt},
			{"role": "user", "content": buildUserMessage(req)},
		},
	}

	body, err := postJSON(ctx, baseURL+"/api/chat", nil, payload)
	if err != nil {
		return nil, fmt.Errorf("ollama request failed: %w", err)
	}

	var out struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("ollama response decode: %w", err)
	}
	return parseFindings(out.Message.Content)
}

// ── Local CLI agent ──────────────────────────────────────────────────────────

// cliProvider shells out to an arbitrary command (e.g. "claude", "aider"),
// feeding the prompt on stdin and parsing findings from stdout.
type cliProvider struct {
	opts Options
}

func (p *cliProvider) Name() string { return "cli:" + fields(p.opts.CLICommand)[0] }

func (p *cliProvider) Review(ctx context.Context, req Request) ([]Finding, error) {
	parts := fields(p.opts.CLICommand)
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)

	prompt := p.opts.SystemPrompt + "\n\n" + buildUserMessage(req)
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cli agent %q failed: %w: %s", p.opts.CLICommand, err, strings.TrimSpace(stderr.String()))
	}
	return parseFindings(stdout.String())
}

// fields splits a command string on whitespace. It always returns at least one
// element (callers guarantee a non-empty command).
func fields(s string) []string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return []string{s}
	}
	return f
}
