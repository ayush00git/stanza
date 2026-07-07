# AGENTS.md

Guidance for AI coding agents working in this repository. Read this before making changes.

## Core rules

1. **One feature = one commit.** Each self-contained feature, fix, or change ships as its own commit. Don't batch unrelated changes into a single commit, and don't split one feature across many. Write a clear, descriptive commit message that explains what the feature does.

2. **Commit on `main`. Do not create branches.** All work lands directly on the `main` branch. Do not create feature branches, do not open PRs, do not switch branches.

3. **Do not push.** After committing, **notify the user that there are commits ready to push on `main`.** The user pushes. You commit only.

4. **Stay inside your assigned working directory.** The user will designate a specific directory/scope for you to work in. Confine all file changes to that scope.
   - Other agents may be working in other parts of the repo **at the same time**.
   - Touching files outside your assigned scope risks conflicts with those agents. Don't do it.
   - Only `git add` the files within your assigned scope — never `git add -A` or `git add .` from the repo root, since that can stage another agent's in-progress work.

## Workflow

For each feature:

1. Confirm you know your assigned working directory. If it wasn't given, **ask the user before starting.**
2. Implement the feature within that scope only.
3. Stage only the files you changed for this feature:
   ```
   git add <specific files within your scope>
   ```
4. Commit with a descriptive message:
   ```
   git commit -m "Add <feature>: <what it does>"
   ```
5. Repeat for the next feature — a new commit each time.
6. When done (or at a good stopping point), tell the user:
   > "There are N commit(s) ready on `main`. Please push when ready."

## Don't

- ❌ Don't `git push`.
- ❌ Don't create or switch branches.
- ❌ Don't `git add -A` / `git add .` from the repo root.
- ❌ Don't edit files outside your assigned working directory.
- ❌ Don't bundle multiple features into one commit.
- ❌ Don't rebase, reset, or rewrite history that could disrupt other agents.

## Why this exists

Multiple agents may operate in this repo concurrently, each isolated to its own directory. Keeping one feature per commit, committing only within your scope, and leaving the push to the user keeps everyone's work clean, reviewable, and conflict-free.
