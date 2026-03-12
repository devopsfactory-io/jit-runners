---
name: maintain-documentation
description: Ensures human and AI documentation stay in sync with code and config. Use when changing behavior, adding features, refactoring, or when the user asks to update docs. Delegates the actual updates to the documentation-maintainer subagent.
---

# Maintain Documentation

## When to Use

Use this skill when:
- Changing Lambda behavior, IaC resources, or config
- Adding or changing env vars or AWS resource structure
- Refactoring project structure or conventions
- Modifying CI or release workflows
- The user asks to update or review documentation

## What to Do

**Delegate documentation updates to the documentation-maintainer subagent** (`.claude/agents/documentation-maintainer.md`).

When this skill applies, invoke the **documentation-maintainer** subagent with a prompt that describes what changed (e.g. Lambda code, IaC, config, CI, or structure) so it can run its full checklist and update README, docs/, infra/, AGENTS.md, CLAUDE.md, .claude/commands, and .claude/skills as needed. The subagent owns the detailed checklist and the "where things live" / "when to update" mapping; do not duplicate that work — delegate it.
