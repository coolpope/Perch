package agentsetup

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// setupArgument is the subcommand that RunCLI serves.
const setupArgument = "setup"

// Exit codes of the setup command.
const (
	exitOK     int = 0
	exitFailed int = 1
	exitUsage  int = 2
)

var allComponents = []Component{ComponentTools, ComponentSkill, ComponentCommand}

// RunCLI serves "perch setup" and returns the exit code of the process.
func RunCLI(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("perch "+setupArgument, flag.ContinueOnError)
	flags.SetOutput(stderr)
	only := flags.String("only", "tools,skill,command", "parts to change, separated by commas: tools, skill, command")
	remove := flags.Bool("remove", false, "remove the parts instead of installing them")
	status := flags.Bool("status", false, "show the current setup and change nothing")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	components, err := parseComponents(*only)
	if err != nil || flags.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "perch setup: %v\n", errors.Join(err, unexpectedArguments(flags.Args())))
		return exitUsage
	}
	env, err := CurrentEnvironment()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch setup: %v\n", err)
		return exitFailed
	}
	current, err := Inspect(env)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "perch setup: %v\n", err)
		return exitFailed
	}
	if *status {
		printStatus(stdout, current)
		return exitOK
	}
	change := Install
	if *remove {
		change = Remove
	}
	return applyChanges(ctx, env, current, components, change, stdout, stderr)
}

func unexpectedArguments(args []string) error {
	if len(args) == 0 {
		return nil
	}
	return fmt.Errorf("unexpected arguments: %s", strings.Join(args, " "))
}

func parseComponents(list string) ([]Component, error) {
	var components []Component
	for name := range strings.SplitSeq(list, ",") {
		component := Component(strings.TrimSpace(name))
		if !slices.Contains(allComponents, component) {
			return nil, fmt.Errorf("unknown part %q; choose from tools, skill, command", name)
		}
		components = append(components, component)
	}
	return components, nil
}

// applyChanges changes every chosen part, continuing past a failure so that one
// blocked part does not hold back the others.
func applyChanges(
	ctx context.Context,
	env Environment,
	current Status,
	components []Component,
	change func(context.Context, Environment, Target) error,
	stdout io.Writer,
	stderr io.Writer,
) int {
	code := exitOK
	for _, target := range chosenTargets(current, components, stdout) {
		if err := change(ctx, env, target); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", targetName(target), err)
			code = exitFailed
			continue
		}
		_, _ = fmt.Fprintf(stdout, "%s: done\n", targetName(target))
	}
	if slices.Contains(components, ComponentCommand) && !onSearchPath(env) {
		_, _ = fmt.Fprintf(stdout, "Add %s to your PATH to use the perch command.\n", filepath.Dir(commandFile(env)))
	}
	return code
}

// chosenTargets lists the chosen parts of the agents on this machine.
func chosenTargets(current Status, components []Component, stdout io.Writer) []Target {
	var targets []Target
	for _, agent := range current.Agents {
		if !agent.Found {
			_, _ = fmt.Fprintf(stdout, "%s: not found, skipped\n", agent.Name)
			continue
		}
		for _, component := range components {
			if component != ComponentCommand {
				targets = append(targets, Target{Agent: agent.ID, Component: component})
			}
		}
	}
	if slices.Contains(components, ComponentCommand) {
		targets = append(targets, Target{Component: ComponentCommand})
	}
	return targets
}

func targetName(target Target) string {
	if target.Component == ComponentCommand {
		return "terminal command"
	}
	return claudeName + " " + string(target.Component)
}

func onSearchPath(env Environment) bool {
	return slices.Contains(filepath.SplitList(os.Getenv("PATH")), filepath.Dir(commandFile(env)))
}

func printStatus(stdout io.Writer, current Status) {
	for _, agent := range current.Agents {
		if !agent.Found {
			_, _ = fmt.Fprintf(stdout, "%s: not found\n", agent.Name)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "%s tools: %s\n%s skill: %s\n", agent.Name, agent.Tools, agent.Name, agent.Skill)
	}
	_, _ = fmt.Fprintf(stdout, "terminal command: %s\n", current.Command)
}
