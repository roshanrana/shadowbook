# enterprise-dev-lifecycle — partial install

`SKILL.md` is installed (copied from the account-synced copy of the skill, which
ships SKILL.md only). The **`references/` directory is still missing**:

    references/design-templates.md      # needed at Phase 1 (HLD) and Phase 2 (LLD)
    references/execution-planning.md    # needed at Phase 3 (execution plan)
    references/context-engineering.md   # needed at Phase 5
    references/orchestration.md         # needed at Phase 5
    references/validation-shipping.md   # needed at Phase 4, 6, 7

Unzip `enterprise-dev-lifecycle.zip` into this directory to supply them
(SKILL.md may be overwritten; it is the same file). Delete this README once
`references/` is present.
