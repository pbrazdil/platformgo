package ids

import "encoding/json"

// ActorID identifies an actor component.
type ActorID string

func ParseActorID(value string) (ActorID, error) {
	return parseASCIIIdentifier[ActorID](value)
}

func MustActorID(value string) ActorID {
	id, err := ParseActorID(value)
	panicOnError(err)
	return id
}

func (id ActorID) String() string { return string(id) }

// ComponentID identifies a system component.
type ComponentID string

func ParseComponentID(value string) (ComponentID, error) {
	return parseASCIIIdentifier[ComponentID](value)
}

func MustComponentID(value string) ComponentID {
	id, err := ParseComponentID(value)
	panicOnError(err)
	return id
}

func (id ComponentID) String() string { return string(id) }

// ExecAlgorithmID identifies an execution algorithm.
type ExecAlgorithmID string

func ParseExecAlgorithmID(value string) (ExecAlgorithmID, error) {
	return parseASCIIIdentifier[ExecAlgorithmID](value)
}

func MustExecAlgorithmID(value string) ExecAlgorithmID {
	id, err := ParseExecAlgorithmID(value)
	panicOnError(err)
	return id
}

func (id ExecAlgorithmID) String() string { return string(id) }

// ClientID identifies a system client.
type ClientID string

func ParseClientID(value string) (ClientID, error) {
	return parseASCIIIdentifier[ClientID](value)
}

func MustClientID(value string) ClientID {
	id, err := ParseClientID(value)
	panicOnError(err)
	return id
}

func (id ClientID) String() string { return string(id) }

func (id *ClientID) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	parsed, err := ParseClientID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// VenueOrderID identifies an order assigned by a trading venue.
type VenueOrderID string

func ParseVenueOrderID(value string) (VenueOrderID, error) {
	return parseASCIIIdentifier[VenueOrderID](value)
}

func MustVenueOrderID(value string) VenueOrderID {
	id, err := ParseVenueOrderID(value)
	panicOnError(err)
	return id
}

func (id VenueOrderID) String() string { return string(id) }

func parseASCIIIdentifier[T ~string](value string) (T, error) {
	if err := validASCII(value); err != nil {
		return "", err
	}
	return T(value), nil
}
