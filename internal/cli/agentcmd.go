package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ramsterr/Wombat/internal/agents"
	"github.com/ramsterr/Wombat/internal/config"
)

func cmdAgent(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: wombat agent new <name> | wombat agent list")
	}
	switch args[0] {
	case "new", "create":
		return cmdAgentNew(args[1:])
	case "list", "ls":
		return cmdAgentList(args[1:])
	case "help", "-h", "--help":
		fmt.Print(agentHelp)
		return nil
	default:
		return fmt.Errorf("unknown agent command %q (use new or list)", args[0])
	}
}

const agentHelp = `custom agents

  wombat agent new <name> [--project]   create a starter definition
  wombat agent list                     list the definitions wombat can see

definitions live in:
  ~/.wombat/agents/<name>.json|.md      every project
  .wombat/agents/<name>.json|.md        this project (wins on conflicts)

select them inside a session with /agents, or for a one-shot run with:
  wombat run --agents <name1,name2> "..."
`

func cmdAgentNew(args []string) error {
	project := false
	var name string
	for _, arg := range args {
		switch arg {
		case "--project", "-p":
			project = true
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag %q", arg)
			}
			if name == "" {
				name = arg
			}
		}
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("usage: wombat agent new <name> [--project]")
	}
	def := &config.AgentDef{Name: name}
	if err := def.Validate(); err != nil {
		return err
	}
	dir, err := agentDefDir(project)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, def.Name+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	scaffold := fmt.Sprintf(`{
  "name": %q,
  "description": "TODO: when should the planner use this agent?",
  "prompt": "You are a specialist. TODO: describe your expertise, method, and quality bar.",
  "model": "",
  "tools": [],
  "exclude_tools": [],
  "mcp_servers": [],
  "permissions": {},
  "skills": [],
  "max_steps": 0,
  "readonly": false
}
`, def.Name)
	if err := os.WriteFile(path, []byte(scaffold), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s %s\n\n", bold("created"), path)
	fmt.Println(dim("edit the description, prompt, tools, and permissions, then:"))
	fmt.Printf("  %-42s %s\n", green("wombat run --agents "+def.Name+" \"...\""), dim("one-shot"))
	fmt.Printf("  %-42s %s\n", green("/agents "+def.Name), dim("inside the chat TUI"))
	fmt.Printf("  %-42s %s\n", green("wombat agent list"), dim("check what wombat sees"))
	return nil
}

func agentDefDir(project bool) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if project {
		return filepath.Join(cwd, ".wombat", "agents"), nil
	}
	return agents.DefDir(config.Home(), cwd), nil
}

func cmdAgentList(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	cwd, _ := os.Getwd()
	defs := agents.Load(cfg, config.Home(), cwd)
	if len(defs) == 0 {
		fmt.Println("no custom agents yet")
		fmt.Println(dim("create one with: wombat agent new <name>"))
		return nil
	}
	fmt.Printf("%-20s %-46s %s\n", bold("NAME"), bold("DESCRIPTION"), bold("SOURCE"))
	for _, loaded := range defs {
		if loaded.Err != nil {
			fmt.Printf("%-20s %s\n", loaded.Name(), red("invalid: "+loaded.Err.Error()))
			continue
		}
		description := loaded.Def.Description
		if description == "" {
			description = "—"
		}
		if len(description) > 44 {
			description = description[:44] + "…"
		}
		source := loaded.Source
		if source == "config.json" {
			source = "config"
		}
		fmt.Printf("%-20s %-46s %s\n", loaded.Def.Name, description, dim(source))
	}
	return nil
}
