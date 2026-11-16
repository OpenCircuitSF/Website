package audit

import "strconv"

// Metadata keys AttributeAPIToken writes (#0674).
const (
	// MetaActor holds "token:{id}" on a row written by an API-token request.
	MetaActor = "actor"
	// MetaAPIToken holds the token's id, name, prefix and created_by.
	MetaAPIToken = "api_token"
)

// APITokenActor identifies the API token behind a write (#0674). It has no
// field for the secret, so attribution can never record one.
type APITokenActor struct {
	ID        int64
	Name      string
	Prefix    string // the non-secret display prefix
	CreatedBy *int64 // the admin who created the token; nil once deleted
}

// AttributeAPIToken returns e attributed to the token t. ActorID becomes nil,
// since a token is not a user, and the metadata gains MetaActor
// ("token:{id}") and MetaAPIToken ({id, name, prefix, created_by}). A
// map[string]any metadata is copied before the keys are added; nil becomes a
// new map; any other metadata value is kept under "details".
func AttributeAPIToken(e Entry, t APITokenActor) Entry {
	meta := map[string]any{}
	switch m := e.Metadata.(type) {
	case nil:
	case map[string]any:
		for k, v := range m {
			meta[k] = v
		}
	default:
		meta["details"] = m
	}
	meta[MetaActor] = "token:" + strconv.FormatInt(t.ID, 10)
	meta[MetaAPIToken] = map[string]any{
		"id":         t.ID,
		"name":       t.Name,
		"prefix":     t.Prefix,
		"created_by": t.CreatedBy,
	}
	e.ActorID = nil
	e.Metadata = meta
	return e
}
