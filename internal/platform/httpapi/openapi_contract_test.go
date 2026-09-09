package httpapi

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestEveryHTTPRouteIsDocumentedInOpenAPI(t *testing.T) {
	serverSource, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	openAPI, err := os.Open("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer openAPI.Close()

	documented := make(map[string]struct{})
	var currentPath string
	scanner := bufio.NewScanner(openAPI)
	for scanner.Scan() {
		line := scanner.Text()
		if (strings.HasPrefix(line, "  /") || strings.HasPrefix(line, "  '/") || strings.HasPrefix(line, "  \"/")) && strings.HasSuffix(line, ":") {
			currentPath = normalizeContractPath(strings.Trim(strings.TrimSuffix(strings.TrimSpace(line), ":"), "'\""))
			continue
		}
		if currentPath != "" && strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "      ") {
			method := strings.TrimSuffix(strings.TrimSpace(line), ":")
			switch method {
			case "get", "post", "put", "delete", "patch":
				documented[strings.ToUpper(method)+" "+currentPath] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	routePattern := regexp.MustCompile(`mux\.HandleFunc\("(GET|POST|PUT|DELETE|PATCH) ([^"]+)`)
	for _, match := range routePattern.FindAllStringSubmatch(string(serverSource), -1) {
		route := match[1] + " " + normalizeContractPath(match[2])
		if _, ok := documented[route]; !ok {
			t.Errorf("HTTP route is missing from docs/openapi.yaml: %s", route)
		}
	}
}

var pathParameterPattern = regexp.MustCompile(`\{[^}]+\}`)

func normalizeContractPath(path string) string {
	return pathParameterPattern.ReplaceAllString(path, "{}")
}

// Global security is empty: each protected Lulu operation must opt into bearer auth.
func TestLuluOpenAPIOperationsRequireBearerAuth(t *testing.T) {
	data, err := os.ReadFile("../../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var path, method string
	var body strings.Builder
	count := 0
	check := func() {
		if method == "" || !strings.Contains(path, "/lulu/") {
			return
		}
		count++
		if !strings.Contains(body.String(), "      security:\n      - bearerAuth: []\n") {
			t.Errorf("%s %s must declare bearerAuth so Swagger sends Authorization", method, path)
		}
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "  \"/") && strings.HasSuffix(line, ":") {
			check()
			method = ""
			path = strings.Trim(strings.TrimSuffix(strings.TrimSpace(line), ":"), "\"")
			body.Reset()
			continue
		}
		if line == "components:" {
			check()
			method = ""
			break
		}
		if strings.HasPrefix(line, "    ") && !strings.HasPrefix(line, "      ") {
			candidate := strings.TrimSuffix(strings.TrimSpace(line), ":")
			switch candidate {
			case "get", "post", "put", "delete", "patch":
				check()
				method = candidate
				body.Reset()
			}
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	if count != 12 {
		t.Fatalf("expected 12 Lulu operations, checked %d", count)
	}
}
