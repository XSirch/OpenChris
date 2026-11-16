package httpapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openchris/conselho/internal/pluggy"
)

func TestPluggyWidgetStylesAreAllowedOnlyOnConnectionPages(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		config := Config{Production: true, Password: "widget-policy-test-password", PublicURL: "https://chris.example.test"}
		config.Pluggy = pluggy.Config{
			Enabled: enabled, ClientID: "synthetic-client", ClientSecret: "synthetic-secret",
			WebhookSecret: strings.Repeat("s", 40), PublicURL: config.PublicURL + "/webhooks/pluggy",
		}
		handler := New(nil, config)
		for _, path := range []string{"/dashboard", "/expenses", "/expenses?month=2026-11", "/login", "/api/integrations", "/static/integrations.js", "/dashboard/other"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, request)
			directives := map[string]string{}
			for _, directive := range strings.Split(out.Header().Get("Content-Security-Policy"), ";") {
				name, value, _ := strings.Cut(strings.TrimSpace(directive), " ")
				directives[name] = value
			}
			wantWidgetStyles := enabled && (request.URL.Path == "/dashboard" || request.URL.Path == "/expenses")
			if allowed := strings.Contains(directives["style-src-elem"], "'unsafe-inline'"); allowed != wantWidgetStyles {
				t.Errorf("enabled=%t path=%s: widget style elements allowed=%t want=%t", enabled, path, allowed, wantWidgetStyles)
			}
			if directives["style-src"] != "'self'" || strings.Contains(directives["style-src-attr"], "'unsafe-inline'") {
				t.Errorf("enabled=%t path=%s: style attributes lost their restriction", enabled, path)
			}
			if strings.Contains(directives["script-src"], "'unsafe-inline'") || strings.Contains(directives["script-src"], "'unsafe-eval'") {
				t.Errorf("enabled=%t path=%s: inline scripts or eval were allowed", enabled, path)
			}
			if directives["object-src"] != "'none'" || directives["frame-ancestors"] != "'none'" || directives["base-uri"] != "'none'" || directives["form-action"] != "'self'" {
				t.Errorf("enabled=%t path=%s: unrelated policy restrictions changed", enabled, path)
			}
		}
	}
}
