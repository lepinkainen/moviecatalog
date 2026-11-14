package identify

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAI calls the OpenAI Responses API.
type OpenAI struct {
	APIKey  string
	Model   string
	BaseURL string // defaults to https://api.openai.com/v1
	HTTP    *http.Client
	Price   *Price // list price of Model, for cost estimates; nil when unknown
}

// Guess is what the LLM read off the covers.
type Guess struct {
	Format     string       `json:"format"`
	Titles     []GuessTitle `json:"titles"`
	Confidence string       `json:"confidence"`
	Notes      string       `json:"notes"`
}

// GuessTitle is one movie or series in a Guess.
type GuessTitle struct {
	MediaType     string `json:"media_type"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Year          *int   `json:"year"`
	// Seasons are the season numbers of a TV release; nil for movies or when the case does not say.
	Seasons []int `json:"seasons"`
	// CompleteSeries is a TV release sold as every season of the series.
	CompleteSeries bool `json:"complete_series"`
}

type object = map[string]any

var guessSchema = object{
	"type": "object", "additionalProperties": false,
	"required": []string{"format", "titles", "confidence", "notes"},
	"properties": object{
		"format": object{"type": "string", "enum": []string{"DVD", "Blu-ray", "UHD"}},
		"titles": object{"type": "array", "items": object{
			"type": "object", "additionalProperties": false,
			"required": []string{"media_type", "title", "original_title", "year", "seasons", "complete_series"},
			"properties": object{
				"media_type":      object{"type": "string", "enum": []string{"movie", "tv"}},
				"title":           object{"type": "string"},
				"original_title":  object{"type": "string"},
				"year":            object{"type": []string{"integer", "null"}},
				"seasons":         object{"type": []string{"array", "null"}, "items": object{"type": "integer"}},
				"complete_series": object{"type": "boolean"},
			},
		}},
		"confidence": object{"type": "string", "enum": []string{"high", "medium", "low"}},
		"notes":      object{"type": "string"},
	},
}

const identifyInstructions = `You identify physical home video releases from photos of the case.
You get the front cover and the back cover of one DVD, Blu-ray or 4K Ultra HD Blu-ray case.
Report:
- format: the disc format of this release. For a combo pack report the best one (UHD > Blu-ray > DVD).
- titles: every movie or TV series contained in this release. A box set lists each movie separately. A TV release is one entry for the series.
  - title: as printed on the cover.
  - original_title: the original-language release title.
  - year: original release year of the movie, or first air year of the series.
  - seasons: for TV, the season numbers this release holds, e.g. [2] or [1, 2, 3]; British "Series 2" is season 2. Null for movies, or when the case does not say which seasons.
  - complete_series: true when a TV release is sold as the complete series; otherwise false.
- confidence: how sure you are of the identification as a whole.
- notes: one short sentence, only when something is uncertain; otherwise empty.`

var pickSchema = object{
	"type": "object", "additionalProperties": false,
	"required": []string{"tmdb_id", "reason"},
	"properties": object{
		"tmdb_id": object{"type": []string{"integer", "null"}},
		"reason":  object{"type": "string"},
	},
}

const pickInstructions = `A movie or TV series was read off a physical disc case. Pick the TMDB search result that is the same work.
Answer with its tmdb_id, or null when none of the candidates is the same work. Give the reason in one short sentence.`

// Identify reads the release off the front and back cover photos (JPEG).
// The second return value is the raw API response, for the audit trail.
func (c *OpenAI) Identify(ctx context.Context, front, back []byte) (*Guess, string, error) {
	content := []object{
		{"type": "input_text", "text": "Front cover, then back cover."},
		imagePart(front),
		imagePart(back),
	}
	var guess Guess
	raw, err := c.respond(ctx, identifyInstructions, content, "identification", guessSchema, &guess)
	if err != nil {
		return nil, raw, err
	}
	return &guess, raw, nil
}

// Pick chooses the candidate that is the same work as title, or nil when none is.
func (c *OpenAI) Pick(ctx context.Context, title GuessTitle, candidates []Candidate) (*int64, string, error) {
	question, err := json.MarshalIndent(object{"from_disc_case": title, "candidates": candidates}, "", "  ")
	if err != nil {
		return nil, "", err
	}
	var answer struct {
		TMDBID *int64 `json:"tmdb_id"`
		Reason string `json:"reason"`
	}
	content := []object{{"type": "input_text", "text": string(question)}}
	raw, err := c.respond(ctx, pickInstructions, content, "pick", pickSchema, &answer)
	if err != nil {
		return nil, raw, err
	}
	return answer.TMDBID, raw, nil
}

func imagePart(jpeg []byte) object {
	return object{
		"type":      "input_image",
		"detail":    "high",
		"image_url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg),
	}
}

// respond makes one structured-output request and decodes the answer into out.
func (c *OpenAI) respond(ctx context.Context, instructions string, content []object, schemaName string, schema object, out any) (string, error) {
	body, err := json.Marshal(object{
		"model":        c.Model,
		"instructions": instructions,
		"input":        []object{{"role": "user", "content": content}},
		"text": object{"format": object{
			"type": "json_schema", "name": schemaName, "strict": true, "schema": schema,
		}},
	})
	if err != nil {
		return "", err
	}
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", transient(fmt.Errorf("openai request: %w", err))
	}
	defer resp.Body.Close()
	rawBytes, err := io.ReadAll(resp.Body)
	raw := string(rawBytes)
	if err != nil {
		return raw, transient(fmt.Errorf("openai response: %w", err))
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("openai: %s: %s", resp.Status, truncate(raw, 300))
		if transientStatus(resp.StatusCode) {
			err = transient(err)
		}
		return raw, err
	}

	var parsed struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rawBytes, &parsed); err != nil {
		return raw, fmt.Errorf("openai response: %w", err)
	}
	for _, item := range parsed.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" {
				if err := json.Unmarshal([]byte(part.Text), out); err != nil {
					return raw, fmt.Errorf("openai answer is not the expected JSON: %w", err)
				}
				return raw, nil
			}
		}
	}
	return raw, fmt.Errorf("openai: no answer in response (status %q)", parsed.Status)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
