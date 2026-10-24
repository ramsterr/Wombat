package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/mcp"
	"github.com/ramsterr/Wombat/internal/tools"
)

func cmdMCP(args []string) error {
	if len(args) == 0 {
		return mcpUsage()
	}
	switch args[0] {
	case "list", "ls":
		return mcpList(args[1:])
	case "add":
		return mcpAdd(args[1:])
	case "remove", "rm":
		return mcpRemove(args[1:])
	case "enable", "disable":
		return mcpToggle(args[0], args[1:])
	case "test", "tools":
		return mcpTest(args[1:])
	case "import":
		return mcpImport(args[1:])
	case "help", "-h", "--help":
		return mcpUsage()
	default:
		return fmt.Errorf("unknown mcp command %q", args[0])
	}
}

func mcpUsage() error {
	fmt.Print(`usage:
  wombat mcp list
  wombat mcp add <name> [--env KEY=VALUE] [--header KEY=VALUE] [--cwd DIR]
                            [--timeout SECONDS] [--class CLASS] [--workers] -- <command> [args...]
  wombat mcp add <name> --url <url> [--header KEY=VALUE] [--type http|sse]
                            [--timeout SECONDS] [--class CLASS]
  wombat mcp remove <name>
  wombat mcp enable <name>
  wombat mcp disable <name>
  wombat mcp test [name...]
  wombat mcp import <file.json>

examples:
  wombat mcp add everything -- npx -y @modelcontextprotocol/server-everything
  wombat mcp add context7 --url https://mcp.context7.com/mcp
  wombat mcp add github --url https://api.githubcopilot.com/mcp/ \
    --header 'Authorization=Bearer ${GITHUB_TOKEN}'`)
	return nil
}

func mcpList(args []string) error {
	cfg := mustConfig()
	if len(cfg.MCPServers) == 0 {
		fmt.Println("no MCP servers configured")
		fmt.Println(dim("add one with `wombat mcp add <name> -- <command>` or `wombat mcp add <name> --url <url>`"))
		return nil
	}
	names := sortedMCPNames(cfg.MCPServers)
	fmt.Printf("%s (%d)\n", bold("MCP servers"), len(names))
	for _, name := range names {
		server := cfg.MCPServers[name]
		state := green("enabled")
		if !server.EffectiveEnabled() {
			state = dim("disabled")
		}
		fmt.Printf("  %-18s %-7s %-8s %s\n", bold(name), server.Transport(), state, dim(tools.RedactSecrets(mcpServerSummary(server))))
	}
	return nil
}

func mcpAdd(args []string) error {
	if len(args) == 0 {
		return mcpUsage()
	}
	name := args[0]
	if err := validateMCPName(name); err != nil {
		return err
	}
	cfg := mustConfig()
	server, err := parseMCPAdd(name, args[1:])
	if err != nil {
		return err
	}
	action := "added"
	if _, exists := cfg.MCPServers[name]; exists {
		action = "updated"
	}
	cfg.MCPServers[name] = server
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("%s %s %s\n", green(action+":"), bold(name), dim(tools.RedactSecrets(mcpServerSummary(server))))
	fmt.Println(dim("test it with `wombat mcp test " + name + "`"))
	return nil
}

func mcpRemove(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: wombat mcp remove <name>")
	}
	cfg := mustConfig()
	if _, ok := cfg.MCPServers[args[0]]; !ok {
		return fmt.Errorf("MCP server %q is not configured", args[0])
	}
	delete(cfg.MCPServers, args[0])
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println(green("removed: ") + bold(args[0]))
	return nil
}

func mcpToggle(action string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: wombat mcp %s <name>", action)
	}
	cfg := mustConfig()
	server, ok := cfg.MCPServers[args[0]]
	if !ok || server == nil {
		return fmt.Errorf("MCP server %q is not configured", args[0])
	}
	enabled := action == "enable"
	server.Disabled = false
	server.Enabled = &enabled
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println(green(action+"d: ") + bold(args[0]))
	return nil
}

func mcpTest(args []string) error {
	cfg := mustConfig()
	selected := map[string]*config.MCPServer{}
	if len(args) == 0 {
		selected = cfg.MCPServers
	} else {
		for _, name := range args {
			server, ok := cfg.MCPServers[name]
			if !ok || server == nil {
				return fmt.Errorf("MCP server %q is not configured", name)
			}
			selected[name] = server
		}
	}
	if len(selected) == 0 {
		fmt.Println("no MCP servers configured")
		return nil
	}
	for name, server := range selected {
		if server != nil && !server.EffectiveEnabled() {
			fmt.Printf("%s %-16s %s\n", dim("○"), bold(name), dim("disabled"))
			delete(selected, name)
		}
	}
	if len(selected) == 0 {
		return nil
	}
	cwd, _ := os.Getwd()
	manager := mcp.Connect(context.Background(), selected, cwd, Version)
	defer manager.Close()
	failed := false
	for _, server := range manager.Servers() {
		if err := server.Err(); err != nil {
			failed = true
			text := tools.RedactSecrets(err.Error())
			fmt.Printf("%s %-16s %s\n", red("✗"), bold(server.Name()), red(text))
			lower := strings.ToLower(text)
			if strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "oauth") {
				fmt.Println(dim("    this server may need OAuth or an API token; add one with --header 'Authorization=Bearer ...'"))
			}
			continue
		}
		fmt.Printf("%s %-16s %s\n", green("✓"), bold(server.Name()), dim(fmt.Sprintf("%d tools", len(server.Tools()))))
		for _, tool := range server.Tools() {
			desc := strings.TrimSpace(tool.Description)
			if desc == "" {
				desc = "no description"
			}
			fmt.Printf("    %-28s %s\n", tool.Name, dim(clipLine(desc, 100)))
		}
	}
	if failed {
		return fmt.Errorf("one or more MCP servers failed to connect")
	}
	return nil
}

func mcpImport(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: wombat mcp import <file.json>")
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	imported, err := parseMCPFile(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", args[0], err)
	}
	if len(imported) == 0 {
		return fmt.Errorf("no MCP servers found in %s", args[0])
	}
	cfg := mustConfig()
	added := 0
	for name, server := range imported {
		if err := validateMCPName(name); err != nil {
			fmt.Fprintln(os.Stderr, yellow("skipped "+name+": "+err.Error()))
			continue
		}
		cfg.MCPServers[name] = server
		added++
	}
	if added == 0 {
		return fmt.Errorf("no valid MCP servers found in %s", args[0])
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("%s %d MCP server(s) from %s\n", green("imported:"), added, bold(filepath.Base(args[0])))
	return nil
}

func parseMCPAdd(name string, args []string) (*config.MCPServer, error) {
	flags, command := splitMCPArgs(args)
	fs := flag.NewFlagSet("mcp add "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var (
		url, transport, cwd, class string
		envs, headers              stringList
		allow, exclude             stringList
		timeout, callTimeout       int
		workers                    bool
	)
	fs.StringVar(&url, "url", "", "remote MCP endpoint")
	fs.StringVar(&transport, "type", "", "stdio, http, or sse")
	fs.StringVar(&transport, "transport", "", "alias for --type")
	fs.StringVar(&cwd, "cwd", "", "working directory for a local server")
	fs.StringVar(&class, "class", "", "tool class: mcp, read, network, write")
	fs.Var(&envs, "env", "environment variable KEY=VALUE (repeatable)")
	fs.Var(&headers, "header", "HTTP header KEY=VALUE or KEY: VALUE (repeatable)")
	fs.Var(&allow, "allow-tool", "only expose this tool (repeatable, supports *)")
	fs.Var(&exclude, "exclude-tool", "hide this tool (repeatable, supports *)")
	fs.IntVar(&timeout, "timeout", 0, "startup timeout in seconds")
	fs.IntVar(&callTimeout, "call-timeout", 0, "tool call timeout in seconds")
	fs.BoolVar(&workers, "workers", false, "allow delegated workers to use this server's tools")
	if err := fs.Parse(flags); err != nil {
		return nil, err
	}
	if fs.NArg() > 0 {
		return nil, fmt.Errorf("unexpected argument %q; put the server command after --", fs.Arg(0))
	}
	server := &config.MCPServer{
		Type:           transport,
		URL:            url,
		Cwd:            cwd,
		Class:          class,
		Workers:        workers,
		TimeoutSeconds: timeout,
		CallSeconds:    callTimeout,
	}
	if len(command) > 0 {
		server.Command = command[0]
		server.Args = command[1:]
	}
	if len(envs) > 0 {
		server.Env = map[string]string{}
		for _, item := range envs {
			key, value, ok := strings.Cut(item, "=")
			if !ok || strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("bad --env %q: want KEY=VALUE", item)
			}
			server.Env[strings.TrimSpace(key)] = value
		}
	}
	if len(headers) > 0 {
		server.Headers = map[string]string{}
		for _, item := range headers {
			key, value, ok := strings.Cut(item, "=")
			if !ok {
				key, value, ok = strings.Cut(item, ":")
			}
			if !ok || strings.TrimSpace(key) == "" {
				return nil, fmt.Errorf("bad --header %q: want KEY=VALUE or KEY: VALUE", item)
			}
			server.Headers[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	server.AllowTools = allow
	server.ExcludeTools = exclude
	if server.Command == "" && server.URL == "" {
		return nil, fmt.Errorf("add a command after -- (stdio) or pass --url (http/sse)")
	}
	if server.Command != "" && server.URL != "" {
		return nil, fmt.Errorf("use either --url or a command after --, not both")
	}
	switch server.Transport() {
	case "stdio", "http", "sse":
	default:
		return nil, fmt.Errorf("unsupported transport %q: use stdio, http, or sse", server.Type)
	}
	if server.Transport() != "stdio" && server.Command != "" {
		return nil, fmt.Errorf("a command after -- is only valid for the stdio transport")
	}
	if server.Transport() == "stdio" && server.URL != "" {
		return nil, fmt.Errorf("--url is only valid for the http or sse transport")
	}
	switch strings.ToLower(class) {
	case "", "mcp", "read", "network", "write", "edit", "bash":
	default:
		return nil, fmt.Errorf("unsupported class %q: use mcp, read, network, write, edit, or bash", class)
	}
	if server.Transport() == "stdio" && server.Type == "" {
		server.Type = "stdio"
	}
	if server.Transport() == "http" && server.Type == "" {
		server.Type = "http"
	}
	return server, nil
}

func splitMCPArgs(args []string) (flags []string, command []string) {
	for i, arg := range args {
		if arg == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

func parseMCPFile(raw []byte) (map[string]*config.MCPServer, error) {
	var file struct {
		MCPServers map[string]*config.MCPServer `json:"mcpServers"`
		MCP        struct {
			Servers map[string]*config.MCPServer `json:"servers"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if len(file.MCPServers) > 0 {
		return file.MCPServers, nil
	}
	if len(file.MCP.Servers) > 0 {
		return file.MCP.Servers, nil
	}
	return nil, nil
}

func mcpServerSummary(server *config.MCPServer) string {
	if server == nil {
		return ""
	}
	if server.Transport() == "stdio" {
		parts := append([]string{server.Command}, server.Args...)
		return strings.Join(parts, " ")
	}
	return server.URL
}

func sortedMCPNames(servers map[string]*config.MCPServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func validateMCPName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("MCP server name is required")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return fmt.Errorf("MCP server name may only contain letters, digits, dots, dashes, and underscores")
		}
	}
	return nil
}
