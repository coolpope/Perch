package agentsetup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"perch/skills"
)

func testEnvironment(t *testing.T) Environment {
	t.Helper()
	return Environment{Home: t.TempDir(), Executable: "/Applications/Perch's Copy.app/Contents/MacOS/Perch", SearchPath: os.Getenv("PATH")}
}

// TestClaudeCodeIsSetUpAndRemoved uses the real claude command on a private
// home folder.
func TestClaudeCodeIsSetUpAndRemoved(t *testing.T) {
	if _, err := exec.LookPath(claudeCLI); err != nil {
		t.Skip("claude is not installed")
	}
	env := testEnvironment(t)
	ctx := context.Background()
	status, err := Inspect(env)
	if err != nil {
		t.Fatal(err)
	}
	pending := status.Pending()
	if len(pending) != 3 {
		t.Fatalf("pending %+v", pending)
	}
	for _, target := range pending {
		if err := Install(ctx, env, target); err != nil {
			t.Fatalf("install %+v: %v", target, err)
		}
	}
	status, err = Inspect(env)
	if err != nil {
		t.Fatal(err)
	}
	if agent := status.Agents[0]; agent.Tools != StateInstalled || agent.Skill != StateInstalled || status.Command != StateInstalled {
		t.Fatalf("status %+v", status)
	}

	moved := env
	moved.Executable = "/Applications/Perch.app/Contents/MacOS/Perch"
	status, err = Inspect(moved)
	if err != nil {
		t.Fatal(err)
	}
	if status.Agents[0].Tools != StateOutdated || status.Command != StateOutdated {
		t.Fatalf("after moving the app: %+v", status)
	}
	if err := Install(ctx, moved, Target{Agent: AgentClaude, Component: ComponentTools}); err != nil {
		t.Fatal(err)
	}

	for _, target := range []Target{{AgentClaude, ComponentTools}, {AgentClaude, ComponentSkill}, {"", ComponentCommand}} {
		if err := Remove(ctx, moved, target); err != nil {
			t.Fatalf("remove %+v: %v", target, err)
		}
	}
	status, err = Inspect(moved)
	if err != nil {
		t.Fatal(err)
	}
	if agent := status.Agents[0]; agent.Tools != StateMissing || agent.Skill != StateMissing || status.Command != StateMissing {
		t.Fatalf("after removing: %+v", status)
	}
}

func TestSetupLeavesOtherFilesAlone(t *testing.T) {
	env := testEnvironment(t)
	ctx := context.Background()
	mine := "#!/bin/sh\necho my own perch\n"
	if err := os.MkdirAll(filepath.Dir(commandFile(env)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commandFile(env), []byte(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	target := Target{Component: ComponentCommand}
	if err := Install(ctx, env, target); !errors.Is(err, ErrBlocked) {
		t.Errorf("install over another file: %v", err)
	}
	if err := Remove(ctx, env, target); !errors.Is(err, ErrBlocked) {
		t.Errorf("remove another file: %v", err)
	}
	data, err := os.ReadFile(commandFile(env))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != mine {
		t.Errorf("the file became %q", data)
	}
}

func TestSkillInstallReplacesALinkWithoutWritingThroughIt(t *testing.T) {
	env := testEnvironment(t)
	source := filepath.Join(t.TempDir(), "perch")
	older := strings.Replace(skills.Perch, "# Working with Perch", "# An older skill", 1)
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, skillFileName), []byte(older), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Dir(claudeSkillFile(env))
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), env, Target{Agent: AgentClaude, Component: ComponentSkill}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(source, skillFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != older {
		t.Error("the install wrote through the link")
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("the link is still there")
	}
}
