FROM mcr.microsoft.com/devcontainers/go:1-1.23-bookworm

# protoc, for hack/build-grpc.sh.
RUN apt-get update \
    && apt-get install -y --no-install-recommends protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

# Claude is installed here rather than by a dev container feature, and its configuration
# lives in one directory that is part of the image: the login comes from
# CLAUDE_CODE_OAUTH_TOKEN, and these files hold everything else (onboarding and folder
# trust, the Atlassian MCP server, permissions, what the container's Claude remembers).
USER vscode
ENV CLAUDE_CONFIG_DIR=/home/vscode/.claude \
    PATH=/home/vscode/.local/bin:$PATH
RUN curl -fsSL https://claude.ai/install.sh | bash

COPY --chown=vscode:vscode .claude.json /home/vscode/.claude/.claude.json
COPY --chown=vscode:vscode claude-settings.json /home/vscode/.claude/settings.json
COPY --chown=vscode:vscode claude-memory.md /home/vscode/.claude/CLAUDE.md
# GitHub's and Bitbucket's published host keys, pinned rather than fetched with ssh-keyscan
# at start, which would trust whatever answered.
# ~/.ssh is made first, so that vscode owns it and ssh can update known_hosts.
RUN mkdir -m 700 -p /home/vscode/.ssh
COPY --chown=vscode:vscode known_hosts /home/vscode/.ssh/known_hosts
USER root
