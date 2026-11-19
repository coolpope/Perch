package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/egoist/mygo/ui"

	"perch/internal/agentsetup"
	"perch/locales"
)

// agentSetupTimeout bounds one change, which runs the agent's own command.
const agentSetupTimeout time.Duration = time.Minute

// agentsPage is the settings page that connects Perch to coding agents.
type agentsPage struct {
	open    bool
	status  agentsetup.Status
	checked bool
	working bool
	copied  bool
}

// showAgents opens the page and reads the current setup.
func (a *App) showAgents() {
	a.agents = agentsPage{open: true, working: true}
	a.changeAgentSetup(func(context.Context, agentsetup.Environment) error { return nil })
}

// changeAgentSetup runs a change off the main thread and reads the setup again.
// The goroutine ends with the change, within agentSetupTimeout.
func (a *App) changeAgentSetup(change func(context.Context, agentsetup.Environment) error) {
	a.agents.working = true
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), agentSetupTimeout)
		defer cancel()
		status, err := applyAgentChange(ctx, change)
		a.post(func() { a.agentSetupChanged(status, err) })
	}()
}

func applyAgentChange(ctx context.Context, change func(context.Context, agentsetup.Environment) error) (agentsetup.Status, error) {
	env, err := agentsetup.CurrentEnvironment()
	if err != nil {
		return agentsetup.Status{}, err
	}
	changeErr := change(ctx, env)
	status, err := agentsetup.Inspect(env)
	if changeErr != nil {
		return status, changeErr
	}
	return status, err
}

func (a *App) agentSetupChanged(status agentsetup.Status, err error) {
	if err != nil {
		slog.Warn("change agent setup", "error", err)
		a.notice = toast{title: locales.AgentSetupFailed(), detail: err.Error()}
	}
	if a.agents.open {
		a.agents.status, a.agents.checked, a.agents.working = status, true, false
	}
}

func (a *App) installAgentTarget(target agentsetup.Target) {
	a.changeAgentSetup(func(ctx context.Context, env agentsetup.Environment) error {
		return agentsetup.Install(ctx, env, target)
	})
}

func (a *App) removeAgentTarget(target agentsetup.Target) {
	a.changeAgentSetup(func(ctx context.Context, env agentsetup.Environment) error {
		return agentsetup.Remove(ctx, env, target)
	})
}

// installPendingAgentTargets installs every part that is not current, and stops
// at the first failure, which the person is then told about.
func (a *App) installPendingAgentTargets() {
	pending := a.agents.status.Pending()
	a.changeAgentSetup(func(ctx context.Context, env agentsetup.Environment) error {
		for _, target := range pending {
			if err := agentsetup.Install(ctx, env, target); err != nil {
				return err
			}
		}
		return nil
	})
}

func (a *App) agentsContent(c *ui.Context) {
	colors := a.look.palette
	ui.Column(c).FillWidth().Padding(6, 20, 16, 20).Children(func() {
		ui.Row(c).FillWidth().Height(48).Gap(6).AlignItems(ui.Center).Children(func() {
			back := ui.Box(c).Size(24, 24).Center().Radius(radiusSmall).Label(locales.LanguagesBack())
			if back.Hovered() {
				back.Background(colors.hover)
			}
			if back.Clicked() {
				a.agents = agentsPage{}
			}
			back.Children(func() { ui.Icon(c, iconBack).FontSize(15).TextColor(colors.muted) })
			ui.Text(c, locales.AgentsTitle()).FontSize(16).FontWeight(600)
		})
		if !a.agents.checked {
			ui.Text(c, locales.LanguageChecking()).TextColor(colors.muted).Padding(8, 0, 16, 0)
			return
		}
		for _, agent := range a.agents.status.Agents {
			c.Key(string(agent.ID))
			a.agentSectionView(c, agent)
		}
		a.settingsSection(c, locales.SectionTerminal(), func() {
			a.agentPartRow(c, locales.TerminalCommandName(), locales.TerminalCommandDetail(), agentsetup.Target{Component: agentsetup.ComponentCommand}, a.agents.status.Command)
		})
		a.agentsFooterView(c)
	})
}

func (a *App) agentSectionView(c *ui.Context, agent agentsetup.AgentStatus) {
	a.settingsSection(c, agent.Name, func() {
		if !agent.Found {
			ui.Text(c, locales.AgentNotFound()).TextColor(a.look.palette.muted).Padding(8, 0, 8, 0)
			return
		}
		a.agentPartRow(c, locales.AgentToolsName(), locales.AgentToolsDetail(), agentsetup.Target{Agent: agent.ID, Component: agentsetup.ComponentTools}, agent.Tools)
		a.agentPartRow(c, locales.AgentSkillName(), locales.AgentSkillDetail(), agentsetup.Target{Agent: agent.ID, Component: agentsetup.ComponentSkill}, agent.Skill)
	})
}

func (a *App) agentPartRow(c *ui.Context, name string, detail string, target agentsetup.Target, state agentsetup.State) {
	colors := a.look.palette
	c.Key(name)
	ui.Row(c).FillWidth().Height(languageRowHeight).Gap(10).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(colors.border).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Gap(3).Children(func() {
			ui.Text(c, name).MaxLines(1)
			ui.Text(c, detail).FontSize(smallFontSize - 1).TextColor(colors.muted).MaxLines(1)
		})
		a.agentPartControl(c, name, target, state)
	})
}

func (a *App) agentPartControl(c *ui.Context, name string, target agentsetup.Target, state agentsetup.State) {
	switch state {
	case agentsetup.StateBlocked:
		ui.Text(c, locales.AgentBlocked()).FontSize(smallFontSize).TextColor(a.look.palette.warning).Shrink(0)
	case agentsetup.StateOutdated:
		if ui.Button(c, locales.AgentUpdate()).Disabled(a.agents.working).Shrink(0).Clicked() {
			a.installAgentTarget(target)
		}
	default:
		installed := state == agentsetup.StateInstalled
		on := installed
		ui.Switch(c, &on).Disabled(a.agents.working).Label(name)
		switch {
		case on && !installed:
			a.installAgentTarget(target)
		case !on && installed:
			a.removeAgentTarget(target)
		}
	}
}

func (a *App) agentsFooterView(c *ui.Context) {
	ui.Column(c).FillWidth().Gap(10).Padding(12, 0, 0, 0).Children(func() {
		ui.Text(c, locales.AgentsHint()).FontSize(smallFontSize).TextColor(a.look.palette.muted)
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Spacer(c)
			label := locales.CopySetupCommand()
			if a.agents.copied {
				label = locales.CopiedToClipboard()
			}
			if ui.Button(c, label).Clicked() {
				a.copyAgentSetupCommand(c)
			}
			pending := len(a.agents.status.Pending()) > 0
			if ui.PrimaryButton(c, locales.InstallEverything()).Disabled(a.agents.working || !pending).Clicked() {
				a.installPendingAgentTargets()
			}
		})
	})
}

func (a *App) copyAgentSetupCommand(c *ui.Context) {
	env, err := agentsetup.CurrentEnvironment()
	if err != nil {
		a.notice = toast{title: locales.AgentSetupFailed(), detail: err.Error()}
		return
	}
	c.WriteClipboard(agentsetup.SetupCommand(env))
	a.agents.copied = true
}
