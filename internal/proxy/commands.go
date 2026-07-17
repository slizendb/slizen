package proxy

import (
	"fmt"
	"strings"

	"github.com/tidwall/redcon"
)

type ParsedCommand struct {
	Name string
	Args []string
}

func ParseCommand(cmd redcon.Command) (ParsedCommand, error) {
	if len(cmd.Args) == 0 {
		return ParsedCommand{}, fmt.Errorf("empty command")
	}
	args := make([]string, len(cmd.Args))
	for i, arg := range cmd.Args {
		args[i] = string(arg)
	}
	return ParsedCommand{Name: strings.ToUpper(args[0]), Args: args}, nil
}

func wrongArity(name string) string {
	return "ERR wrong number of arguments for '" + strings.ToLower(name) + "' command"
}

func unsupported(name string) string {
	return "ERR unsupported command '" + strings.ToLower(name) + "' in Slizen"
}

func rejectedUnsafe(name string) string {
	return "ERR command '" + strings.ToLower(name) + "' is stateful or unsafe and is not supported by Slizen"
}

func rejectedSetGet() string {
	return "ERR SET GET option is not supported by Slizen"
}

func rejectedMutation(name string) string {
	return "ERR mutating command '" + strings.ToLower(name) + "' is not supported by Slizen"
}

func setUsesGetOption(options []string) bool {
	for i := 0; i < len(options); i++ {
		switch strings.ToUpper(options[i]) {
		case "GET":
			return true
		case "EX", "PX", "EXAT", "PXAT":
			i++
		}
	}
	return false
}

func isUnsafeCommand(name string) bool {
	switch name {
	case "MULTI", "EXEC", "WATCH", "UNWATCH", "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "MONITOR":
		return true
	default:
		return isBlockingCommand(name)
	}
}

func isRejectedMutation(name string) bool {
	switch name {
	case "MSET", "RENAME", "HSET", "HDEL", "LPUSH", "RPUSH", "LPOP", "RPOP", "SADD", "SREM":
		return true
	default:
		return false
	}
}

func isBlockingCommand(name string) bool {
	switch name {
	case "BLPOP", "BRPOP", "BRPOPLPUSH", "BLMOVE", "BZPOPMIN", "BZPOPMAX", "XREAD", "XREADGROUP":
		return true
	default:
		return false
	}
}
