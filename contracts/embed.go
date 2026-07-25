// Package contracts exposes immutable external contract artifacts.
package contracts

import (
	"embed"
	"fmt"
)

//go:embed openapi/*.json
var artifacts embed.FS

// OpenAPIDocuments returns defensive copies keyed by their HTTP routes.
func OpenAPIDocuments() (map[string][]byte, error) {
	files := map[string]string{
		"/admin/v1/openapi.json":  "openapi/admin-v1.json",
		"/v1/openapi.json":        "openapi/client-v1.json",
		"/broker/v1/openapi.json": "openapi/broker-v1.json",
	}
	documents := make(map[string][]byte, len(files))
	for route, name := range files {
		document, err := artifacts.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read embedded contract %s: %w", name, err)
		}
		documents[route] = append([]byte(nil), document...)
	}
	return documents, nil
}
