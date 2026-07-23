package compatibility

import (
	"sort"
	"strings"
)

const SchemaVersion = "slizen.compatibility.v1"

type Status string

const (
	StatusSupported   Status = "supported"
	StatusPassThrough Status = "pass-through"
	StatusRejected    Status = "rejected"
	StatusUnsupported Status = "unsupported"
)

type Class string

const (
	ClassLocal            Class = "local"
	ClassRead             Class = "read"
	ClassWrite            Class = "write"
	ClassPassThrough      Class = "pass-through"
	ClassRejectedMutation Class = "rejected-mutation"
	ClassRejectedStateful Class = "rejected-stateful"
	ClassRejectedBlocking Class = "rejected-blocking"
	ClassUnsupported      Class = "unsupported"
)

type Entry struct {
	Command                string `json:"command"`
	Status                 Status `json:"status"`
	Class                  Class  `json:"class"`
	Compatible             bool   `json:"command_name_compatible"`
	ArgumentReviewRequired bool   `json:"argument_review_required,omitempty"`
	Behavior               string `json:"behavior"`
	Limitations            string `json:"limitations,omitempty"`
}

// commandCatalog is sorted by command and never mutated. Keeping the runtime
// lookup table private lets Catalog return a defensive copy to callers.
var commandCatalog = [...]Entry{
	supported("BLMOVE", ClassRejectedBlocking, "Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model."),
	supported("BLPOP", ClassRejectedBlocking, "Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model."),
	supported("BRPOP", ClassRejectedBlocking, "Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model."),
	supported("BRPOPLPUSH", ClassRejectedBlocking, "Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model."),
	supported("BZPOPMAX", ClassRejectedBlocking, "Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model."),
	supported("BZPOPMIN", ClassRejectedBlocking, "Rejected before upstream dispatch because blocking commands are outside the v0.2 connection model."),
	supported("DEL", ClassWrite, "Affected local entries are invalidated before upstream dispatch and remain invalidated if the upstream returns an error or the outcome is ambiguous."),
	supported("EXEC", ClassRejectedStateful, "Rejected before upstream dispatch because transactions are stateful and unsupported."),
	supported("EXISTS", ClassPassThrough, "Forwarded to the upstream without local cache behavior."),
	supportedWithLimitations("EXPIRE", ClassWrite, "The affected local entry is invalidated before upstream dispatch and remains invalidated if the upstream returns an error or the outcome is ambiguous.", "Only EXPIRE key seconds is accepted; NX, XX, GT, and LT options are unsupported."),
	supported("GET", ClassRead, "Cache-aware in cache mode and always forwarded in observe mode."),
	supported("HDEL", ClassRejectedMutation, "Rejected before upstream dispatch because hash mutations are outside the v0.2 invalidation contract."),
	supported("HSET", ClassRejectedMutation, "Rejected before upstream dispatch because hash mutations are outside the v0.2 invalidation contract."),
	supported("LPOP", ClassRejectedMutation, "Rejected before upstream dispatch because list mutations are outside the v0.2 invalidation contract."),
	supported("LPUSH", ClassRejectedMutation, "Rejected before upstream dispatch because list mutations are outside the v0.2 invalidation contract."),
	supported("MGET", ClassRead, "Ordered multi-key cache-aware read in cache mode and upstream read in observe mode."),
	supported("MONITOR", ClassRejectedStateful, "Rejected before upstream dispatch because monitoring mode is connection-stateful."),
	supported("MSET", ClassRejectedMutation, "Rejected before upstream dispatch because multi-key writes are outside the v0.2 invalidation contract."),
	supported("MULTI", ClassRejectedStateful, "Rejected before upstream dispatch because transactions are stateful and unsupported."),
	supported("PERSIST", ClassWrite, "The affected local entry is invalidated before upstream dispatch and remains invalidated if the upstream returns an error or the outcome is ambiguous."),
	supportedWithLimitations("PEXPIRE", ClassWrite, "The affected local entry is invalidated before upstream dispatch and remains invalidated if the upstream returns an error or the outcome is ambiguous.", "Only PEXPIRE key milliseconds is accepted; NX, XX, GT, and LT options are unsupported."),
	supported("PING", ClassLocal, "Handled locally and returns PONG or the provided payload."),
	supported("PSETEX", ClassWrite, "The affected local entry is invalidated before upstream dispatch and remains invalidated if the upstream returns an error or the outcome is ambiguous."),
	supported("PSUBSCRIBE", ClassRejectedStateful, "Rejected before upstream dispatch because Pub/Sub is connection-stateful and unsupported."),
	supported("PTTL", ClassPassThrough, "Forwarded to the upstream without local cache behavior."),
	supported("QUIT", ClassLocal, "Returns OK, stops dispatching the current pipeline, and closes the client connection."),
	supported("RENAME", ClassRejectedMutation, "Rejected before upstream dispatch until source and destination invalidation are supported."),
	supported("RPOP", ClassRejectedMutation, "Rejected before upstream dispatch because list mutations are outside the v0.2 invalidation contract."),
	supported("RPUSH", ClassRejectedMutation, "Rejected before upstream dispatch because list mutations are outside the v0.2 invalidation contract."),
	supported("SADD", ClassRejectedMutation, "Rejected before upstream dispatch because set mutations are outside the v0.2 invalidation contract."),
	supportedWithLimitations("SELECT", ClassLocal, "SELECT 0 is accepted as a local no-op.", "Databases other than 0 are rejected."),
	supportedWithLimitations("SET", ClassWrite, "The key is invalidated before upstream dispatch and remains invalidated if the upstream returns an error or the outcome is ambiguous. After an exact option-free SET succeeds, eligible cache-policy state may be refreshed locally.", "SET GET is rejected; only an exact option-free SET can refresh an already admitted cache-policy key."),
	supported("SETEX", ClassWrite, "The affected local entry is invalidated before upstream dispatch and remains invalidated if the upstream returns an error or the outcome is ambiguous."),
	supported("SREM", ClassRejectedMutation, "Rejected before upstream dispatch because set mutations are outside the v0.2 invalidation contract."),
	supported("SSUBSCRIBE", ClassRejectedStateful, "Rejected before upstream dispatch because Pub/Sub is connection-stateful and unsupported."),
	supported("SUBSCRIBE", ClassRejectedStateful, "Rejected before upstream dispatch because Pub/Sub is connection-stateful and unsupported."),
	supported("TTL", ClassPassThrough, "Forwarded to the upstream without local cache behavior."),
	supported("UNLINK", ClassWrite, "Affected local entries are invalidated before upstream dispatch and remain invalidated if the upstream returns an error or the outcome is ambiguous."),
	supported("UNWATCH", ClassRejectedStateful, "Rejected before upstream dispatch because transactions are stateful and unsupported."),
	supported("WATCH", ClassRejectedStateful, "Rejected before upstream dispatch because transactions are stateful and unsupported."),
	supported("XREAD", ClassRejectedBlocking, "Rejected before upstream dispatch because stream reads may block and are outside the v0.2 connection model."),
	supported("XREADGROUP", ClassRejectedBlocking, "Rejected before upstream dispatch because stream reads may block and are outside the v0.2 connection model."),
}

// Catalog returns the complete, deterministic command contract compiled into
// this Slizen binary. Commands not present in the catalog are unsupported.
func Catalog() []Entry {
	entries := make([]Entry, len(commandCatalog))
	copy(entries, commandCatalog[:])
	return entries
}

func Lookup(command string) Entry {
	command = strings.ToUpper(strings.TrimSpace(command))
	return LookupNormalized(command)
}

// LookupNormalized returns the contract entry for an already-trimmed,
// uppercase command name. Runtime dispatch uses this form after ParseCommand
// has normalized the command once.
func LookupNormalized(command string) Entry {
	index := sort.Search(len(commandCatalog), func(i int) bool {
		return commandCatalog[i].Command >= command
	})
	if index < len(commandCatalog) && commandCatalog[index].Command == command {
		return commandCatalog[index]
	}
	return Entry{
		Command:    command,
		Status:     StatusUnsupported,
		Class:      ClassUnsupported,
		Compatible: false,
		Behavior:   "Unsupported because it is not part of the v0.2 command contract.",
	}
}

func supported(command string, class Class, behavior string) Entry {
	status := StatusSupported
	compatible := true
	switch class {
	case ClassPassThrough:
		status = StatusPassThrough
	case ClassRejectedMutation, ClassRejectedStateful, ClassRejectedBlocking:
		status = StatusRejected
		compatible = false
	case ClassUnsupported:
		status = StatusUnsupported
		compatible = false
	}
	return Entry{
		Command:    command,
		Status:     status,
		Class:      class,
		Compatible: compatible,
		Behavior:   behavior,
	}
}

func supportedWithLimitations(command string, class Class, behavior, limitation string) Entry {
	entry := supported(command, class, behavior)
	entry.ArgumentReviewRequired = true
	entry.Limitations = limitation
	return entry
}
