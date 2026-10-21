package agent

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/llm"
)

type credentialProvider struct {
	complete func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error)
}

func (p credentialProvider) Complete(ctx context.Context, r llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return p.complete(ctx, r)
}
func (p credentialProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(y func(llm.StreamEvent, error) bool) { y(llm.StreamEvent{}, errors.New("unexpected stream")) }
}

func TestCredentialJSONSeparationAndLimits(t *testing.T) {
	raw := `{"accounts":[{"username":"alice","password":"fixture-one"},{"username":"bob","password":"fixture-two"},{"token":"fixture-token"}]}`
	got, ok := ParseCredentialJSON(raw)
	if !ok || len(got) != 3 || got[0].Username != "alice" || got[0].Password != "fixture-one" || got[1].Username != "bob" || got[1].Password != "fixture-two" {
		t.Fatal("objects were not preserved")
	}
	for _, bad := range []string{"not JSON", `{"username":"alone"}`, strings.Repeat("x", 32769), strings.Repeat("[", 14) + `{"token":"fixture"}` + strings.Repeat("]", 14)} {
		if _, ok := ParseCredentialJSON(bad); ok {
			t.Fatal("invalid or unbounded JSON accepted")
		}
	}
	many := `[` + strings.Repeat(`{"token":"fixture"},`, 100) + `{"token":"fixture"}]`
	if _, ok := ParseCredentialJSON(many); ok {
		t.Fatal("more than 100 accounts accepted")
	}
}

func TestCredentialGrounding(t *testing.T) {
	raw := `{"username":"a","password":"a\"b","token":"local-token"}`
	a := db.CredentialAccount{Username: "a", Password: "a\"b", Label: strings.Repeat("x", 201)}
	got, err := ValidateCredentialAccounts(raw, []db.CredentialAccount{a, a, {Username: "unpaired"}})
	if err != nil || len(got) != 1 || got[0].Label != "" {
		t.Fatal("grounded decoding/dedup failed", err)
	}
	got, err = ValidateCredentialAccounts("token=encoded%2Bvalue", []db.CredentialAccount{{Token: "encoded+value"}})
	if err != nil || len(got) != 1 {
		t.Fatal("URL decoding failed")
	}
	for _, a := range []db.CredentialAccount{{Token: "fabricated"}, {Token: "local-token", Endpoint: "https://invented.invalid"}, {Token: strings.Repeat("x", 8193)}} {
		if _, err := ValidateCredentialAccounts(raw, []db.CredentialAccount{a}); err == nil {
			t.Fatal("ungrounded account accepted")
		}
	}
	if _, err := ValidateCredentialAccounts(raw, make([]db.CredentialAccount, 101)); err == nil {
		t.Fatal("account limit missing")
	}
	if CredentialSourceText("bad%xx") != "bad%xx" {
		t.Fatal("invalid URL encoding changed source")
	}
}

func TestCredentialExtractBoundedAndGrounded(t *testing.T) {
	calls := 0
	p := credentialProvider{complete: func(ctx context.Context, r llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		calls++
		if len(r.Tools) != 0 || len(r.Messages) != 1 || r.MaxTokens != 6000 || r.Thinking != "disabled" || len(r.System) != 1 || r.System[0] != CredentialProcessorPrompt {
			t.Fatal("unbounded extraction request")
		}
		return llm.Message{Content: []llm.ContentBlock{llm.TextBlock("\u0060\u0060\u0060json\n" + `{"accounts":[{"token":"fixture-token"}]}` + "\n\u0060\u0060\u0060")}}, "end_turn", llm.Usage{InputTokens: 4, OutputTokens: 3}, nil
	}}
	got, usage, err := ExtractCredentials(t.Context(), p, "", "fixture-token", 10000)
	if err != nil || len(got) != 1 || calls != 1 || usage.InputTokens != 4 {
		t.Fatal("single extraction failed", err)
	}
	if _, _, err := ExtractCredentials(t.Context(), nil, "", "raw", 0); err == nil {
		t.Fatal("nil provider accepted")
	}
	if _, _, err := ExtractCredentials(t.Context(), p, "", strings.Repeat("x", 32769), 0); err == nil || calls != 1 {
		t.Fatal("oversized source reached provider")
	}
	for _, response := range []struct {
		text, stop string
		err        error
	}{
		{text: `{"accounts":[{"token":"fabricated"}]}`},
		{text: "{}"}, {text: "not JSON"}, {text: `{"accounts":[]}`, stop: "length"},
		{err: errors.New("provider includes fixture-secret")},
	} {
		p := credentialProvider{complete: func(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
			return llm.Message{Content: []llm.ContentBlock{llm.TextBlock(response.text)}}, response.stop, llm.Usage{}, response.err
		}}
		_, _, err := ExtractCredentials(t.Context(), p, "", "fixture-token", 1)
		if err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal("invalid provider response accepted or leaked")
		}
	}
}

func TestCredentialExtractCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	p := credentialProvider{complete: func(ctx context.Context, _ llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
		cancel()
		<-ctx.Done()
		return llm.Message{}, "", llm.Usage{}, ctx.Err()
	}}
	if _, _, err := ExtractCredentials(ctx, p, "", "fixture", 1); err == nil || ctx.Err() == nil {
		t.Fatal("cancelled extraction succeeded")
	}
}
