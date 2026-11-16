package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxContext = 32 << 10
const MaxResult = 12 << 10
const MaxStream = 1 << 20
const DefaultModel = "gpt-6.1-sol"

type Request struct {
	RunID   string          `json:"run_id"`
	AgentID string          `json:"agent_id"`
	Context json.RawMessage `json:"context"`
}
type Usage struct {
	InputTokens     *int64   `json:"input_tokens"`
	OutputTokens    *int64   `json:"output_tokens"`
	CostEstimateUSD *float64 `json:"cost_estimate_usd"`
}

func (u Usage) State() string {
	if u.InputTokens == nil && u.OutputTokens == nil {
		return "unavailable"
	}
	if u.InputTokens == nil || u.OutputTokens == nil {
		return "partial"
	}
	return "measured"
}

type Result struct {
	Reply       string         `json:"reply"`
	Episode     string         `json:"episode"`
	Evidence    []string       `json:"evidence"`
	Assumptions []string       `json:"assumptions"`
	Unknowns    []string       `json:"unknowns"`
	Strategy    *StrategyDraft `json:"strategy,omitempty"`
	Usage       Usage          `json:"usage"`
	Model       string         `json:"model"`
}
type generated struct {
	Reply       string         `json:"reply"`
	Episode     string         `json:"episode"`
	Evidence    []string       `json:"evidence"`
	Assumptions []string       `json:"assumptions"`
	Unknowns    []string       `json:"unknowns"`
	Strategy    *StrategyDraft `json:"strategy,omitempty"`
}

// StrategyDraft carries research content. The server owns identity, status and version.
type StrategyDraft struct {
	Title                  string   `json:"title"`
	Hypothesis             string   `json:"hypothesis"`
	Mandate                string   `json:"mandate"`
	TimeHorizon            string   `json:"time_horizon"`
	Conditions             []string `json:"conditions"`
	InvalidationConditions []string `json:"invalidation_conditions"`
	EligibleInstruments    []string `json:"eligible_instruments"`
	Risks                  []string `json:"risks"`
	Evidence               []string `json:"evidence"`
}
type Status struct {
	State string `json:"state"`
	Model string `json:"model"`
}
type Error struct {
	Kind      string `json:"code"`
	Transient bool   `json:"transient"`
}

func (e *Error) Error() string { return "codex: " + e.Kind }
func Code(err error) string {
	if err == nil {
		return ""
	}
	var failure *Error
	if errors.As(err, &failure) {
		return failure.Kind
	}
	return "unavailable"
}
func failure(kind string) error { return &Error{Kind: kind} }

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var agent = regexp.MustCompile(`^A(0[1-9]|1[0-9]|2[0-3])$`)

func (r Request) Validate() error {
	if !uuid.MatchString(r.RunID) || !agent.MatchString(r.AgentID) || r.AgentID == "A02" || r.AgentID == "A04" || len(r.Context) > MaxContext || !utf8.Valid(r.Context) {
		return failure("invalid_request")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(r.Context, &object) != nil || object == nil {
		return failure("invalid_request")
	}
	return nil
}
func strictJSON(raw []byte, out any) error {
	if !utf8.Valid(raw) {
		return failure("invalid_output")
	}
	if err := uniqueJSONValue(json.NewDecoder(bytes.NewReader(raw))); err != nil {
		return failure("invalid_output")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return failure("invalid_output")
	}
	return nil
}

// Reject duplicate members before decoding into a struct can silently overwrite them.
func uniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			member, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := member.(string)
			if !ok || seen[name] {
				return failure("invalid_output")
			}
			seen[name] = true
			if err = uniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err = uniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return failure("invalid_output")
	}
	_, err = decoder.Token()
	return err
}
func validateGenerated(raw []byte, request Request) (generated, error) {
	var out generated
	if len(raw) > MaxResult || strictJSON(raw, &out) != nil || strings.TrimSpace(out.Reply) == "" || strings.TrimSpace(out.Episode) == "" || out.Evidence == nil || out.Assumptions == nil || out.Unknowns == nil || strings.ContainsRune(out.Reply+out.Episode, 0) {
		return out, failure("invalid_output")
	}
	var ctx struct {
		Sources []struct {
			ID string `json:"id"`
		} `json:"sources"`
	}
	if json.Unmarshal(request.Context, &ctx) != nil {
		return out, failure("invalid_request")
	}
	known := map[string]bool{}
	for _, source := range ctx.Sources {
		known[source.ID] = true
	}
	for _, id := range out.Evidence {
		if !known[id] {
			return out, failure("invalid_evidence")
		}
	}
	if len(out.Evidence) > 32 || len(out.Assumptions) > 12 || len(out.Unknowns) > 12 {
		return out, failure("invalid_output")
	}
	if out.Strategy != nil {
		if err := out.Strategy.validate(known); err != nil {
			return out, err
		}
	}
	return out, nil
}

func validText(value string, limit int) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= limit && !strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' })
}

func validTextList(values []string, count, length int) bool {
	if values == nil || len(values) > count {
		return false
	}
	for _, value := range values {
		if !validText(value, length) {
			return false
		}
	}
	return true
}

func (s StrategyDraft) validate(known map[string]bool) error {
	if !validText(s.Title, 160) || !validText(s.Hypothesis, 2000) || !validText(s.TimeHorizon, 240) || !validTextList(s.Conditions, 12, 600) || !validTextList(s.InvalidationConditions, 12, 600) || !validTextList(s.EligibleInstruments, 12, 600) || !validTextList(s.Risks, 12, 600) || !validTextList(s.Evidence, 32, 160) {
		return failure("invalid_output")
	}
	switch s.Mandate {
	case "long_term", "tactical", "cross_mandate", "not_applicable":
	default:
		return failure("invalid_output")
	}
	for _, id := range s.Evidence {
		if !known[id] {
			return failure("invalid_evidence")
		}
	}
	return nil
}

const strategySchema = `{"anyOf":[{"type":"null"},{"type":"object","properties":{"title":{"type":"string","minLength":1,"maxLength":160},"hypothesis":{"type":"string","minLength":1,"maxLength":2000},"mandate":{"type":"string","enum":["long_term","tactical","cross_mandate","not_applicable"]},"time_horizon":{"type":"string","minLength":1,"maxLength":240},"conditions":{"type":"array","maxItems":12,"items":{"type":"string","minLength":1,"maxLength":600}},"invalidation_conditions":{"type":"array","maxItems":12,"items":{"type":"string","minLength":1,"maxLength":600}},"eligible_instruments":{"type":"array","maxItems":12,"items":{"type":"string","minLength":1,"maxLength":600}},"risks":{"type":"array","maxItems":12,"items":{"type":"string","minLength":1,"maxLength":600}},"evidence":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1,"maxLength":160}}},"required":["title","hypothesis","mandate","time_horizon","conditions","invalidation_conditions","eligible_instruments","risks","evidence"],"additionalProperties":false}]}`

const outputSchema = `{"type":"object","properties":{"reply":{"type":"string"},"episode":{"type":"string"},"evidence":{"type":"array","items":{"type":"string"}},"assumptions":{"type":"array","items":{"type":"string"}},"unknowns":{"type":"array","items":{"type":"string"}},"strategy":` + strategySchema + `},"required":["reply","episode","evidence","assumptions","unknowns","strategy"],"additionalProperties":false}`
