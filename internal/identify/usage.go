package identify

import (
	"encoding/json"
	"fmt"
)

// Price is a model's list price in USD per token.
type Price struct {
	Input       float64
	CachedInput float64 // 0 means cached input is billed at the Input price
	Output      float64
}

// Usage is the token count of one OpenAI response.
type Usage struct {
	InputTokens  int64
	CachedTokens int64 // the part of InputTokens served from the prompt cache
	OutputTokens int64 // includes reasoning tokens
}

// LookupPrice finds model's standard-tier list price in a LiteLLM
// model_prices_and_context_window.json. It returns nil when the model is not
// listed or has no token prices.
func LookupPrice(prices []byte, model string) (*Price, error) {
	var table map[string]json.RawMessage
	if err := json.Unmarshal(prices, &table); err != nil {
		return nil, fmt.Errorf("model price table: %w", err)
	}
	for _, key := range []string{model, "openai/" + model} {
		raw, ok := table[key]
		if !ok {
			continue
		}
		var entry struct {
			Provider    string   `json:"litellm_provider"`
			Input       *float64 `json:"input_cost_per_token"`
			CachedInput *float64 `json:"cache_read_input_token_cost"`
			Output      *float64 `json:"output_cost_per_token"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			return nil, fmt.Errorf("model price table, %s: %w", key, err)
		}
		if entry.Provider != "openai" || entry.Input == nil || entry.Output == nil {
			continue
		}
		p := &Price{Input: *entry.Input, Output: *entry.Output}
		if entry.CachedInput != nil {
			p.CachedInput = *entry.CachedInput
		}
		return p, nil
	}
	return nil, nil
}

// parseUsage reads the usage block of a raw Responses API reply, or returns
// nil when there is none (failed request, unparseable body).
func parseUsage(raw string) *Usage {
	var parsed struct {
		Usage *struct {
			InputTokens        int64 `json:"input_tokens"`
			OutputTokens       int64 `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil || parsed.Usage == nil {
		return nil
	}
	return &Usage{
		InputTokens:  parsed.Usage.InputTokens,
		CachedTokens: parsed.Usage.InputTokensDetails.CachedTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}
}

// Cost estimates the price of u in USD from the list price.
func (p Price) Cost(u Usage) float64 {
	cached := p.CachedInput
	if cached == 0 {
		cached = p.Input
	}
	uncached := u.InputTokens - u.CachedTokens
	return float64(uncached)*p.Input + float64(u.CachedTokens)*cached + float64(u.OutputTokens)*p.Output
}
