package catalog

import (
	"fmt"
	"sort"
)

type catalogError struct {
	Code    string
	Message string
}

func (err *catalogError) Error() string {
	return err.Code + ": " + err.Message
}

type collectionRecord struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Venue       string  `json:"venue,omitempty"`
	ParentID    *string `json:"parentId,omitempty"`
	SortOrder   int     `json:"sortOrder"`
	IsPublished bool    `json:"isPublished"`
	MemberCount int     `json:"memberCount"`
}

type collectionMember struct {
	Symbol   string `json:"symbol"`
	Position int    `json:"position"`
}

type collectionDetail struct {
	Collection  collectionRecord   `json:"collection"`
	Instruments []instrumentRecord `json:"instruments"`
}

type collectionNode struct {
	collectionRecord
	Children []collectionNode `json:"children"`
}

type collectionFixture struct {
	instruments *instrumentFixture
	collections map[string]collectionRecord
	members     map[string][]collectionMember
	nextID      int
}

func newCollectionFixture() *collectionFixture {
	return &collectionFixture{
		instruments: newInstrumentFixture(),
		collections: make(map[string]collectionRecord),
		members:     make(map[string][]collectionMember),
	}
}

func (fixture *collectionFixture) create(
	slug, name, venue string,
	parentID *string,
	published bool,
) (string, error) {
	if fixture.idBySlug(slug) != "" {
		return "", &catalogError{Code: "conflict", Message: "collection slug already exists"}
	}
	if parentID != nil {
		if _, ok := fixture.collections[*parentID]; !ok {
			return "", &catalogError{Code: "bad_request", Message: "parent collection does not exist"}
		}
	}
	fixture.nextID++
	id := fmt.Sprintf("collection-%d", fixture.nextID)
	parentCopy := copyStringPointer(parentID)
	fixture.collections[id] = collectionRecord{
		ID: id, Slug: slug, Name: name, Venue: venue, ParentID: parentCopy,
		IsPublished: published,
	}
	return id, nil
}

func (fixture *collectionFixture) update(record collectionRecord) error {
	current, ok := fixture.collections[record.ID]
	if !ok {
		return &catalogError{Code: "not_found", Message: "collection does not exist"}
	}
	if duplicate := fixture.idBySlug(record.Slug); duplicate != "" && duplicate != record.ID {
		return &catalogError{Code: "conflict", Message: "collection slug already exists"}
	}
	if record.ParentID != nil {
		if *record.ParentID == record.ID || fixture.isDescendant(*record.ParentID, record.ID) {
			return &catalogError{Code: "conflict", Message: "collection hierarchy cycle"}
		}
		if _, exists := fixture.collections[*record.ParentID]; !exists {
			return &catalogError{Code: "bad_request", Message: "parent collection does not exist"}
		}
	}
	record.MemberCount = current.MemberCount
	record.ParentID = copyStringPointer(record.ParentID)
	fixture.collections[record.ID] = record
	return nil
}

func (fixture *collectionFixture) list() []collectionRecord {
	rows := make([]collectionRecord, 0, len(fixture.collections))
	for _, row := range fixture.collections {
		row.MemberCount = len(fixture.members[row.ID])
		rows = append(rows, row)
	}
	sort.Slice(rows, func(left, right int) bool {
		if rows[left].SortOrder != rows[right].SortOrder {
			return rows[left].SortOrder < rows[right].SortOrder
		}
		return rows[left].Slug < rows[right].Slug
	})
	return rows
}

func (fixture *collectionFixture) get(id string) (collectionDetail, error) {
	record, ok := fixture.collections[id]
	if !ok {
		return collectionDetail{}, &catalogError{Code: "not_found", Message: "collection does not exist"}
	}
	members := append([]collectionMember(nil), fixture.members[id]...)
	sort.Slice(members, func(left, right int) bool {
		if members[left].Position != members[right].Position {
			return members[left].Position < members[right].Position
		}
		return members[left].Symbol < members[right].Symbol
	})
	instruments := make([]instrumentRecord, 0, len(members))
	for _, member := range members {
		row, _ := fixture.instruments.bySymbol(member.Symbol)
		instruments = append(instruments, row)
	}
	record.MemberCount = len(instruments)
	return collectionDetail{Collection: record, Instruments: instruments}, nil
}

func (fixture *collectionFixture) setMembers(id string, members []collectionMember) error {
	if _, ok := fixture.collections[id]; !ok {
		return &catalogError{Code: "not_found", Message: "collection does not exist"}
	}
	for _, member := range members {
		if _, ok := fixture.instruments.bySymbol(member.Symbol); !ok {
			return &catalogError{Code: "bad_request", Message: "unknown instrument " + member.Symbol}
		}
	}
	fixture.members[id] = append([]collectionMember(nil), members...)
	return nil
}

func (fixture *collectionFixture) addMember(id, symbol string, position int) error {
	if _, ok := fixture.instruments.bySymbol(symbol); !ok {
		return &catalogError{Code: "bad_request", Message: "unknown instrument " + symbol}
	}
	for _, member := range fixture.members[id] {
		if member.Symbol == symbol {
			return &catalogError{Code: "conflict", Message: "instrument is already a member"}
		}
	}
	fixture.members[id] = append(fixture.members[id], collectionMember{Symbol: symbol, Position: position})
	return nil
}

func (fixture *collectionFixture) removeMember(id, symbol string) error {
	members := fixture.members[id]
	for index, member := range members {
		if member.Symbol == symbol {
			fixture.members[id] = append(members[:index:index], members[index+1:]...)
			return nil
		}
	}
	return &catalogError{Code: "not_found", Message: "instrument is not a collection member"}
}

func (fixture *collectionFixture) delete(id string, force bool) error {
	if _, ok := fixture.collections[id]; !ok {
		return &catalogError{Code: "not_found", Message: "collection does not exist"}
	}
	children := fixture.childrenOf(id)
	if len(children) != 0 && !force {
		return &catalogError{Code: "conflict", Message: "collection has children"}
	}
	for _, childID := range children {
		if err := fixture.delete(childID, true); err != nil {
			return err
		}
	}
	delete(fixture.members, id)
	delete(fixture.collections, id)
	return nil
}

func (fixture *collectionFixture) publicTree(venue string) []collectionNode {
	roots := make([]collectionNode, 0)
	for _, row := range fixture.list() {
		if row.ParentID != nil || !row.IsPublished || !venueMatches(row.Venue, venue) {
			continue
		}
		roots = append(roots, fixture.publicNode(row, venue))
	}
	return roots
}

func (fixture *collectionFixture) publicNode(row collectionRecord, venue string) collectionNode {
	node := collectionNode{collectionRecord: row, Children: []collectionNode{}}
	for _, candidate := range fixture.list() {
		if candidate.ParentID == nil || *candidate.ParentID != row.ID ||
			!candidate.IsPublished || !venueMatches(candidate.Venue, venue) {
			continue
		}
		node.Children = append(node.Children, fixture.publicNode(candidate, venue))
	}
	return node
}

func (fixture *collectionFixture) idBySlug(slug string) string {
	for id, row := range fixture.collections {
		if row.Slug == slug {
			return id
		}
	}
	return ""
}

func (fixture *collectionFixture) isDescendant(candidateID, ancestorID string) bool {
	currentID := candidateID
	for currentID != "" {
		if currentID == ancestorID {
			return true
		}
		current, ok := fixture.collections[currentID]
		if !ok || current.ParentID == nil {
			return false
		}
		currentID = *current.ParentID
	}
	return false
}

func (fixture *collectionFixture) childrenOf(id string) []string {
	children := make([]string, 0)
	for childID, row := range fixture.collections {
		if row.ParentID != nil && *row.ParentID == id {
			children = append(children, childID)
		}
	}
	sort.Strings(children)
	return children
}

func venueMatches(collectionVenue, requestedVenue string) bool {
	return collectionVenue == "" || requestedVenue == "" || collectionVenue == requestedVenue
}

func copyStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
