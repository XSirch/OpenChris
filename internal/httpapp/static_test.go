package httpapp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProductionStaticFilesRejectEscapedTraversalAndDirectoryListings(t *testing.T) {
	handler := New(nil, Config{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test"})
	for _, path := range []string{
		"/static/office/%2e%2e/referencia_dashboard_opcao_1_refinada.png",
		"/static/office/%2E%2E/referencia_dashboard_opcao_1_refinada.png",
		"/static/office/.%2e/referencia_dashboard_opcao_1_refinada.png",
		"/static/office/%2e./referencia_dashboard_opcao_1_refinada.png",
		"/static/office/%2e%2e%2freferencia_dashboard_opcao_1_refinada.png",
		"/static/office/%252e%252e/referencia_dashboard_opcao_1_refinada.png",
		"/static/office/../referencia_dashboard_opcao_1_refinada.png",
		"/static/office/%2e%2e/conselho-office-v2.webp",
		"/static/fonts/%2e%2e/dashboard.html",
		"/static/fonts/%2e%2e/benchmark.html",
		"/static/fonts/%2e%2e/dashboard.html/",
		"/static/fonts/%2e%2e/",
		"/static/office/%2e%2e/",
		"/static/office/",
		"/static/fonts/",
		"/static/",
	} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, request)
			if out.Code != http.StatusNotFound || strings.Contains(out.Body.String(), "Directory listing") {
				t.Fatalf("unsafe static path: status=%d location=%q bytes=%d", out.Code, out.Header().Get("Location"), out.Body.Len())
			}
		})
	}
}

func TestStaticOfficeAssetsRemainAvailableInProductionAndDevelopment(t *testing.T) {
	for _, config := range []Config{
		{Production: true, Password: "production-test-password-1234", PublicURL: "https://chris.example.test"},
		{Demo: true, Password: "demo-conselho", TelegramSecret: "synthetic-webhook"},
	} {
		handler := New(nil, config)
		for _, path := range []string{"/static/office/manifest.json", "/static/office/background.webp", "/static/office/agents-a.webp", "/static/office-scene.webp", "/static/app.js", "/static/fonts/manrope.ttf"} {
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
			if out.Code != http.StatusOK || out.Body.Len() == 0 {
				t.Fatalf("production=%t asset=%s: status=%d bytes=%d", config.Production, path, out.Code, out.Body.Len())
			}
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "/static/reference.png", nil))
		if !config.Production && (out.Code != http.StatusOK || out.Body.Len() == 0) {
			t.Fatal("development reference image became unavailable")
		}
		if !config.Production {
			for _, path := range []string{"/static/office/%2e%2e/referencia_dashboard_opcao_1_refinada.png", "/static/office/%2e%2e/conselho-office-v2.webp"} {
				out := httptest.NewRecorder()
				handler.ServeHTTP(out, httptest.NewRequest(http.MethodGet, path, nil))
				if out.Code != http.StatusNotFound {
					t.Fatalf("office filesystem escaped its root in development: %s status=%d", path, out.Code)
				}
			}
		}
	}
}
