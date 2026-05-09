package nginx_test

import (
	"os"
	"strings"
	"testing"
)

func TestConnectivityWebSocketRoutesStayProxiedInNginxTemplates(t *testing.T) {
	templates := []string{
		"agentunnel-http.conf.template",
		"agentunnel-tls.conf.template",
		"../../ansible/templates/nginx-site-http.conf.j2",
		"../../ansible/templates/nginx-site-tls.conf.j2",
	}
	for _, path := range templates {
		t.Run(path, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile returned error: %v", err)
			}
			got := string(raw)
			for _, want := range []string{
				"location /api/",
				"location = /connectivity/computer/ws",
				"location = /connectivity/daemon/ws",
				"proxy_http_version 1.1",
				"proxy_set_header Upgrade $http_upgrade",
				"proxy_set_header Connection $connection_upgrade",
			} {
				if !strings.Contains(got, want) {
					t.Fatalf("%s missing %q", path, want)
				}
			}
		})
	}
}
