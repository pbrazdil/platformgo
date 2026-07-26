package contracts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

type compatibilityManifest struct {
	PlatformSourceRevision string            `json:"platformSourceRevision"`
	OpenAPIArtifacts       map[string]string `json:"openAPIArtifacts"`
	ProtobufDescriptor     artifactHash      `json:"protobufDescriptor"`
	RealtimeFixture        artifactHash      `json:"realtimeFixture"`
	SupportedRoleCommands  []string          `json:"supportedRoleCommands"`
	EnvironmentKeys        []string          `json:"environmentKeys"`
	ImplementedHTTPRoutes  []string          `json:"implementedHTTPRoutes"`
	ImplementedWorkers     []string          `json:"implementedWorkerHandlers"`
	SourceWorkerInventory  []string          `json:"sourceWorkerHandlerInventory"`
	GRPCSurface            grpcSurface       `json:"grpcSurface"`
	IntentionalDeviations  []json.RawMessage `json:"intentionalDeviations"`
}

type grpcSurface struct {
	LegacySourceService bool   `json:"legacySourceService"`
	Status              string `json:"status"`
}

type artifactHash struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func TestCompatibilityManifestHashesAndSourceRevision(t *testing.T) {
	manifest := readManifest(t)
	if manifest.PlatformSourceRevision != "50141367492be46ebf5623f6191a14b94af2f2bd" {
		t.Fatalf("source revision = %q", manifest.PlatformSourceRevision)
	}
	for name, expected := range manifest.OpenAPIArtifacts {
		assertHash(t, name, expected)
	}
	assertHash(t, manifest.ProtobufDescriptor.Path, manifest.ProtobufDescriptor.SHA256)
	assertHash(t, manifest.RealtimeFixture.Path, manifest.RealtimeFixture.SHA256)
	wantRoles := []string{
		"app serve",
		"app worker --handlers=<role>",
		"app migrate",
		"app doctor",
		"nautilus",
	}
	if !reflect.DeepEqual(manifest.SupportedRoleCommands, wantRoles) {
		t.Fatalf("role commands = %v", manifest.SupportedRoleCommands)
	}
	if !contains(manifest.EnvironmentKeys, "UZO_ENGINE_SHARD_ID") {
		t.Fatal("UZO_ENGINE_SHARD_ID missing from compatibility manifest")
	}
	if !contains(manifest.EnvironmentKeys, "UZO_TRUSTED_PROXY_CIDRS") {
		t.Fatal("UZO_TRUSTED_PROXY_CIDRS missing from compatibility manifest")
	}
	if !reflect.DeepEqual(manifest.ImplementedWorkers, []string{
		"outbox-publisher", "realtime-publisher",
		"event-consumer", "event-consumer:<pattern>",
	}) {
		t.Fatalf("implemented workers = %v", manifest.ImplementedWorkers)
	}
	if !reflect.DeepEqual(manifest.SourceWorkerInventory, []string{
		"outbox-publisher", "event-consumer", "marketdata", "cron-scheduler",
	}) {
		t.Fatalf("source worker inventory = %v", manifest.SourceWorkerInventory)
	}
	if manifest.GRPCSurface.LegacySourceService ||
		manifest.GRPCSurface.Status != "additive-versioned-go-contract" {
		t.Fatalf("gRPC surface = %#v", manifest.GRPCSurface)
	}
	if !contains(manifest.ImplementedHTTPRoutes, "GET /v1/me/accounts") {
		t.Fatal("GET /v1/me/accounts missing from compatibility manifest")
	}
}

func TestOpenAPIContractContainsPinnedLifecycleAssertions(t *testing.T) {
	documents, err := OpenAPIDocuments()
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 3 {
		t.Fatalf("documents = %d, want 3", len(documents))
	}
	admin := decodeDocument(t, documents["/admin/v1/openapi.json"])
	assertPointer(t, admin, "components", "schemas", "LoginRequest")
	assertMethod(t, admin, "/admin/v1/auth/login", "post")
	assertPointer(t, admin, "components", "securitySchemes", "bearer")

	client := decodeDocument(t, documents["/v1/openapi.json"])
	myAccounts := assertMethod(t, client, "/v1/me/accounts", "get")
	assertPointer(t, client, "components", "schemas", "MyAccountView")
	assertResponse(t, myAccounts, "200")
	assertResponse(t, myAccounts, "401")
	assertResponse(t, myAccounts, "503")
	if myAccounts["x-platformgo-contract-status"] != "phase3-accepted-runtime" {
		t.Fatalf(
			"my accounts contract status = %v",
			myAccounts["x-platformgo-contract-status"],
		)
	}
	assertOperationSecurity(t, myAccounts, "bearer")
	funding := assertMethod(t, client, "/v1/accounts/{accountId}/funding", "get")
	assertPointer(t, client, "components", "schemas", "FundingView")
	assertPointer(t, client, "components", "schemas", "FundingPage")
	assertResponse(t, funding, "200")
	if funding["x-platformgo-contract-status"] != "phase3-accepted-runtime" {
		t.Fatalf("funding contract status = %v", funding["x-platformgo-contract-status"])
	}
	assertOperationSecurity(t, funding, "bearer")
	order := assertMethod(t, client, "/v1/accounts/{accountId}/orders", "post")
	parameters, ok := order["parameters"].([]any)
	if !ok {
		t.Fatal("submit-order parameters missing")
	}
	found := false
	for _, value := range parameters {
		parameter, _ := value.(map[string]any)
		found = found ||
			(parameter["name"] == "Idempotency-Key" && parameter["in"] == "header")
	}
	if !found {
		t.Fatalf("Idempotency-Key header missing: %v", parameters)
	}

	broker := decodeDocument(t, documents["/broker/v1/openapi.json"])
	assertMethod(t, broker, "/broker/v1/ping", "get")
	assertPointer(t, broker, "components", "securitySchemes", "apiKey")
	account := assertMethod(t, broker, "/broker/v1/accounts", "post")
	assertResponse(t, account, "201")
	if account["x-platformgo-contract-status"] != "phase3-accepted-runtime" {
		t.Fatalf("broker account contract status = %v", account["x-platformgo-contract-status"])
	}
	inventory := assertMethod(t, broker, "/broker/v1/accounts/{accountId}", "get")
	if inventory["x-platformgo-contract-status"] != "source-route-inventory" {
		t.Fatalf("broker account read inventory status = %v", inventory["x-platformgo-contract-status"])
	}
}

func assertResponse(t *testing.T, operation map[string]any, status string) {
	t.Helper()
	responses, ok := operation["responses"].(map[string]any)
	if !ok {
		t.Fatal("responses missing")
	}
	if _, ok := responses[status]; !ok {
		t.Fatalf("response %s missing: %v", status, responses)
	}
}

func assertOperationSecurity(
	t *testing.T,
	operation map[string]any,
	scheme string,
) {
	t.Helper()
	security, ok := operation["security"].([]any)
	if !ok || len(security) != 1 {
		t.Fatalf("operation security = %v, want one %s requirement", security, scheme)
	}
	requirement, ok := security[0].(map[string]any)
	if !ok {
		t.Fatalf("operation security requirement = %v", security[0])
	}
	if _, ok := requirement[scheme]; !ok {
		t.Fatalf("operation security = %v, want %s", requirement, scheme)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestProtobufDescriptorFreezesServiceAndEconomicStringFields(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("proto", "platform", "v1", "trading.pb"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.File) != 1 {
		t.Fatalf("files = %d", len(descriptor.File))
	}
	file := descriptor.File[0]
	if file.GetPackage() != "platform.v1" ||
		len(file.Service) != 1 ||
		file.Service[0].GetName() != "TradingService" ||
		file.Service[0].Method[0].GetName() != "SubmitOrder" {
		t.Fatalf("descriptor service = %#v", file.Service)
	}
	request := file.MessageType[0]
	wantFields := map[string]int32{
		"account_id": 1, "intent_id": 2, "symbol": 3, "side": 4, "type": 5,
		"quantity": 6, "price": 7, "trigger_price": 8, "reduce_only": 9,
		"time_in_force": 10, "max_slippage_bps": 11, "idempotency_key": 12,
	}
	for _, field := range request.Field {
		if want, ok := wantFields[field.GetName()]; !ok || field.GetNumber() != want {
			t.Fatalf("field %s = %d", field.GetName(), field.GetNumber())
		}
		switch field.GetName() {
		case "quantity", "price", "trigger_price":
			if field.GetType() != descriptorpb.FieldDescriptorProto_TYPE_STRING {
				t.Fatalf("%s type = %s, want string", field.GetName(), field.GetType())
			}
		}
	}
}

func readManifest(t *testing.T) compatibilityManifest {
	t.Helper()
	raw, err := os.ReadFile("compatibility-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest compatibilityManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func assertHash(t *testing.T, name, expected string) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	actual := sha256.Sum256(raw)
	if got := hex.EncodeToString(actual[:]); got != expected {
		t.Fatalf("%s sha256 = %s, want %s", name, got, expected)
	}
}

func decodeDocument(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func assertMethod(
	t *testing.T,
	document map[string]any,
	path string,
	method string,
) map[string]any {
	t.Helper()
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		t.Fatal("paths missing")
	}
	pathItem, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("path %s missing", path)
	}
	operation, ok := pathItem[method].(map[string]any)
	if !ok {
		t.Fatalf("%s %s missing", method, path)
	}
	return operation
}

func assertPointer(t *testing.T, document map[string]any, segments ...string) {
	t.Helper()
	var value any = document
	for _, segment := range segments {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("pointer %v stopped at %q", segments, segment)
		}
		value, ok = object[segment]
		if !ok {
			t.Fatalf("pointer %v missing %q", segments, segment)
		}
	}
}
