package review

import "testing"

func TestParseFindings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "plain array",
			input: `[{"severity":"high","file":"a.go","line":3,"description":"nil deref","action":"ask-user"}]`,
			want:  1,
		},
		{
			name: "fenced with prose",
			input: "Here is my review:\n```json\n" +
				`[{"severity":"low","file":"b.go","description":"typo"}]` +
				"\n```\nThanks!",
			want: 1,
		},
		{
			name:  "empty array",
			input: "[]",
			want:  0,
		},
		{
			name:  "no array at all",
			input: "No issues found.",
			want:  0,
		},
		{
			name:  "drops entries without description",
			input: `[{"severity":"high","file":"a.go"},{"severity":"low","file":"b.go","description":"real"}]`,
			want:  1,
		},
		{
			name:  "bracket inside string does not break parsing",
			input: `[{"severity":"medium","file":"c.go","description":"index x[0] out of range"}]`,
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := parseFindings(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(findings) != tt.want {
				t.Fatalf("got %d findings, want %d (%+v)", len(findings), tt.want, findings)
			}
		})
	}
}

func TestParseFindingsDefaults(t *testing.T) {
	findings, err := parseFindings(`[{"file":"a.go","description":"x"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	if findings[0].Severity != "medium" {
		t.Errorf("missing severity should default to medium, got %q", findings[0].Severity)
	}
	if findings[0].Action != "ask-user" {
		t.Errorf("missing action should default to ask-user, got %q", findings[0].Action)
	}
}

func TestMeetsThreshold(t *testing.T) {
	tests := []struct {
		sev    string
		failOn string
		want   bool
	}{
		{"critical", "high", true},
		{"high", "high", true},
		{"medium", "high", false},
		{"low", "high", false},
		{"medium", "medium", true},
		{"low", "critical", false},
		{"high", "", true}, // empty failOn defaults to "high"
		{"medium", "", false},
	}
	for _, tt := range tests {
		if got := MeetsThreshold(tt.sev, tt.failOn); got != tt.want {
			t.Errorf("MeetsThreshold(%q, %q) = %v, want %v", tt.sev, tt.failOn, got, tt.want)
		}
	}
}

func TestNewProviderNoKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, err := NewProvider(Options{Provider: "anthropic"}); err == nil {
		t.Fatal("expected ErrNoAPIKey when key is unset")
	}
}

func TestNewProviderCLIRequiresCommand(t *testing.T) {
	if _, err := NewProvider(Options{Provider: "cli"}); err == nil {
		t.Fatal("expected error when cli_command is empty")
	}
	if _, err := NewProvider(Options{Provider: "cli", CLICommand: "claude"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
