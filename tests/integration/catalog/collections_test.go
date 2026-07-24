package catalog

import (
	"slices"
	"testing"
)

func requireCatalogError(t *testing.T, err error, code string) {
	t.Helper()
	catalogErr, ok := err.(*catalogError)
	if !ok || catalogErr.Code != code {
		t.Fatalf("error = %#v, want catalog code %q", err, code)
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_collections.rs:54
//	test: collection_crud_lifecycle
func TestCollectionCRUDLifecycle(t *testing.T) {
	fixture := newCollectionFixture()
	id, err := fixture.create("movers", "Top Movers", "hyperliquid", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	list := fixture.list()
	if len(list) != 1 || list[0].Slug != "movers" || list[0].IsPublished {
		t.Fatalf("created collections = %#v", list)
	}
	got, err := fixture.get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Collection.Name != "Top Movers" || len(got.Instruments) != 0 {
		t.Fatalf("collection detail = %#v", got)
	}

	err = fixture.update(collectionRecord{
		ID: id, Slug: "movers", Name: "Hot Movers", Description: "the daily movers",
		Venue: "hyperliquid", SortOrder: 3, IsPublished: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = fixture.get(id)
	if got.Collection.Name != "Hot Movers" || !got.Collection.IsPublished {
		t.Fatalf("updated collection = %#v", got.Collection)
	}
	if err := fixture.delete(id, false); err != nil {
		t.Fatal(err)
	}
	if len(fixture.list()) != 0 {
		t.Fatalf("collections after delete = %#v", fixture.list())
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_collections.rs:115
//	test: duplicate_slug_is_conflict
func TestDuplicateSlugIsConflict(t *testing.T) {
	fixture := newCollectionFixture()
	if _, err := fixture.create("dup", "One", "", nil, false); err != nil {
		t.Fatal(err)
	}
	_, err := fixture.create("dup", "Two", "", nil, false)
	requireCatalogError(t, err, "conflict")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_collections.rs:126
//	test: nesting_reparent_and_cycle_guard
func TestNestingReparentAndCycleGuard(t *testing.T) {
	fixture := newCollectionFixture()
	a, _ := fixture.create("a", "A", "", nil, true)
	b, _ := fixture.create("b", "B", "", &a, true)
	c, _ := fixture.create("c", "C", "", &b, true)

	err := fixture.update(collectionRecord{
		ID: a, Slug: "a", Name: "A", ParentID: &c, IsPublished: true,
	})
	requireCatalogError(t, err, "conflict")
	err = fixture.update(collectionRecord{
		ID: b, Slug: "b", Name: "B", ParentID: &b, IsPublished: true,
	})
	requireCatalogError(t, err, "conflict")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_collections.rs:171
//	test: members_set_and_delete_cascade
func TestMembersSetAndDeleteCascade(t *testing.T) {
	fixture := newCollectionFixture()
	if err := fixture.instruments.seedInstrument("BTC-PERP", "BTC"); err != nil {
		t.Fatal(err)
	}
	if err := fixture.instruments.seedInstrument("ETH-PERP", "ETH"); err != nil {
		t.Fatal(err)
	}
	id, _ := fixture.create("majors", "Majors", "", nil, true)
	err := fixture.setMembers(id, []collectionMember{
		{Symbol: "ETH-PERP", Position: 1},
		{Symbol: "BTC-PERP", Position: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := fixture.get(id)
	if len(got.Instruments) != 2 || got.Instruments[0].Symbol != "BTC-PERP" ||
		got.Instruments[1].Symbol != "ETH-PERP" || got.Collection.MemberCount != 2 {
		t.Fatalf("members = %#v", got)
	}

	err = fixture.setMembers(id, []collectionMember{{Symbol: "NOPE-PERP", Position: 0}})
	requireCatalogError(t, err, "bad_request")
	if err := fixture.delete(id, false); err != nil {
		t.Fatal(err)
	}
	if len(fixture.list()) != 0 {
		t.Fatalf("collections after delete = %#v", fixture.list())
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_collections.rs:236
//	test: add_and_remove_single_member
func TestAddAndRemoveSingleMember(t *testing.T) {
	fixture := newCollectionFixture()
	if err := fixture.instruments.seedInstrument("BTC-PERP", "BTC"); err != nil {
		t.Fatal(err)
	}
	id, _ := fixture.create("watch", "Watchlist", "", nil, true)
	if err := fixture.addMember(id, "BTC-PERP", 0); err != nil {
		t.Fatal(err)
	}
	got, _ := fixture.get(id)
	if len(got.Instruments) != 1 || got.Instruments[0].Symbol != "BTC-PERP" {
		t.Fatalf("members after add = %#v", got.Instruments)
	}
	err := fixture.addMember(id, "NOPE-PERP", 0)
	requireCatalogError(t, err, "bad_request")
	if err := fixture.removeMember(id, "BTC-PERP"); err != nil {
		t.Fatal(err)
	}
	got, _ = fixture.get(id)
	if len(got.Instruments) != 0 {
		t.Fatalf("members after remove = %#v", got.Instruments)
	}
	err = fixture.removeMember(id, "BTC-PERP")
	requireCatalogError(t, err, "not_found")
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_collections.rs:301
//	test: delete_restrict_and_force
func TestDeleteRestrictAndForce(t *testing.T) {
	fixture := newCollectionFixture()
	parent, _ := fixture.create("p", "P", "", nil, true)
	if _, err := fixture.create("ch", "Ch", "", &parent, true); err != nil {
		t.Fatal(err)
	}
	err := fixture.delete(parent, false)
	requireCatalogError(t, err, "conflict")
	if err := fixture.delete(parent, true); err != nil {
		t.Fatal(err)
	}
	if len(fixture.list()) != 0 {
		t.Fatalf("collections after force delete = %#v", fixture.list())
	}
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/catalog/e2e_collections.rs:336
//	test: trader_tree_publication_and_venue_filter
func TestTraderTreePublicationAndVenueFilter(t *testing.T) {
	fixture := newCollectionFixture()
	if err := fixture.instruments.seedInstrument("BTC-PERP", "BTC"); err != nil {
		t.Fatal(err)
	}
	hl, _ := fixture.create("hl", "HL", "hyperliquid", nil, true)
	if _, err := fixture.create("hl-shown", "Shown", "hyperliquid", &hl, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.create("hl-hidden", "Hidden", "hyperliquid", &hl, false); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.create("cross", "Cross", "", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.create("cfd", "CFD", "fix_cfd", nil, true); err != nil {
		t.Fatal(err)
	}

	tree := fixture.publicTree("hyperliquid")
	roots := make([]string, len(tree))
	for index, node := range tree {
		roots[index] = node.Slug
	}
	if !slices.Contains(roots, "hl") || !slices.Contains(roots, "cross") || slices.Contains(roots, "cfd") {
		t.Fatalf("root slugs = %v", roots)
	}
	var hlNode collectionNode
	for _, node := range tree {
		if node.Slug == "hl" {
			hlNode = node
		}
	}
	if len(hlNode.Children) != 1 || hlNode.Children[0].Slug != "hl-shown" {
		t.Fatalf("HL children = %#v", hlNode.Children)
	}

	err := fixture.update(collectionRecord{
		ID: hl, Slug: "hl", Name: "HL", Venue: "hyperliquid", IsPublished: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	tree = fixture.publicTree("hyperliquid")
	for _, node := range tree {
		if node.Slug == "hl" || node.Slug == "hl-shown" {
			t.Fatalf("unpublished subtree leaked into public tree: %#v", tree)
		}
	}
}
