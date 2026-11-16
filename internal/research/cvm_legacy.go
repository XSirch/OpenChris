package research

import (
	"net/url"
	"path"
	"strings"
)

// The two official CVM feeds publish legacy HTTP links whose HTTPS equivalents
// redirect to the same document path. Only normalize these links; never fetch HTTP.
func cvmLegacyHTTPS(rawURL, sourceURL string) string {
	if sourceURL != "https://conteudo.cvm.gov.br/feed/decisoes.xml" && sourceURL != "https://conteudo.cvm.gov.br/feed/legislacao.xml" {
		return rawURL
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "http" || !strings.EqualFold(u.Hostname(), "www.cvm.gov.br") || u.User != nil || strings.Contains(rawURL, "#") || u.Port() != "" && u.Port() != "80" {
		return rawURL
	}
	if u.Path != path.Clean(u.Path) || !strings.HasPrefix(u.Path, "/decisoes/") && !strings.HasPrefix(u.Path, "/legislacao/") {
		return rawURL
	}
	u.Scheme, u.Host = "https", "www.cvm.gov.br"
	return u.String()
}
