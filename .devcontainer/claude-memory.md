## In this dev container

- The repository's `AGENTS.md` describes its layout, how to build, test and lint it, and its
  pitfalls. Read it before working on the code.
- You may run any command: the tokens in here give only the access this work needs, so
  there is no list of allowed commands. The one thing you must never do is merge: not a pull
  request (`gh pr merge`, `mergeBitbucketRepoPullRequest` in the Atlassian MCP, the merge endpoints
  of the GitHub or Bitbucket API, or any other way)
  and not a branch (`git merge`, or a `git pull` that merges another branch into this one).
  Merging bypasses the pull request's checks and review, so it is for a human. Integrate other
  work with `git rebase` and `git pull --rebase`, push your branch and open a pull request.
  The settings deny the usual forms of merging; do not look for a way around them, and if a task
  seems to need a merge, stop and say so.
- Jira, Confluence and Bitbucket pull requests go through the `atlassian` MCP server (v2), with
  the usual tools: `getJiraIssue`, `searchJiraIssuesUsingJql`, `searchConfluence` and so on;
  `discover` finds the rest, including the Bitbucket pull request operations
  (`createBitbucketRepoPullRequest` and the like). Its `executeDestructive` is denied.
- Submodules are not initialised when the container starts: that needs the forwarded ssh
  agent, which only connects once someone is in the container. If a git command or a build fails
  because of a missing or uninitialised submodule, run `git submodule update --init --recursive`,
  then retry.
- `gh` is signed in through `GH_TOKEN`, a fine-grained token limited to this repository (pull
  requests and read-only contents) and to reading `curityio/curity-ai-plugins`, when the host provided one; run `gh auth status` to see.
  The `curity-dev` plugin is installed by the `trust` start command; if it is missing, see
  `~/.curity-ai-plugins-install.log`.
