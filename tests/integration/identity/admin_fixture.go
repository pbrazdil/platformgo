package identity

import (
	"fmt"
	"strings"
)

const (
	adminStatusOK                             = 200
	adminStatusUnauthorized                   = 401
	adminRuntimeTopic                         = "uzo.events.settings.changed"
	adminRiskLevelsKey                        = "risk.levels"
	errAdminForbidden       adminFixtureError = "forbidden"
	errAdminBadRequest      adminFixtureError = "bad request"
)

type adminFixtureError string

func (err adminFixtureError) Error() string { return string(err) }

type adminRiskSettings struct {
	MarginCallLevelPercent int
	StopOutLevelPercent    int
	FormMarketSlippageBPS  int
	CloseSlippageBPS       int
	TPSLSlippageBPS        int
}

type adminSettings struct {
	Environment string
	Risk        adminRiskSettings
	Currencies  []string
}

type adminCatalogItem struct {
	ID    string
	Label string
}

type adminDoorbell struct {
	Topic string
	Key   string
}

type adminOperator struct {
	ID     string
	Login  string
	Email  string
	Status string
}

type adminUser struct {
	ID             string
	Login          string
	Email          string
	Status         string
	FailedAttempts int
}

type adminPolicy struct {
	Object string
	Action string
	Effect string
}

type adminPlaneFixture struct {
	settings     adminSettings
	doorbells    []adminDoorbell
	admins       map[string]adminOperator
	adminByLogin map[string]string
	passwords    map[string]string
	users        map[string]adminUser
	roles        map[string]string
	policies     map[string][]adminPolicy
	assignments  map[string]map[string]bool
	sessions     map[string][]string
	brokerKeys   []string
	tokens       map[string]string
	nextAdmin    int
	nextUser     int
}

func newAdminPlaneFixture() *adminPlaneFixture {
	return &adminPlaneFixture{
		settings: adminSettings{
			Environment: "test",
			Risk: adminRiskSettings{
				MarginCallLevelPercent: 100, StopOutLevelPercent: 50,
				FormMarketSlippageBPS: 50, CloseSlippageBPS: 800, TPSLSlippageBPS: 1000,
			},
			Currencies: []string{"USD", "EUR"},
		},
		admins: make(map[string]adminOperator), adminByLogin: make(map[string]string),
		passwords: make(map[string]string), users: make(map[string]adminUser),
		roles: make(map[string]string), policies: make(map[string][]adminPolicy),
		assignments: make(map[string]map[string]bool), sessions: make(map[string][]string),
		tokens: make(map[string]string),
	}
}

func (fixture *adminPlaneFixture) setRiskLevels(admin bool, marginCall, stopOut int) (string, error) {
	if !admin {
		return "", errAdminForbidden
	}
	if marginCall <= stopOut || stopOut <= 0 {
		return "", errAdminBadRequest
	}
	fixture.settings.Risk.MarginCallLevelPercent = marginCall
	fixture.settings.Risk.StopOutLevelPercent = stopOut
	fixture.doorbells = append(fixture.doorbells, adminDoorbell{
		Topic: adminRuntimeTopic, Key: adminRiskLevelsKey,
	})
	return fmt.Sprintf("%d/%d", marginCall, stopOut), nil
}

func (fixture *adminPlaneFixture) setSlippageBands(admin bool, form, closeBand, tpSL int) (string, error) {
	if !admin {
		return "", errAdminForbidden
	}
	if form <= 0 || closeBand <= 0 || tpSL <= 0 {
		return "", errAdminBadRequest
	}
	fixture.settings.Risk.FormMarketSlippageBPS = form
	fixture.settings.Risk.CloseSlippageBPS = closeBand
	fixture.settings.Risk.TPSLSlippageBPS = tpSL
	return fmt.Sprintf("%d/%d/%d", form, closeBand, tpSL), nil
}

func (fixture *adminPlaneFixture) readSettings(admin bool) (adminSettings, error) {
	if !admin {
		return adminSettings{}, errAdminForbidden
	}
	return fixture.settings, nil
}

func (fixture *adminPlaneFixture) permissionCatalog(admin bool) ([]adminCatalogItem, []adminCatalogItem, error) {
	if !admin {
		return nil, nil, errAdminForbidden
	}
	resourceIDs := []string{
		"diagnostics", "admins", "users", "roles", "api-keys", "schedules",
		"accounts", "orders", "instruments", "collections", "tenants",
	}
	actionIDs := []string{"read", "write", "create", "delete"}
	resources := make([]adminCatalogItem, len(resourceIDs))
	for index, id := range resourceIDs {
		resources[index] = adminCatalogItem{ID: id, Label: strings.ToUpper(id[:1]) + id[1:]}
	}
	actions := make([]adminCatalogItem, len(actionIDs))
	for index, id := range actionIDs {
		actions[index] = adminCatalogItem{ID: id, Label: strings.ToUpper(id[:1]) + id[1:]}
	}
	return resources, actions, nil
}

func (fixture *adminPlaneFixture) registerAdmin(login, email, password string) adminOperator {
	fixture.nextAdmin++
	id := fmt.Sprintf("urn:xb:admin:%d", fixture.nextAdmin)
	admin := adminOperator{ID: id, Login: login, Email: email, Status: "active"}
	fixture.admins[id], fixture.adminByLogin[login], fixture.passwords[id] = admin, id, password
	return admin
}

func (fixture *adminPlaneFixture) login(login, password string) (int, string) {
	id, ok := fixture.adminByLogin[login]
	if !ok || fixture.passwords[id] != password || fixture.admins[id].Status != "active" {
		return adminStatusUnauthorized, ""
	}
	token := "admin-token::" + id
	fixture.tokens[token] = id
	return adminStatusOK, token
}

func (fixture *adminPlaneFixture) me(token string) (int, string, string) {
	id, ok := fixture.tokens[token]
	if !ok {
		return adminStatusUnauthorized, "", ""
	}
	return adminStatusOK, "admin::" + id, "admin"
}

func (fixture *adminPlaneFixture) runCommand(name string, args map[string]string) (map[string]string, error) {
	switch name {
	case "admin.register":
		admin := fixture.registerAdmin(args["login"], args["email"], args["password"])
		return map[string]string{"id": admin.ID}, nil
	case "admin.login":
		status, token := fixture.login(args["login"], args["password"])
		if status != adminStatusOK {
			return nil, errAdminForbidden
		}
		return map[string]string{"accessToken": token}, nil
	default:
		return nil, errAdminBadRequest
	}
}

func (fixture *adminPlaneFixture) seedUser(login string) adminUser {
	fixture.nextUser++
	user := adminUser{
		ID:    fmt.Sprintf("urn:xb:user:%d", fixture.nextUser),
		Login: login, Email: login + "@example.com", Status: "active",
	}
	fixture.users[user.ID] = user
	return user
}

func (fixture *adminPlaneFixture) createRole(name, description string) {
	fixture.roles[name] = description
}

func (fixture *adminPlaneFixture) grant(admin bool, role, object, action, effect string) error {
	if !admin {
		return errAdminForbidden
	}
	fixture.policies[role] = append(fixture.policies[role], adminPolicy{
		Object: object, Action: action, Effect: effect,
	})
	return nil
}

func (fixture *adminPlaneFixture) assign(adminID, role string) {
	if fixture.assignments[adminID] == nil {
		fixture.assignments[adminID] = make(map[string]bool)
	}
	fixture.assignments[adminID][role] = true
}

func (fixture *adminPlaneFixture) setAdminStatus(id, status string) (adminOperator, error) {
	if status != "active" && status != "disabled" {
		return adminOperator{}, errAdminBadRequest
	}
	admin, ok := fixture.admins[id]
	if !ok {
		return adminOperator{}, errAdminBadRequest
	}
	admin.Status, fixture.admins[id] = status, admin
	return admin, nil
}

func (fixture *adminPlaneFixture) revokeSession(sessionID string) error {
	for userID, sessions := range fixture.sessions {
		for index, id := range sessions {
			if id == sessionID {
				fixture.sessions[userID] = append(sessions[:index], sessions[index+1:]...)
				return nil
			}
		}
	}
	return errAdminBadRequest
}

func (fixture *adminPlaneFixture) listUsers(query string) []adminUser {
	users := make([]adminUser, 0, len(fixture.users))
	for _, user := range fixture.users {
		if query == "" || strings.Contains(user.Login, query) {
			users = append(users, user)
		}
	}
	return users
}

func (fixture *adminPlaneFixture) updateUser(admin bool, id, email string) (adminUser, error) {
	if !admin {
		return adminUser{}, errAdminForbidden
	}
	user, ok := fixture.users[id]
	if !ok {
		return adminUser{}, errAdminBadRequest
	}
	user.Email, fixture.users[id] = email, user
	return user, nil
}

func (fixture *adminPlaneFixture) setUserStatus(id, status string) adminUser {
	user := fixture.users[id]
	user.Status, fixture.users[id] = status, user
	return user
}

func (fixture *adminPlaneFixture) unlockUser(id string) adminUser {
	user := fixture.users[id]
	user.FailedAttempts, fixture.users[id] = 0, user
	return user
}
