package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const publicRequest = `{"run_id":"00000000-0000-4000-8000-000000000002","topic":"brazil_macro","as_of":"2026-11-07T12:00:00Z"}`
const publicOutput = `{"summary":"Dados públicos para pesquisa.","sources":[{"url":"https://www.bcb.gov.br/publicacoes","title":"Publicação de teste","summary":"Fonte descoberta, aguardando verificação.","published_at":null}],"unknowns":["Conteúdo ainda não verificado pelo coletor."]}`

func TestResearchExposesOnlyWebDirectlyAndKeepsCodeHostDisabled(t *testing.T) {
	public := researchCLIArgs("schema.json", "result.json")
	private := cliArgs("schema.json", "result.json")
	want := `features.code_mode={enabled=false,direct_only_tool_namespaces=["web"]}`
	if !slices.Contains(public, want) || slices.Contains(public, "features.code_mode=false") {
		t.Fatalf("research does not expose web directly in code-mode-only models: %v", public)
	}
	if !slices.Contains(private, "features.code_mode=false") || slices.Contains(private, want) {
		t.Fatal("public search configuration reached private analysis")
	}
	for _, flag := range private {
		if strings.HasPrefix(flag, "features.") && flag != "features.code_mode=false" && !slices.Contains(public, flag) {
			t.Fatalf("public research relaxed feature restriction %s", flag)
		}
	}
	if !slices.Contains(public, "features.code_mode_host=false") || slices.Contains(public, "features.standalone_web_search=true") {
		t.Fatal("research changed the code host or standalone search feature")
	}
}

func researchRunner(t *testing.T) *Runner {
	t.Helper()
	r := testRunner(t, true)
	r.cfg.prefixArgs = []string{"-test.run=TestResearchCLIHelperProcess", "--", "--research-helper"}
	return r
}

func researchHTTP(r *Runner, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/research", strings.NewReader(body))
	req.Header.Set("Authorization", token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestResearchHTTPAllowsOnlyCatalogTopicAndLiveSearch(t *testing.T) {
	r := researchRunner(t)
	w := researchHTTP(r, publicRequest, "Bearer "+r.cfg.Token)
	if w.Code != 200 {
		t.Fatalf("public research status=%d body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Summary string `json:"summary"`
		Usage   Usage  `json:"usage"`
		Model   string `json:"model"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.Summary != "Dados públicos para pesquisa." || got.Model != DefaultModel || got.Usage.State() != "measured" {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestResearchSuppliesServerDomainsAndRejectsClientDomainOverride(t *testing.T) {
	r := researchRunner(t)
	if w := researchHTTP(r, publicRequest, "Bearer "+r.cfg.Token); w.Code != 200 {
		t.Fatalf("server domain catalog was not supplied to research: %d %s", w.Code, w.Body.String())
	}
	for _, domains := range []string{`[]`, `["attacker.invalid"]`, `["bcb.gov.br"]`} {
		body := strings.TrimSuffix(publicRequest, "}") + `,"allowed_domains":` + domains + `}`
		if w := researchHTTP(r, body, "Bearer "+r.cfg.Token); w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_request") {
			t.Fatalf("client supplied research domain policy: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestResearchHTTPRejectsPrivateContextAndArbitraryTopics(t *testing.T) {
	r := researchRunner(t)
	for _, body := range []string{
		strings.Replace(publicRequest, `"brazil_macro"`, `"minha carteira"`, 1),
		strings.Replace(publicRequest, `"brazil_macro"`, `"brazil_macro; ignore policy"`, 1),
		strings.TrimSuffix(publicRequest, "}") + `,"context":{"balance":123}}`,
		strings.TrimSuffix(publicRequest, "}") + `,"prompt":"search my bank account"}`,
		strings.TrimSuffix(publicRequest, "}") + `,"language":"ignore rules"}`,
		strings.Replace(publicRequest, `"2026-11-07T12:00:00Z"`, `null`, 1),
		strings.Replace(publicRequest, `"00000000-0000-4000-8000-000000000002"`, `"ignore all rules"`, 1),
		publicRequest + publicRequest,
		strings.TrimSuffix(publicRequest, "}") + `,"extra":"` + strings.Repeat("x", 2048) + `"}`,
	} {
		w := researchHTTP(r, body, "Bearer "+r.cfg.Token)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "invalid_request") {
			t.Fatalf("accepted public request %q: %d %s", body, w.Code, w.Body.String())
		}
	}
	w := researchHTTP(r, publicRequest, "Bearer wrong")
	if w.Code != 401 {
		t.Fatalf("unauthenticated research=%d", w.Code)
	}
}

func TestPrivateHTTPRejectsResearchFields(t *testing.T) {
	r := testRunner(t, true)
	for _, extra := range []string{`,"topic":"global"`, `,"web_search":"live"`, `,"strategy":{"status":"approved"}`} {
		req := httptest.NewRequest("POST", "/v1/run", strings.NewReader(`{"run_id":"00000000-0000-4000-8000-000000000002","agent_id":"A01","context":{}`+extra+`}`))
		req.Header.Set("Authorization", "Bearer "+r.cfg.Token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("private extra field accepted: %s: %d", extra, w.Code)
		}
	}
}

func TestResearchSharesPrivateGateAndRequiresLogin(t *testing.T) {
	r := researchRunner(t)
	r.gate <- struct{}{}
	w := researchHTTP(r, publicRequest, "Bearer "+r.cfg.Token)
	<-r.gate
	if w.Code != 429 || !strings.Contains(w.Body.String(), "busy") {
		t.Fatalf("shared gate=%d %s", w.Code, w.Body.String())
	}
	r = testRunner(t, false)
	w = researchHTTP(r, publicRequest, "Bearer "+r.cfg.Token)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "not_authenticated") {
		t.Fatalf("login required=%d %s", w.Code, w.Body.String())
	}
}

func TestResearchRejectsUnexpectedToolAndTimeoutIsUnknown(t *testing.T) {
	for _, tc := range []struct {
		topic, code string
		timeout     bool
	}{
		{"fixed_income", "policy_violation", false}, {"funds", "failed_unknown", true},
		{"long_term", "invalid_output", false}, {"global", "invalid_output", false}, {"crypto", "invalid_output", false},
	} {
		t.Run(tc.topic, func(t *testing.T) {
			r := researchRunner(t)
			if tc.timeout {
				r.cfg.Timeout = 50 * time.Millisecond
			}
			w := researchHTTP(r, strings.Replace(publicRequest, "brazil_macro", tc.topic, 1), "Bearer "+r.cfg.Token)
			if w.Code != 503 || !strings.Contains(w.Body.String(), tc.code) {
				t.Fatalf("result=%d %s want=%s", w.Code, w.Body.String(), tc.code)
			}
			if tc.code == "policy_violation" && r.Status(context.Background()).State != "policy_violation" {
				t.Fatal("tool incident was not blocked")
			}
		})
	}
}

func TestStrategyDraftRejectsAuthorityAndUnknownEvidence(t *testing.T) {
	request := Request{RunID: "00000000-0000-4000-8000-000000000002", AgentID: "A01", Context: json.RawMessage(`{"sources":[{"id":"evidence:1"}]}`)}
	base := `{"reply":"Estudo de teste.","episode":"Pesquisa.","evidence":["evidence:1"],"assumptions":[],"unknowns":[]`
	draft := `{"title":"Investigar renda fixa","hypothesis":"Estudar retornos reais.","mandate":"long_term","time_horizon":"Cinco anos","conditions":[],"invalidation_conditions":[],"eligible_instruments":[],"risks":["Inflação"],"evidence":["evidence:1"]}`
	if _, err := validateGenerated([]byte(base+`,"strategy":`+draft+`}`), request); err != nil {
		t.Fatalf("valid strategy rejected: %v", err)
	}
	if _, err := validateGenerated([]byte(base+`}`), request); err != nil {
		t.Fatalf("legacy output rejected: %v", err)
	}
	for _, bad := range []string{
		strings.Replace(draft, `"long_term"`, `"approved"`, 1),
		strings.Replace(draft, `"evidence:1"`, `"not-observed"`, 1),
		strings.Replace(draft, `"Investigar renda fixa"`, `"`+strings.Repeat("x", 161)+`"`, 1),
		strings.Replace(draft, `"Cinco anos"`, `"\u0000"`, 1),
		strings.TrimSuffix(draft, "}") + `,"status":"eligible_with_conditions"}`,
		strings.TrimSuffix(draft, "}") + `,"version":9}`,
		strings.TrimSuffix(draft, "}") + `,"author":"owner"}`,
	} {
		if _, err := validateGenerated([]byte(base+`,"strategy":`+bad+`}`), request); err == nil {
			t.Fatalf("accepted invalid draft %s", bad)
		}
	}
}

// Isolated executable fixture. It checks the actual CLI policy and stdin without contacting a provider.
func TestResearchCLIHelperProcess(t *testing.T) {
	idx := slices.Index(os.Args, "--research-helper")
	if idx < 0 {
		return
	}
	args := os.Args[idx+1:]
	for _, v := range []string{"read-only", "--ignore-user-config", "--ephemeral", `web_search="live"`, "features.shell_tool=false", "features.view_image=false", "features.apps=false", "features.plugins=false", "features.multi_agent=false", "features.browser_use=false", "features.computer_use=false", `features.code_mode={enabled=false,direct_only_tool_namespaces=["web"]}`, "features.code_mode_host=false"} {
		if !slices.Contains(args, v) {
			fmt.Fprintln(os.Stderr, "missing policy", v)
			os.Exit(9)
		}
	}
	if slices.Contains(args, `web_search="disabled"`) {
		os.Exit(10)
	}
	expected := `tools.web_search.allowed_domains=["bcb.gov.br","gov.br","conteudo.cvm.gov.br","ibge.gov.br","agenciadenoticias.ibge.gov.br","agenciabrasil.ebc.com.br","tesourodireto.com.br","b3.com.br","federalreserve.gov","ecb.europa.eu"]`
	if !slices.Contains(args, expected) {
		fmt.Fprintln(os.Stderr, "missing domain allowlist")
		os.Exit(11)
	}
	for _, name := range []string{"DATABASE_URL", "PLUGGY_CLIENT_SECRET", "CODEX_RUNNER_TOKEN", "CODEX_AUTH_KEY"} {
		if os.Getenv(name) != "" {
			os.Exit(12)
		}
	}
	raw, _ := io.ReadAll(os.Stdin)
	var in struct {
		Policy         string    `json:"policy"`
		Topic          string    `json:"topic"`
		AsOf           time.Time `json:"as_of"`
		AllowedDomains []string  `json:"allowed_domains"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || in.Topic == "" || in.AsOf.IsZero() || in.Policy == "" {
		os.Exit(13)
	}
	if !slices.Equal(in.AllowedDomains, ResearchDomains()) || !strings.Contains(in.Policy, "ao menos uma consulta web") {
		fmt.Fprintln(os.Stderr, "missing server domain catalog or required search instruction")
		os.Exit(16)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")); err != nil {
		os.Exit(14)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "auth.json"), []byte(`{"tokens":{"access_token":"refreshed-synthetic"}}`), 0600); err != nil {
		os.Exit(15)
	}
	result := publicOutput
	switch in.Topic {
	case "fixed_income":
		fmt.Println(`{"type":"item.started","item":{"type":"command_execution"}}`)
		os.Exit(0)
	case "funds":
		time.Sleep(10 * time.Second)
	case "long_term":
		result = strings.Replace(publicOutput, "https://www.bcb.gov.br/publicacoes", "http://127.0.0.1/secret", 1)
	case "global":
		result = strings.Replace(publicOutput, "Dados públicos para pesquisa.", strings.Repeat("x", MaxResult), 1)
	case "crypto":
		result = strings.TrimSuffix(publicOutput, "}") + `,"context":"private"}`
	}
	if i := slices.Index(args, "-o"); i >= 0 {
		os.WriteFile(args[i+1], []byte(result), 0600)
	}
	fmt.Println(`{"type":"item.completed","item":{"type":"web_search","action":{"type":"search","query":"indicadores públicos"}}}`)
	fmt.Println(`{"type":"turn.completed","usage":{"input_tokens":11,"output_tokens":7}}`)
	os.Exit(0)
}

func TestResearchEndpointDoesNotExposeClientTokenOnRedirect(t *testing.T) {
	called := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer server.Close()
	client, _ := NewClient(server.URL, strings.Repeat("t", 40))
	var out map[string]any
	err := client.call(context.Background(), "POST", "/v1/research", json.RawMessage(publicRequest), &out)
	if err == nil || called {
		t.Fatal("research followed service redirect")
	}
}

func TestResearchStrictJSONRejectsDuplicateFields(t *testing.T) {
	r := researchRunner(t)
	duplicate := strings.TrimSuffix(publicRequest, "}") + `,"topic":"brazil_macro"}`
	if w := researchHTTP(r, duplicate, "Bearer "+r.cfg.Token); w.Code != 400 {
		t.Fatalf("duplicate field accepted: %d %s", w.Code, w.Body.String())
	}
	var request ResearchRequest
	json.Unmarshal([]byte(publicRequest), &request)
	duplicateOutput := strings.TrimSuffix(publicOutput, "}") + `,"summary":"Second answer"}`
	if _, err := validateResearch([]byte(duplicateOutput), request); Code(err) != "invalid_output" {
		t.Fatalf("duplicate output accepted: %v", err)
	}
}

func TestResearchResponseProvesObservedWebSearchWithoutRawToolEvents(t *testing.T) {
	r := researchRunner(t)
	w := researchHTTP(r, publicRequest, "Bearer "+r.cfg.Token)
	var result map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("research failed: %d", w.Code)
	}
	if result["web_search_used"] != true {
		t.Fatal("research response does not distinguish observed web search")
	}
	if strings.Contains(w.Body.String(), "indicadores públicos") {
		t.Fatal("raw search event leaked")
	}
}

func TestResearchClientUsesAuthenticatedEndpointAndValidatesResult(t *testing.T) {
	var request ResearchRequest
	json.Unmarshal([]byte(publicRequest), &request)
	for _, tc := range []struct{ name, body, code string }{
		{"valid", strings.TrimSuffix(publicOutput, "}") + `,"usage":{"input_tokens":2,"output_tokens":3,"cost_estimate_usd":9},"model":"gpt-6.1-sol"}`, ""},
		{"negative_usage", strings.TrimSuffix(publicOutput, "}") + `,"usage":{"input_tokens":-2},"model":"gpt-6.1-sol"}`, "invalid_output"},
		{"missing_model", publicOutput, "invalid_output"},
		{"unknown_field", strings.TrimSuffix(publicOutput, "}") + `,"usage":{},"model":"gpt-6.1-sol","strategy":{}}`, "invalid_output"},
		{"large", strings.Repeat("x", MaxResult+2049), "invalid_output"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.URL.Path != "/v1/research" || r.Method != "POST" || !authorized(r.Header.Get("Authorization"), strings.Repeat("t", 40)) {
					t.Error("wrong authenticated research route")
				}
				var got ResearchRequest
				if json.NewDecoder(r.Body).Decode(&got) != nil || got.Topic != request.Topic || got.RunID != request.RunID || !got.AsOf.Equal(request.AsOf) {
					t.Error("wrong research request")
				}
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			client, _ := NewClient(server.URL, strings.Repeat("t", 40))
			result, err := client.Research(context.Background(), request)
			if Code(err) != tc.code || !called {
				t.Fatalf("result=%+v code=%s want=%s called=%v", result, Code(err), tc.code, called)
			}
			if tc.code == "" && result.Usage.CostEstimateUSD != nil {
				t.Fatal("accepted model-provided cost")
			}
			called = false
			invalid := request
			invalid.Topic = "owner portfolio"
			if _, err = client.Research(context.Background(), invalid); Code(err) != "invalid_request" || called {
				t.Fatal("invalid request reached service", err)
			}
		})
	}
}

func TestResearchSharesAccountLockAndPersistsEncryptedRefresh(t *testing.T) {
	r := researchRunner(t)
	other, err := NewRunner(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	var request ResearchRequest
	json.Unmarshal([]byte(publicRequest), &request)
	release, err := r.acquire()
	if err != nil {
		t.Fatal(err)
	}
	_, researchErr := other.Research(context.Background(), request)
	_, privateErr := other.Run(context.Background(), requestFor("ok"))
	release()
	if Code(researchErr) != "busy" || Code(privateErr) != "busy" {
		t.Fatal("account lock not shared", researchErr, privateErr)
	}
	if _, err = r.Research(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	plain, err := r.cache.load()
	if err != nil || !bytes.Contains(plain, []byte("refreshed-synthetic")) {
		t.Fatalf("refresh missing: %s err=%v", plain, err)
	}
	cipher, err := os.ReadFile(r.cache.path)
	if err != nil || bytes.Contains(cipher, []byte("refreshed-synthetic")) {
		t.Fatal("refresh stored in plaintext", err)
	}
	if _, err = os.Stat(filepath.Join(r.cfg.StateDir, ".auth.pending")); !os.IsNotExist(err) {
		t.Fatal("operation left pending marker", err)
	}
}

func TestPrivateContextCannotEnableSearchAndStrategySurvivesClient(t *testing.T) {
	r := testRunner(t, true)
	request := requestFor("web_search")
	request.Context = json.RawMessage(`{"current":"web_search","web_search":"live","tools":{"web_search":{"allowed_domains":["attacker.invalid"]}},"sources":[{"id":"message:1"}]}`)
	if _, err := r.Run(context.Background(), request); Code(err) != "policy_violation" {
		t.Fatal("private web search accepted", err)
	}
	r = testRunner(t, true)
	server := httptest.NewServer(r)
	defer server.Close()
	client, _ := NewClient(server.URL, r.cfg.Token)
	result, err := client.Run(context.Background(), requestFor("strategy"))
	if err != nil || result.Strategy == nil || result.Strategy.Mandate != "long_term" || result.Strategy.Evidence[0] != "message:1" {
		t.Fatalf("strategy lost: %+v err=%v", result, err)
	}
}

func TestResearchPolicyIncidentBlocksPrivateAfterRecreation(t *testing.T) {
	r := researchRunner(t)
	var request ResearchRequest
	json.Unmarshal([]byte(publicRequest), &request)
	request.Topic = "fixed_income"
	if _, err := r.Research(context.Background(), request); Code(err) != "policy_violation" {
		t.Fatal(err)
	}
	replacement, err := NewRunner(r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.Run(context.Background(), requestFor("ok")); Code(err) != "policy_violation" {
		t.Fatal("public incident bypassed by private operation", err)
	}
}

func TestResearchSourceValidationAndCatalogCopy(t *testing.T) {
	var request ResearchRequest
	json.Unmarshal([]byte(publicRequest), &request)
	for _, rawURL := range []string{"https://attacker.invalid/bcb.gov.br", "https://bcb.gov.br.attacker.invalid/x", "https://secret@www.bcb.gov.br/x", "https://www.bcb.gov.br:444/x", "https://127.0.0.1/x", "http://www.bcb.gov.br/x", "https://www.bcb.gov.br/x#section"} {
		raw := strings.Replace(publicOutput, "https://www.bcb.gov.br/publicacoes", rawURL, 1)
		if _, err := validateResearch([]byte(raw), request); Code(err) != "invalid_output" {
			t.Fatalf("accepted URL %q", rawURL)
		}
	}
	for _, raw := range []string{
		strings.Replace(publicOutput, `"published_at":null`, `"published_at":"2026-11-08T12:00:00Z"`, 1),
		strings.Replace(publicOutput, `"unknowns":["Conteúdo ainda não verificado pelo coletor."]`, `"unknowns":null`, 1),
		strings.Replace(publicOutput, `"summary":"Dados públicos para pesquisa."`, `"summary":"\u0000"`, 1),
	} {
		if _, err := validateResearch([]byte(raw), request); Code(err) != "invalid_output" {
			t.Fatalf("accepted output %s", raw)
		}
	}
	domains := ResearchDomains()
	domains[0] = "attacker.invalid"
	if ResearchDomains()[0] != "bcb.gov.br" {
		t.Fatal("caller mutated shared domain policy")
	}
}

func TestResearchOutputLimitIncludesJSONEncoding(t *testing.T) {
	var request ResearchRequest
	json.Unmarshal([]byte(publicRequest), &request)
	raw := strings.Replace(publicOutput, "Dados públicos para pesquisa.", strings.Repeat("&", 3500), 1)
	if len(raw) >= MaxResult {
		t.Fatal("fixture already exceeds raw limit")
	}
	if _, err := validateResearch([]byte(raw), request); Code(err) != "invalid_output" {
		t.Fatal("encoding expanded public output beyond limit", err)
	}
}
