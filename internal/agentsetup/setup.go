// Package agentsetup connects Perch to the coding agents on this machine: the
// tools an agent calls, the skill that teaches it to use them, and the perch
// terminal command. It changes only what it installed itself.
package agentsetup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"perch/skills"
)

// State says how one part of the setup stands.
type State string

const (
	// StateMissing is a part that is not installed.
	StateMissing State = "missing"
	// StateInstalled is a part that is current.
	StateInstalled State = "installed"
	// StateOutdated is a part from another version or location of Perch.
	StateOutdated State = "outdated"
	// StateBlocked is something else that already uses the part's name.
	StateBlocked State = "blocked"
)

// Component names a part of the setup.
type Component string

const (
	// ComponentTools is the MCP server an agent calls.
	ComponentTools Component = "tools"
	// ComponentSkill is the skill an agent reads.
	ComponentSkill Component = "skill"
	// ComponentCommand is the perch terminal command.
	ComponentCommand Component = "command"
)

// AgentID names a coding agent Perch can connect to.
type AgentID string

// AgentClaude is Claude Code.
const AgentClaude AgentID = "claude"

const (
	serverName     = "perch"
	mcpArgument    = "mcp"
	claudeCLI      = "claude"
	claudeName     = "Claude Code"
	managedSkill   = "<!-- Managed by Perch."
	managedCommand = "# Managed by Perch."

	skillFileName   = "SKILL.md"
	commandFileName = "perch"

	publicFileMode  fs.FileMode = 0o644
	commandFileMode fs.FileMode = 0o755
	publicDirMode   fs.FileMode = 0o755
)

// ErrBlocked reports a name that something other than Perch already uses.
var ErrBlocked = errors.New("something else already uses this name")

// ErrAgentNotFound reports an agent that is not installed on this machine.
var ErrAgentNotFound = errors.New("the agent is not installed")

// Environment is the machine a setup applies to.
type Environment struct {
	Home string
	// Executable is the Perch that agents will start.
	Executable string
	// SearchPath is where to look for the agents' commands.
	SearchPath string
}

// CurrentEnvironment describes this machine and this copy of Perch.
func CurrentEnvironment() (Environment, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Environment{}, fmt.Errorf("find the home folder: %w", err)
	}
	executable, err := os.Executable()
	if err != nil {
		return Environment{}, fmt.Errorf("find the perch executable: %w", err)
	}
	return Environment{Home: home, Executable: executable, SearchPath: os.Getenv("PATH")}, nil
}

// AgentStatus says how one agent is connected.
type AgentStatus struct {
	ID    AgentID
	Name  string
	Found bool
	Tools State
	Skill State
}

// Status says how this machine is set up.
type Status struct {
	Agents  []AgentStatus
	Command State
}

// Target is one part to install or remove. Agent is empty for the terminal
// command, which belongs to no agent.
type Target struct {
	Agent     AgentID
	Component Component
}

// Inspect reads the current setup without changing anything.
func Inspect(env Environment) (Status, error) {
	tools, err := claudeToolsState(env)
	if err != nil {
		return Status{}, err
	}
	skill, err := managedFileState(claudeSkillFile(env), skills.Perch, managedSkill)
	if err != nil {
		return Status{}, err
	}
	command, err := managedFileState(commandFile(env), commandScript(env.Executable), managedCommand)
	if err != nil {
		return Status{}, err
	}
	_, found := findCommand(env, claudeCLI)
	return Status{
		Agents:  []AgentStatus{{ID: AgentClaude, Name: claudeName, Found: found, Tools: tools, Skill: skill}},
		Command: command,
	}, nil
}

// Pending returns the parts that are not current and can be installed.
func (s Status) Pending() []Target {
	var targets []Target
	for _, agent := range s.Agents {
		if !agent.Found {
			continue
		}
		if needsInstall(agent.Tools) {
			targets = append(targets, Target{Agent: agent.ID, Component: ComponentTools})
		}
		if needsInstall(agent.Skill) {
			targets = append(targets, Target{Agent: agent.ID, Component: ComponentSkill})
		}
	}
	if needsInstall(s.Command) {
		targets = append(targets, Target{Component: ComponentCommand})
	}
	return targets
}

func needsInstall(state State) bool {
	return state == StateMissing || state == StateOutdated
}

// Install puts one part in place, replacing an older version of it.
func Install(ctx context.Context, env Environment, target Target) error {
	switch target.Component {
	case ComponentTools:
		return installClaudeTools(ctx, env)
	case ComponentSkill:
		return installManagedFile(claudeSkillFile(env), skills.Perch, managedSkill, publicFileMode)
	case ComponentCommand:
		return installManagedFile(commandFile(env), commandScript(env.Executable), managedCommand, commandFileMode)
	default:
		return fmt.Errorf("unknown component %q", target.Component)
	}
}

// Remove takes one part away, leaving alone what Perch did not install.
func Remove(ctx context.Context, env Environment, target Target) error {
	switch target.Component {
	case ComponentTools:
		return removeClaudeTools(ctx, env)
	case ComponentSkill:
		return removeSkill(env)
	case ComponentCommand:
		return removeManagedFile(commandFile(env), managedCommand)
	default:
		return fmt.Errorf("unknown component %q", target.Component)
	}
}

func claudeSkillFile(env Environment) string {
	return filepath.Join(env.Home, ".claude", "skills", serverName, skillFileName)
}

func commandFile(env Environment) string {
	return filepath.Join(env.Home, ".local", "bin", commandFileName)
}

// commandScript starts Perch in the background, so that the terminal stays
// free, and passes the agent commands straight through.
func commandScript(executable string) string {
	quoted := shellQuote(executable)
	return "#!/bin/sh\n" + managedCommand + "\n" +
		"perch=" + quoted + "\n" +
		`case "$1" in` + "\n" +
		`  mcp|setup) exec "$perch" "$@" ;;` + "\n" +
		"esac\n" +
		`if [ $# -eq 0 ]; then` + "\n" +
		`  nohup "$perch" >/dev/null 2>&1 &` + "\n" +
		"  exit 0\n" +
		"fi\n" +
		`folder=$(cd "$1" && pwd) || exit 1` + "\n" +
		`nohup "$perch" "$folder" >/dev/null 2>&1 &` + "\n"
}

// shellQuote returns text as one word of a POSIX shell.
func shellQuote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

// SetupCommand returns the line that installs everything from a terminal.
func SetupCommand(env Environment) string {
	return shellQuote(env.Executable) + " " + setupArgument
}

// managedFileState compares a file with the version Perch would write.
func managedFileState(file string, want string, marker string) (State, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return StateMissing, nil
	}
	if err != nil {
		return StateMissing, fmt.Errorf("read %q: %w", file, err)
	}
	switch text := string(data); {
	case text == want:
		return StateInstalled, nil
	case strings.Contains(text, marker):
		return StateOutdated, nil
	default:
		return StateBlocked, nil
	}
}

func installManagedFile(file string, content string, marker string, mode fs.FileMode) error {
	state, err := managedFileState(file, content, marker)
	if err != nil {
		return err
	}
	if state == StateBlocked {
		return fmt.Errorf("install %q: %w", file, ErrBlocked)
	}
	dir := filepath.Dir(file)
	if err := replaceManagedLink(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, publicDirMode); err != nil {
		return fmt.Errorf("create %q: %w", dir, err)
	}
	return writeFileAtomically(file, content, mode)
}

// replaceManagedLink removes a linked skill folder, so that an install never
// writes through the link into the folder it points at. Only skill folders are
// linked, and installManagedFile has already checked what the link holds.
func replaceManagedLink(dir string) error {
	if filepath.Base(dir) != serverName {
		return nil
	}
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %q: %w", dir, err)
	}
	if info.Mode()&fs.ModeSymlink == 0 {
		return nil
	}
	if err := os.Remove(dir); err != nil {
		return fmt.Errorf("replace the link %q: %w", dir, err)
	}
	return nil
}

func writeFileAtomically(file string, content string, mode fs.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(file), "."+filepath.Base(file)+"-*")
	if err != nil {
		return fmt.Errorf("write %q: %w", file, err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	_, writeErr := temporary.WriteString(content)
	if err := errors.Join(writeErr, temporary.Chmod(mode), temporary.Close()); err != nil {
		return fmt.Errorf("write %q: %w", file, err)
	}
	if err := os.Rename(temporary.Name(), file); err != nil {
		return fmt.Errorf("write %q: %w", file, err)
	}
	return nil
}

func removeManagedFile(file string, marker string) error {
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %q: %w", file, err)
	}
	if !strings.Contains(string(data), marker) {
		return fmt.Errorf("remove %q: %w", file, ErrBlocked)
	}
	if err := os.Remove(file); err != nil {
		return fmt.Errorf("remove %q: %w", file, err)
	}
	return nil
}

func removeSkill(env Environment) error {
	file := claudeSkillFile(env)
	dir := filepath.Dir(file)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %q: %w", dir, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		state, err := managedFileState(file, skills.Perch, managedSkill)
		if err != nil {
			return err
		}
		if state == StateBlocked {
			return fmt.Errorf("remove %q: %w", dir, ErrBlocked)
		}
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("remove %q: %w", dir, err)
		}
		return nil
	}
	if err := removeManagedFile(file, managedSkill); err != nil {
		return err
	}
	if err := os.Remove(dir); err != nil {
		return fmt.Errorf("remove %q, which holds other files: %w", dir, err)
	}
	return nil
}

type claudeServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type claudeConfig struct {
	Servers map[string]claudeServer `json:"mcpServers"`
}

func claudeConfigFile(env Environment) string {
	return filepath.Join(env.Home, ".claude.json")
}

// claudeToolsState reads the user-level server that Claude Code has on record.
func claudeToolsState(env Environment) (State, error) {
	file := claudeConfigFile(env)
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return StateMissing, nil
	}
	if err != nil {
		return StateMissing, fmt.Errorf("read %q: %w", file, err)
	}
	var config claudeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return StateMissing, fmt.Errorf("parse %q: %w", file, err)
	}
	server, listed := config.Servers[serverName]
	switch {
	case !listed:
		return StateMissing, nil
	case !slices.Equal(server.Args, []string{mcpArgument}):
		return StateBlocked, nil
	case server.Command == env.Executable:
		return StateInstalled, nil
	case !strings.EqualFold(filepath.Base(server.Command), serverName):
		return StateBlocked, nil
	default:
		return StateOutdated, nil
	}
}

func installClaudeTools(ctx context.Context, env Environment) error {
	state, err := claudeToolsState(env)
	if err != nil {
		return err
	}
	switch state {
	case StateInstalled:
		return nil
	case StateBlocked:
		return fmt.Errorf("add the %q server to %s: %w", serverName, claudeName, ErrBlocked)
	case StateOutdated:
		if err := runClaude(ctx, env, "mcp", "remove", "--scope", "user", serverName); err != nil {
			return err
		}
	}
	return runClaude(ctx, env, "mcp", "add", "--scope", "user", serverName, "--", env.Executable, mcpArgument)
}

func removeClaudeTools(ctx context.Context, env Environment) error {
	state, err := claudeToolsState(env)
	if err != nil {
		return err
	}
	switch state {
	case StateMissing:
		return nil
	case StateBlocked:
		return fmt.Errorf("remove the %q server from %s: %w", serverName, claudeName, ErrBlocked)
	default:
		return runClaude(ctx, env, "mcp", "remove", "--scope", "user", serverName)
	}
}

// runClaude changes the agent's configuration through its own command, which
// owns the file.
func runClaude(ctx context.Context, env Environment, args ...string) error {
	path, found := findCommand(env, claudeCLI)
	if !found {
		return fmt.Errorf("find %s: %w", claudeName, ErrAgentNotFound)
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = env.Home
	command.Env = append(os.Environ(), "HOME="+env.Home, "PATH="+strings.Join(searchDirs(env), string(os.PathListSeparator)))
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("run %s %s: %w: %s", claudeCLI, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

// searchDirs lists where commands live. An app opened from the Finder has a
// short PATH, so the usual install folders are added to it.
func searchDirs(env Environment) []string {
	dirs := filepath.SplitList(env.SearchPath)
	dirs = append(dirs, filepath.Join(env.Home, ".local", "bin"), filepath.Join(env.Home, ".claude", "local"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin")
	return slices.Compact(dirs)
}

func findCommand(env Environment, name string) (string, bool) {
	for _, dir := range searchDirs(env) {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return path, true
		}
	}
	return "", false
}
