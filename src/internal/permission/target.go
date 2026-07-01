package permission

import "fmt"

// ExtractTarget returns the primary string that permissions rules and hard
// checks should evaluate for a tool call.
func ExtractTarget(req Request) Target {
	tool := NormalizeToolName(req.Tool)
	switch tool {
	case "bash":
		command := stringArg(req.Args, "command")
		return Target{Kind: TargetCommand, Value: command, Command: command}
	case "read_file", "write_file", "edit_file":
		path := stringArg(req.Args, "path")
		return Target{Kind: TargetPath, Value: path, Path: path}
	case "glob":
		root := stringArg(req.Args, "path")
		if root == "" {
			root = "."
		}
		glob := stringArg(req.Args, "pattern")
		return Target{Kind: TargetSearch, Value: root, SearchRoot: root, Glob: glob}
	case "grep":
		root := stringArg(req.Args, "path")
		if root == "" {
			root = "."
		}
		glob := stringArg(req.Args, "glob")
		return Target{Kind: TargetSearch, Value: root, SearchRoot: root, Glob: glob}
	default:
		return Target{
			Kind:  TargetUnknown,
			Value: fmt.Sprintf("%s %v", req.Tool, req.Args),
		}
	}
}

func stringArg(args map[string]interface{}, name string) string {
	if args == nil {
		return ""
	}
	value, ok := args[name]
	if !ok || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}
