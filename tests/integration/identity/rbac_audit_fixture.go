package identity

import (
	"errors"
	"fmt"
	"sort"
)

type rbacAuditPolicy struct {
	Resource string
	Action   string
	Effect   string
}

type rbacAuditRole struct {
	Builtin  bool
	Parents  []string
	Policies []rbacAuditPolicy
}

type rbacAuditAdmin struct {
	ID       string
	Password string
	Roles    []string
}

// rbacAuditStore represents the database shared by independently cached app
// instances. Every authorization mutation advances Stamp.
type rbacAuditStore struct {
	Roles  map[string]*rbacAuditRole
	Admins map[string]*rbacAuditAdmin
	Stamp  uint64
	nextID int
}

func newRbacAuditStore() *rbacAuditStore {
	return &rbacAuditStore{
		Roles:  make(map[string]*rbacAuditRole),
		Admins: make(map[string]*rbacAuditAdmin),
	}
}

func (store *rbacAuditStore) createRole(name string, builtin bool) {
	store.Roles[name] = &rbacAuditRole{Builtin: builtin}
	store.Stamp++
}

func (store *rbacAuditStore) grant(role, resource, action, effect string) error {
	row, ok := store.Roles[role]
	if !ok {
		return fmt.Errorf("role %q does not exist", role)
	}
	row.Policies = append(row.Policies, rbacAuditPolicy{
		Resource: resource,
		Action:   action,
		Effect:   effect,
	})
	store.Stamp++
	return nil
}

func (store *rbacAuditStore) addParent(role, parent string) error {
	row, roleOK := store.Roles[role]
	_, parentOK := store.Roles[parent]
	if !roleOK || !parentOK {
		return errors.New("role or parent does not exist")
	}
	row.Parents = append(row.Parents, parent)
	store.Stamp++
	return nil
}

func (store *rbacAuditStore) seedAdmin(login, password string) string {
	store.nextID++
	id := fmt.Sprintf("urn:admin:%08d-0000-4000-8000-000000000000", store.nextID)
	store.Admins[login] = &rbacAuditAdmin{ID: id, Password: password}
	return id
}

func (store *rbacAuditStore) assign(login, role string) error {
	admin, adminOK := store.Admins[login]
	_, roleOK := store.Roles[role]
	if !adminOK || !roleOK {
		return errors.New("admin or role does not exist")
	}
	admin.Roles = append(admin.Roles, role)
	store.Stamp++
	return nil
}

func (store *rbacAuditStore) revoke(role, resource, action string) (bool, error) {
	row, ok := store.Roles[role]
	if !ok {
		return false, errors.New("role does not exist")
	}
	if row.Builtin {
		return false, errors.New("builtin role is immutable")
	}
	for index, policy := range row.Policies {
		if policy.Resource == resource && policy.Action == action {
			row.Policies = append(row.Policies[:index:index], row.Policies[index+1:]...)
			store.Stamp++
			return true, nil
		}
	}
	return false, nil
}

func (store *rbacAuditStore) unassign(actorLogin, subjectLogin, role string) (bool, error) {
	if actorLogin == subjectLogin {
		return false, errors.New("self-unassign is forbidden")
	}
	admin, ok := store.Admins[subjectLogin]
	if !ok {
		return false, errors.New("admin does not exist")
	}
	for index, assigned := range admin.Roles {
		if assigned == role {
			admin.Roles = append(admin.Roles[:index:index], admin.Roles[index+1:]...)
			store.Stamp++
			return true, nil
		}
	}
	return false, nil
}

type rbacAuditInstance struct {
	store     *rbacAuditStore
	seenStamp uint64
	cache     map[string][]rbacAuditPolicy
}

func newRbacAuditInstance(store *rbacAuditStore) *rbacAuditInstance {
	return &rbacAuditInstance{store: store, cache: make(map[string][]rbacAuditPolicy)}
}

func (instance *rbacAuditInstance) diagnosticsStatus(login, password string) int {
	admin, ok := instance.store.Admins[login]
	if !ok || admin.Password != password {
		return 401
	}
	if instance.allowed(login, "diagnostics", "read") {
		return 200
	}
	return 403
}

func (instance *rbacAuditInstance) allowed(login, resource, action string) bool {
	policies := instance.effectivePermissions(login)
	allowed := false
	for _, policy := range policies {
		resourceMatches := policy.Resource == resource || policy.Resource == "*"
		actionMatches := policy.Action == action || policy.Action == "*"
		if !resourceMatches || !actionMatches {
			continue
		}
		if policy.Effect == "deny" {
			return false
		}
		if policy.Effect == "allow" {
			allowed = true
		}
	}
	return allowed
}

func (instance *rbacAuditInstance) effectivePermissions(login string) []rbacAuditPolicy {
	if instance.seenStamp != instance.store.Stamp {
		instance.cache = make(map[string][]rbacAuditPolicy)
		instance.seenStamp = instance.store.Stamp
	}
	if cached, ok := instance.cache[login]; ok {
		return append([]rbacAuditPolicy(nil), cached...)
	}
	admin := instance.store.Admins[login]
	if admin == nil {
		return nil
	}
	var policies []rbacAuditPolicy
	visited := make(map[string]bool)
	var collect func(string)
	collect = func(roleName string) {
		if visited[roleName] {
			return
		}
		visited[roleName] = true
		role := instance.store.Roles[roleName]
		if role == nil {
			return
		}
		policies = append(policies, role.Policies...)
		for _, parent := range role.Parents {
			collect(parent)
		}
	}
	for _, role := range admin.Roles {
		collect(role)
	}
	instance.cache[login] = append([]rbacAuditPolicy(nil), policies...)
	return policies
}

func (instance *rbacAuditInstance) roles(login string) []string {
	admin := instance.store.Admins[login]
	if admin == nil {
		return nil
	}
	roles := append([]string(nil), admin.Roles...)
	sort.Strings(roles)
	return roles
}

type rbacAuditEvent struct {
	ActorKind string
	ActorID   string
	Action    string
	Resource  string
	TargetID  string
	Outcome   string
}

type rbacAuditTrail struct {
	Events     []rbacAuditEvent
	admins     map[string]rbacAuditAdmin
	users      map[string]rbacAuditAdmin
	revoked    map[string]bool
	nextEntity int
}

func newRbacAuditTrail() *rbacAuditTrail {
	return &rbacAuditTrail{
		admins:  make(map[string]rbacAuditAdmin),
		users:   make(map[string]rbacAuditAdmin),
		revoked: make(map[string]bool),
	}
}

func (trail *rbacAuditTrail) nextUUID() string {
	trail.nextEntity++
	return fmt.Sprintf("%08d-0000-4000-8000-000000000000", trail.nextEntity)
}

func (trail *rbacAuditTrail) registerAdmin(login, password string) string {
	id := trail.nextUUID()
	trail.admins[login] = rbacAuditAdmin{ID: id, Password: password}
	return id
}

func (trail *rbacAuditTrail) createUser(login, password string) string {
	id := trail.nextUUID()
	trail.users[login] = rbacAuditAdmin{ID: id, Password: password}
	return id
}

func (trail *rbacAuditTrail) login(kind, login, password string) error {
	subjects := trail.users
	if kind == "admin" {
		subjects = trail.admins
	}
	subject, exists := subjects[login]
	if !exists || subject.Password != password {
		actorID := ""
		if exists {
			actorID = subject.ID
		}
		trail.record(kind, actorID, "login", "", "", "failure")
		return errors.New("invalid credentials")
	}
	trail.record(kind, subject.ID, "login", "", "", "success")
	trail.record(kind, subject.ID, "token.issue", "", "", "success")
	return nil
}

func (trail *rbacAuditTrail) createBrokerKey() string {
	id := trail.nextUUID()
	trail.record("system", "", "broker-key.create", "api_key", id, "success")
	return id
}

func (trail *rbacAuditTrail) revokeBrokerKey(id string) error {
	if trail.revoked[id] {
		return errors.New("broker key is already revoked")
	}
	trail.revoked[id] = true
	trail.record("system", "", "broker-key.revoke", "api_key", id, "success")
	return nil
}

func (trail *rbacAuditTrail) changePolicy() {
	trail.record("system", "", "policy.change", "policy", "", "success")
}

func (trail *rbacAuditTrail) createUserKey(userID string) string {
	id := trail.nextUUID()
	trail.record("user", userID, "user-key.create", "api_key", id, "success")
	return id
}

func (trail *rbacAuditTrail) confirmMFA(userID string) {
	trail.record("user", userID, "mfa.verify", "", "", "success")
}

func (trail *rbacAuditTrail) record(
	actorKind, actorID, action, resource, targetID, outcome string,
) {
	trail.Events = append(trail.Events, rbacAuditEvent{
		ActorKind: actorKind,
		ActorID:   actorID,
		Action:    action,
		Resource:  resource,
		TargetID:  targetID,
		Outcome:   outcome,
	})
}

func (trail *rbacAuditTrail) events(action, outcome string) []rbacAuditEvent {
	var matched []rbacAuditEvent
	for _, event := range trail.Events {
		if event.Action == action && event.Outcome == outcome {
			matched = append(matched, event)
		}
	}
	return matched
}
