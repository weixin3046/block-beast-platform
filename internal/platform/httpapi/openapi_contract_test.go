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
		if strings.HasPrefix(line, "  /") && strings.HasSuffix(line, ":") {
			currentPath = normalizeContractPath(strings.TrimSuffix(strings.TrimSpace(line), ":"))
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
