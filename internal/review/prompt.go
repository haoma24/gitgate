package review

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const defaultSystemPrompt = `You are a meticulous senior software engineer performing a pre-push code review.
You are given a unified git diff and (optionally) the author's stated intent.
Report only concrete, actionable problems in the CHANGED lines: bugs, security
issues, race conditions, resource leaks, incorrect error handling, and clear
correctness regressions. Do NOT report style nitpicks, opinions, or issues in
unchanged context lines.`

// reviewInstructions is appended to the user message to force machine-readable
// output regardless of provider.
const reviewInstructions = `
Respond with ONLY a JSON array (no prose, no markdown fences) of findings.
Each finding is an object with these fields:
  "severity":    one of "critical", "high", "medium", "low"
  "file":        path of the file, relative to the repo root
  "line":        integer line number in the new file (0 if not applicable)
  "description": one or two sentences explaining the problem and the fix
  "action":      "auto-fix" if a safe automatic fix is obvious, else "ask-user"
If there are no problems, respond with an empty array: []`

// buildUserMessage assembles the user-facing prompt from a request.
func buildUserMessage(req Request) string {
	var b strings.Builder
	if strings.TrimSpace(req.Intent) != "" {
		fmt.Fprintf(&b, "Author intent: %s\n\n", strings.TrimSpace(req.Intent))
	}
	b.WriteString("Unified diff to review:\n\n")
	b.WriteString("```diff\n")
	b.WriteString(req.Diff)
	if !strings.HasSuffix(req.Diff, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")
	b.WriteString(reviewInstructions)
	return b.String()
}

// parseFindings extracts a JSON array of findings from a model's free-form text
// response. It tolerates surrounding prose and ```json fences by locating the
// outermost bracket pair.
func parseFindings(text string) ([]Finding, error) {
	raw := extractJSONArray(text)
	if raw == "" {
		// A model that correctly finds nothing may reply with prose like
		// "No issues found." Treat the absence of an array as zero findings.
		return nil, nil
	}

	var findings []Finding
	if err := json.Unmarshal([]byte(raw), &findings); err != nil {
		return nil, fmt.Errorf("could not parse findings JSON: %w", err)
	}

	// Normalise + drop obviously empty entries.
	out := findings[:0]
	for _, f := range findings {
		if strings.TrimSpace(f.Description) == "" {
			continue
		}
		if f.Severity == "" {
			f.Severity = "medium"
		}
		if f.Action == "" {
			f.Action = "ask-user"
		}
		out = append(out, f)
	}
	return out, nil
}

// extractJSONArray returns the substring from the first '[' to its matching ']',
// respecting strings so brackets inside string literals don't confuse it.
func extractJSONArray(text string) string {
	start := strings.IndexByte(text, '[')
	if start < 0 {
		return ""
	}

	depth := 0
	inStr := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inStr {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

// firstEnv returns the first non-empty environment variable among names.
func firstEnv(names ...string) string {
	for _, n := range names {
		if v := strings.TrimSpace(os.Getenv(n)); v != "" {
			return v
		}
	}
	return ""
}
