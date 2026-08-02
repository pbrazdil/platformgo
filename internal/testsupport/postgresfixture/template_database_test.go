package postgresfixture_test

import (
	"testing"
	"testing/fstest"

	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
)

func TestCurrentTemplateDigestIsCanonicalAndAuthoritySensitive(t *testing.T) {
	first := fstest.MapFS{
		"20260724000200_second.up.sql": {Data: []byte("SELECT 2;\n")},
		"20260724000100_first.up.sql":  {Data: []byte("SELECT 1;\n")},
	}
	second := fstest.MapFS{
		"20260724000100_first.up.sql":  {Data: []byte("SELECT 1;\n")},
		"20260724000200_second.up.sql": {Data: []byte("SELECT 2;\n")},
	}
	properties := postgresfixture.CurrentTemplateProperties{
		HarnessVersion:       "1",
		PostgresVersion:      "19beta2",
		Encoding:             "UTF8",
		Collation:            "C",
		RoleManifestVersion:  "runtime-roles-v1",
		MigrationManifestTip: "20260724000200_second.up.sql",
	}

	firstDigest, err := postgresfixture.CurrentTemplateDigest(first, properties)
	if err != nil {
		t.Fatalf("digest first manifest: %v", err)
	}
	secondDigest, err := postgresfixture.CurrentTemplateDigest(second, properties)
	if err != nil {
		t.Fatalf("digest reordered manifest: %v", err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("digest depends on filesystem enumeration: %x != %x", firstDigest, secondDigest)
	}

	changedBytes := fstest.MapFS{
		"20260724000100_first.up.sql":  {Data: []byte("SELECT 9;\n")},
		"20260724000200_second.up.sql": {Data: []byte("SELECT 2;\n")},
	}
	changedDigest, err := postgresfixture.CurrentTemplateDigest(changedBytes, properties)
	if err != nil {
		t.Fatalf("digest changed manifest: %v", err)
	}
	if changedDigest == firstDigest {
		t.Fatal("migration byte change did not change template digest")
	}

	properties.RoleManifestVersion = "runtime-roles-v2"
	changedAuthorityDigest, err := postgresfixture.CurrentTemplateDigest(first, properties)
	if err != nil {
		t.Fatalf("digest changed authority: %v", err)
	}
	if changedAuthorityDigest == firstDigest {
		t.Fatal("role authority change did not change template digest")
	}
}

func TestTemplateDatabaseNamesAreBoundedAndSecretFree(t *testing.T) {
	digest := [32]byte{
		0xde, 0xad, 0xbe, 0xef, 0x11, 0x22, 0x33, 0x44,
		0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc,
	}
	templateName, cloneName, err := postgresfixture.TemplateDatabaseNames(
		digest,
		"lease-with-password-sentinel-and-arbitrary-user-text",
	)
	if err != nil {
		t.Fatalf("build template database names: %v", err)
	}
	for _, name := range []string{templateName, cloneName} {
		if len(name) > 63 {
			t.Fatalf("database name %q has %d bytes, want at most 63", name, len(name))
		}
		if !postgresfixture.IsDisposableDatabaseName(name) {
			t.Fatalf("database name %q is outside disposable namespace", name)
		}
		if name == "" || name == postgresfixture.TemplateRootDatabase {
			t.Fatalf("unsafe database name %q", name)
		}
	}
}
