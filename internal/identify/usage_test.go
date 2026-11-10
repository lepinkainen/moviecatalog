package identify

import "testing"

const priceTable = `{
	"sample_spec": {"litellm_provider": "openai"},
	"gpt-x": {"litellm_provider": "openai", "input_cost_per_token": 2e-06,
		"cache_read_input_token_cost": 1e-07, "output_cost_per_token": 1e-05},
	"azure/gpt-x": {"litellm_provider": "azure", "input_cost_per_token": 9},
	"openai/gpt-y": {"litellm_provider": "openai", "input_cost_per_token": 1e-06, "output_cost_per_token": 4e-06},
	"gpt-z": {"litellm_provider": "azure", "input_cost_per_token": 1, "output_cost_per_token": 1}
}`

func TestLookupPrice(t *testing.T) {
	tests := []struct {
		model string
		want  *Price
	}{
		{"gpt-x", &Price{Input: 2e-6, CachedInput: 1e-7, Output: 1e-5}},
		{"gpt-y", &Price{Input: 1e-6, Output: 4e-6}}, // under the openai/ prefix
		{"gpt-z", nil}, // another provider's price
		{"unknown", nil},
		{"sample_spec", nil}, // no prices
	}
	for _, tt := range tests {
		got, err := LookupPrice([]byte(priceTable), tt.model)
		if err != nil {
			t.Fatalf("%s: %v", tt.model, err)
		}
		if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
			t.Errorf("%s: price = %+v, want %+v", tt.model, got, tt.want)
		}
	}
}
