package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/skills"
)

// cmdSkill manages the skill catalogue. Skills are instruction files the
// agent loads on demand with use_skill; they are never pasted into the
// system prompt.
func cmdSkill(args []string) error {
	cwd, _ := os.Getwd()
	home := config.Home()
	if len(args) == 0 {
		return skillList(cwd, home)
	}
	switch strings.ToLower(args[0]) {
	case "list", "ls":
		return skillList(cwd, home)
	case "path", "dir":
		fmt.Println(skills.StoreDir(home))
		return nil
	case "install", "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: wombat skill install <path|url|name>")
		}
		for _, source := range args[1:] {
			installed, err := skills.Install(source, home)
			if err != nil {
				return err
			}
			for _, skill := range installed {
				fmt.Printf("installed %s → %s\n", skill.Name, skill.Source)
			}
		}
		return nil
	case "remove", "rm", "uninstall":
		if len(args) < 2 {
			return fmt.Errorf("usage: wombat skill remove <name>")
		}
		for _, name := range args[1:] {
			path, err := skills.Remove(name, home)
			if err != nil {
				return err
			}
			fmt.Printf("removed %s (%s)\n", name, path)
		}
		return nil
	case "show", "cat":
		if len(args) < 2 {
			return skillList(cwd, home)
		}
		skill, err := skills.Find(args[1], cwd, home)
		if err != nil {
			return err
		}
		fmt.Printf("skill: %s\n", skill.Name)
		if skill.Description != "" {
			fmt.Println(skill.Description)
		}
		fmt.Printf("source: %s\n\n%s\n", skill.Source, skill.PromptFor())
		return nil
	default:
		// A bare name is a show request.
		skill, err := skills.Find(args[0], cwd, home)
		if err != nil {
			return err
		}
		fmt.Printf("skill: %s\n", skill.Name)
		if skill.Description != "" {
			fmt.Println(skill.Description)
		}
		fmt.Printf("source: %s\n\n%s\n", skill.Source, skill.PromptFor())
		return nil
	}
}

func skillList(cwd, home string) error {
	list, problems := skills.Load(cwd, home)
	fmt.Printf("skills: %d installed\n", len(list))
	if len(list) == 0 {
		fmt.Println("  install one with `wombat skill install <path|url|name>`")
		fmt.Printf("  or add %s/<name>.md\n", skills.StoreDir(home))
	}
	for _, skill := range list {
		line := "  " + skill.Name
		if skill.Description != "" {
			line += " — " + clipLine(skill.Description, 110)
		}
		fmt.Println(line)
		fmt.Println(dim("      " + skill.Source))
	}
	for _, problem := range problems {
		fmt.Println("  ! " + problem.String())
	}
	return nil
}
